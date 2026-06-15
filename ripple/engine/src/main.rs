//! Ripple graph engine.
//!
//! Orbit's query DSL is capped at 3 hops with no transitive closure, so deep
//! call chains cannot be analyzed by the API alone. This engine ingests a
//! normalized code subgraph (assembled by the Go agent from bounded Orbit
//! queries) and computes the *full* transitive change-impact ("blast radius").
//!
//! Semantics: a `CALLS` edge `A -> B` means "A calls B". Changing B therefore
//! affects everything that transitively calls B, so the blast radius of a
//! changed definition is its transitive set of callers (reverse reachability).

use std::collections::{HashMap, HashSet, VecDeque};
use std::env;
use std::fs;

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Deserialize)]
struct Node {
    id: String,
    #[serde(default)]
    name: String,
    #[serde(default)]
    file_path: String,
    #[serde(default)]
    definition_type: String,
}

#[derive(Debug, Deserialize)]
struct Edge {
    #[serde(default, rename = "type")]
    etype: String,
    #[serde(default)]
    from: String,
    #[serde(default)]
    to: String,
}

#[derive(Debug, Deserialize)]
struct Graph {
    nodes: Vec<Node>,
    edges: Vec<Edge>,
}

#[derive(Debug, Serialize)]
struct Impacted {
    id: String,
    name: String,
    file_path: String,
    definition_type: String,
    distance: u32,
}

#[derive(Debug, Serialize)]
struct Report {
    changed: Vec<String>,
    impacted_count: usize,
    max_depth: u32,
    blast_radius: Vec<Impacted>,
}

/// Compute the transitive caller set (blast radius) of the changed definitions.
fn analyze(graph: &Graph, changed: &[String]) -> Report {
    let node_by_id: HashMap<&str, &Node> =
        graph.nodes.iter().map(|n| (n.id.as_str(), n)).collect();

    // Reverse adjacency: callee -> [callers]. Treat empty edge type as CALLS.
    let mut callers: HashMap<&str, Vec<&str>> = HashMap::new();
    for e in &graph.edges {
        if e.etype == "CALLS" || e.etype.is_empty() {
            callers.entry(e.to.as_str()).or_default().push(e.from.as_str());
        }
    }

    let mut dist: HashMap<&str, u32> = HashMap::new();
    let mut queue: VecDeque<&str> = VecDeque::new();
    for c in changed {
        if let Some(n) = node_by_id.get(c.as_str()) {
            if dist.insert(n.id.as_str(), 0).is_none() {
                queue.push_back(n.id.as_str());
            }
        }
    }

    while let Some(cur) = queue.pop_front() {
        let d = dist[cur];
        if let Some(cs) = callers.get(cur) {
            for &caller in cs {
                if !dist.contains_key(caller) {
                    dist.insert(caller, d.saturating_add(1));
                    queue.push_back(caller);
                }
            }
        }
    }

    let changed_set: HashSet<&str> = changed.iter().map(|s| s.as_str()).collect();
    let mut blast: Vec<Impacted> = dist
        .iter()
        .filter(|(id, _)| !changed_set.contains(**id))
        .filter_map(|(id, d)| {
            node_by_id.get(id).map(|n| Impacted {
                id: n.id.clone(),
                name: n.name.clone(),
                file_path: n.file_path.clone(),
                definition_type: n.definition_type.clone(),
                distance: *d,
            })
        })
        .collect();
    // Total order: distance, then name, then id (unique) so output is fully
    // deterministic even when several impacted nodes share name/distance
    // (e.g. multiple unnamed nodes) — required for reproducible MR verdicts.
    blast.sort_by(|a, b| {
        a.distance
            .cmp(&b.distance)
            .then_with(|| a.name.cmp(&b.name))
            .then_with(|| a.id.cmp(&b.id))
    });

    let max_depth = blast.iter().map(|x| x.distance).max().unwrap_or(0);
    Report {
        changed: changed.to_vec(),
        impacted_count: blast.len(),
        max_depth,
        blast_radius: blast,
    }
}

fn main() {
    let args: Vec<String> = env::args().collect();
    let mut graph_path = String::new();
    let mut changed_arg = String::new();
    let mut i = 1;
    while i < args.len() {
        match args[i].as_str() {
            "--graph" => {
                graph_path = args.get(i + 1).cloned().unwrap_or_default();
                i += 2;
            }
            "--changed" => {
                changed_arg = args.get(i + 1).cloned().unwrap_or_default();
                i += 2;
            }
            _ => i += 1,
        }
    }

    if graph_path.is_empty() {
        eprintln!("usage: ripple-engine --graph <graph.json> --changed <id,id,...>");
        std::process::exit(2);
    }

    // Graceful errors (not panics): the graph comes from untrusted Orbit-derived
    // data, so malformed input must produce a clean message + non-zero exit.
    let data = match fs::read_to_string(&graph_path) {
        Ok(d) => d,
        Err(e) => {
            eprintln!("ripple-engine: cannot read {graph_path}: {e}");
            std::process::exit(1);
        }
    };
    let graph: Graph = match serde_json::from_str(&data) {
        Ok(g) => g,
        Err(e) => {
            eprintln!("ripple-engine: invalid graph JSON: {e}");
            std::process::exit(1);
        }
    };
    let changed: Vec<String> = changed_arg
        .split(',')
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
        .collect();

    let report = analyze(&graph, &changed);
    match serde_json::to_string_pretty(&report) {
        Ok(s) => println!("{s}"),
        Err(e) => {
            eprintln!("ripple-engine: failed to serialize report: {e}");
            std::process::exit(1);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn node(id: &str, name: &str, f: &str) -> Node {
        Node { id: id.into(), name: name.into(), file_path: f.into(), definition_type: "Function".into() }
    }
    fn edge(from: &str, to: &str) -> Edge {
        Edge { etype: "CALLS".into(), from: from.into(), to: to.into() }
    }

    // Mirrors the verified ripple-demo-go calc graph:
    //   TotalWithTax -> CalculateTax -> {applyRate, standardRate}; ApplyDiscount isolated.
    fn sample() -> Graph {
        Graph {
            nodes: vec![
                node("A", "applyRate", "calc/tax.go"),
                node("S", "standardRate", "calc/tax.go"),
                node("C", "CalculateTax", "calc/tax.go"),
                node("T", "TotalWithTax", "calc/order.go"),
                node("D", "ApplyDiscount", "calc/tax.go"),
            ],
            edges: vec![edge("T", "C"), edge("C", "A"), edge("C", "S")],
        }
    }

    fn by_name(r: &Report) -> HashMap<String, u32> {
        r.blast_radius.iter().map(|x| (x.name.clone(), x.distance)).collect()
    }

    #[test]
    fn blast_radius_is_transitive() {
        let r = analyze(&sample(), &["A".to_string()]);
        let m = by_name(&r);
        assert_eq!(r.impacted_count, 2);
        assert_eq!(m["CalculateTax"], 1);
        assert_eq!(m["TotalWithTax"], 2);
        assert_eq!(r.max_depth, 2);
    }

    #[test]
    fn uncalled_function_has_empty_blast_radius() {
        let r = analyze(&sample(), &["D".to_string()]);
        assert_eq!(r.impacted_count, 0);
        assert_eq!(r.max_depth, 0);
    }

    #[test]
    fn cycle_terminates_and_is_correct() {
        // a -> b -> c -> a (a calls b, b calls c, c calls a)
        let g = Graph {
            nodes: vec![node("A", "a", "f"), node("B", "b", "f"), node("C", "c", "f")],
            edges: vec![edge("A", "B"), edge("B", "C"), edge("C", "A")],
        };
        let r = analyze(&g, &["A".to_string()]);
        let m = by_name(&r);
        assert_eq!(r.impacted_count, 2);
        assert_eq!(m["c"], 1);
        assert_eq!(m["b"], 2);
    }

    #[test]
    fn self_loop_is_safe() {
        let g = Graph { nodes: vec![node("A", "a", "f")], edges: vec![edge("A", "A")] };
        let r = analyze(&g, &["A".to_string()]);
        assert_eq!(r.impacted_count, 0);
    }

    #[test]
    fn diamond_shared_caller_counted_once() {
        // T->B, T->C, B->X, C->X ; change X
        let g = Graph {
            nodes: vec![node("X", "x", "f"), node("B", "b", "f"), node("C", "c", "f"), node("T", "t", "f")],
            edges: vec![edge("B", "X"), edge("C", "X"), edge("T", "B"), edge("T", "C")],
        };
        let r = analyze(&g, &["X".to_string()]);
        let m = by_name(&r);
        assert_eq!(r.impacted_count, 3);
        assert_eq!(m["b"], 1);
        assert_eq!(m["c"], 1);
        assert_eq!(m["t"], 2);
    }

    #[test]
    fn deep_chain_beyond_three_hops() {
        // L1->L0, L2->L1, ... L5->L4 ; change L0 => impacted at depths 1..=5
        // (proves we exceed Orbit's hard 3-hop DSL cap — the core differentiator).
        let mut nodes = Vec::new();
        let mut edges = Vec::new();
        for i in 0..6 {
            nodes.push(node(&format!("L{i}"), &format!("l{i}"), "f"));
        }
        for i in 0..5 {
            edges.push(edge(&format!("L{}", i + 1), &format!("L{i}")));
        }
        let g = Graph { nodes, edges };
        let r = analyze(&g, &["L0".to_string()]);
        assert_eq!(r.impacted_count, 5);
        assert_eq!(r.max_depth, 5);
    }

    #[test]
    fn multiple_changed_nodes_union_correctly() {
        let r = analyze(&sample(), &["A".to_string(), "S".to_string()]);
        assert_eq!(r.impacted_count, 2); // CalculateTax + TotalWithTax
    }

    #[test]
    fn missing_changed_id_is_ignored() {
        let r = analyze(&sample(), &["NOPE".to_string()]);
        assert_eq!(r.impacted_count, 0);
    }

    #[test]
    fn empty_changed_set_is_empty() {
        let r = analyze(&sample(), &[]);
        assert_eq!(r.impacted_count, 0);
        assert_eq!(r.max_depth, 0);
    }

    #[test]
    fn duplicate_edges_do_not_double_count() {
        let mut g = sample();
        g.edges.push(edge("C", "A"));
        let r = analyze(&g, &["A".to_string()]);
        assert_eq!(r.impacted_count, 2);
    }

    #[test]
    fn output_is_deterministic_and_sorted() {
        let names: Vec<String> = analyze(&sample(), &["A".to_string()])
            .blast_radius
            .iter()
            .map(|x| x.name.clone())
            .collect();
        assert_eq!(names, vec!["CalculateTax".to_string(), "TotalWithTax".to_string()]);
    }
}
