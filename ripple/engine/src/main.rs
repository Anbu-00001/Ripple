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
    from: String,
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
                    dist.insert(caller, d + 1);
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
    // FIX (test engineer): add `id` as a final tiebreaker so ordering is TOTAL and
    // deterministic even when (distance, name) collide (e.g. multiple nameless nodes).
    // Node ids are unique, making this a stable total order independent of HashMap
    // iteration order.
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

    let data = fs::read_to_string(&graph_path)
        .unwrap_or_else(|e| panic!("failed to read {graph_path}: {e}"));
    let graph: Graph = serde_json::from_str(&data).expect("invalid graph JSON");
    let changed: Vec<String> = changed_arg
        .split(',')
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
        .collect();

    let report = analyze(&graph, &changed);
    println!("{}", serde_json::to_string_pretty(&report).unwrap());
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

    // ---------------------------------------------------------------------
    // ADVERSARIAL TESTS (added by test engineer; not merged unless taken).
    // ---------------------------------------------------------------------

    fn raw_node(id: &str, name: &str, f: &str, dt: &str) -> Node {
        Node { id: id.into(), name: name.into(), file_path: f.into(), definition_type: dt.into() }
    }

    /// Build a single linear caller chain of `n` nodes: L(n-1) -> ... -> L1 -> L0.
    /// Changing L0 impacts L1..L(n-1) at depths 1..n-1.
    fn linear_chain(n: usize) -> Graph {
        let mut nodes = Vec::with_capacity(n);
        let mut edges = Vec::with_capacity(n.saturating_sub(1));
        for i in 0..n {
            nodes.push(node(&format!("L{i}"), &format!("l{i}"), "f"));
        }
        for i in 0..n.saturating_sub(1) {
            // L(i+1) calls L(i)
            edges.push(edge(&format!("L{}", i + 1), &format!("L{i}")));
        }
        Graph { nodes, edges }
    }

    // --- Very deep chains: no stack overflow, correct depths -----------------

    #[test]
    fn very_deep_chain_1000_no_overflow() {
        let n = 1000;
        let g = linear_chain(n);
        let r = analyze(&g, &["L0".to_string()]);
        assert_eq!(r.impacted_count, n - 1);
        assert_eq!(r.max_depth, (n - 1) as u32);
    }

    #[test]
    fn very_deep_chain_100000_no_overflow() {
        // 100k-deep linear chain — would blow a recursive DFS stack; BFS must survive.
        let n = 100_000;
        let g = linear_chain(n);
        let r = analyze(&g, &["L0".to_string()]);
        assert_eq!(r.impacted_count, n - 1);
        assert_eq!(r.max_depth, (n - 1) as u32);
    }

    // --- Dense cycles --------------------------------------------------------

    #[test]
    fn large_single_cycle_terminates() {
        // N0 -> N1 -> ... -> N(k-1) -> N0  (Ni calls N(i+1)); change N0.
        let k = 5000;
        let mut nodes = Vec::new();
        let mut edges = Vec::new();
        for i in 0..k {
            nodes.push(node(&format!("N{i}"), &format!("n{i}"), "f"));
        }
        for i in 0..k {
            edges.push(edge(&format!("N{i}"), &format!("N{}", (i + 1) % k)));
        }
        let g = Graph { nodes, edges };
        let r = analyze(&g, &["N0".to_string()]);
        // Every other node reachable as a caller; N0 excluded as changed.
        assert_eq!(r.impacted_count, k - 1);
        // Caller of N0 is N(k-1) at dist 1; predecessor chain wraps backwards.
        assert_eq!(r.max_depth, (k - 1) as u32);
    }

    #[test]
    fn complete_digraph_all_at_distance_one() {
        // Complete directed graph: every node calls every other node.
        // Change one node => every other node is a direct caller (distance 1).
        let k = 200usize;
        let mut nodes = Vec::new();
        let mut edges = Vec::new();
        for i in 0..k {
            nodes.push(node(&format!("V{i}"), &format!("v{i}"), "f"));
        }
        for i in 0..k {
            for j in 0..k {
                if i != j {
                    edges.push(edge(&format!("V{i}"), &format!("V{j}")));
                }
            }
        }
        let g = Graph { nodes, edges };
        let r = analyze(&g, &["V0".to_string()]);
        assert_eq!(r.impacted_count, k - 1);
        assert_eq!(r.max_depth, 1);
        for x in &r.blast_radius {
            assert_eq!(x.distance, 1, "node {} not at distance 1", x.id);
        }
    }

    // --- Disconnected components --------------------------------------------

    #[test]
    fn disconnected_components_isolated() {
        // Component 1: A2 -> A1 -> A0.  Component 2: B1 -> B0 (untouched).
        let g = Graph {
            nodes: vec![
                node("A0", "a0", "f"), node("A1", "a1", "f"), node("A2", "a2", "f"),
                node("B0", "b0", "f"), node("B1", "b1", "f"),
            ],
            edges: vec![edge("A1", "A0"), edge("A2", "A1"), edge("B1", "B0")],
        };
        let r = analyze(&g, &["A0".to_string()]);
        let m = by_name(&r);
        assert_eq!(r.impacted_count, 2);
        assert!(m.contains_key("a1") && m.contains_key("a2"));
        assert!(!m.contains_key("b0") && !m.contains_key("b1"));
    }

    // --- Self-loops ----------------------------------------------------------

    #[test]
    fn self_loop_with_real_callers() {
        // X calls itself AND Y calls X.  Change X => only Y impacted, no inflation.
        let g = Graph {
            nodes: vec![node("X", "x", "f"), node("Y", "y", "f")],
            edges: vec![edge("X", "X"), edge("Y", "X")],
        };
        let r = analyze(&g, &["X".to_string()]);
        let m = by_name(&r);
        assert_eq!(r.impacted_count, 1);
        assert_eq!(m["y"], 1);
    }

    // --- Duplicate / parallel edges -----------------------------------------

    #[test]
    fn massively_parallel_edges_no_double_count() {
        // 10k identical C->A edges must not change the result vs a single edge.
        let mut g = sample();
        for _ in 0..10_000 {
            g.edges.push(edge("C", "A"));
        }
        let r = analyze(&g, &["A".to_string()]);
        assert_eq!(r.impacted_count, 2);
        let m = by_name(&r);
        assert_eq!(m["CalculateTax"], 1);
        assert_eq!(m["TotalWithTax"], 2);
    }

    // --- Edges referencing MISSING nodes ------------------------------------

    #[test]
    fn missing_node_referenced_in_edge_is_dropped_from_output() {
        // GHOST is referenced as a caller of A but absent from nodes[].
        // It is traversed (so it can relay distance) but must not appear in output.
        let mut g = sample();
        g.edges.push(edge("GHOST", "A")); // GHOST calls A
        let r = analyze(&g, &["A".to_string()]);
        let ids: HashSet<String> = r.blast_radius.iter().map(|x| x.id.clone()).collect();
        assert!(!ids.contains("GHOST"), "phantom node leaked into output");
        // impacted_count counts ONLY real nodes here: C, T  (GHOST dropped).
        assert_eq!(r.impacted_count, 2);
    }

    #[test]
    fn missing_intermediate_node_still_relays_distance() {
        // Real chain through a phantom: REAL -> GHOST -> A  (REAL calls GHOST, GHOST calls A).
        // GHOST is missing from nodes[]; REAL should still be reported at distance 2.
        let mut g = sample();
        g.nodes.push(node("REAL", "realCaller", "f"));
        g.edges.push(edge("GHOST", "A"));   // GHOST calls A
        g.edges.push(edge("REAL", "GHOST")); // REAL calls GHOST
        let r = analyze(&g, &["A".to_string()]);
        let m = by_name(&r);
        // REAL reachable at distance 2 even though GHOST is invisible.
        assert_eq!(m.get("realCaller"), Some(&2u32),
            "distance through phantom intermediate not relayed");
    }

    // --- Empty name / file_path (the "ugly verdict row") --------------------

    #[test]
    fn nameless_node_currently_appears_with_empty_fields() {
        // Documents CURRENT behavior: a caller with empty name/file_path produces
        // a row with empty strings rather than being skipped or labeled.
        let g = Graph {
            nodes: vec![
                node("A", "applyRate", "calc/tax.go"),
                raw_node("E", "", "", ""), // nameless caller of A
            ],
            edges: vec![edge("E", "A")],
        };
        let r = analyze(&g, &["A".to_string()]);
        assert_eq!(r.impacted_count, 1);
        let row = &r.blast_radius[0];
        assert_eq!(row.id, "E");
        assert_eq!(row.name, "");        // <-- ugly: empty name shipped to verdict
        assert_eq!(row.file_path, "");   // <-- ugly: empty path shipped to verdict
        assert_eq!(row.distance, 1);
    }

    // --- Duplicate changed sets / changed IDs not in graph -------------------

    #[test]
    fn duplicate_changed_ids_are_idempotent() {
        let r1 = analyze(&sample(), &["A".to_string()]);
        let r2 = analyze(&sample(), &["A".to_string(), "A".to_string(), "A".to_string()]);
        assert_eq!(r1.impacted_count, r2.impacted_count);
        assert_eq!(by_name(&r1), by_name(&r2));
    }

    #[test]
    fn changed_set_with_mix_of_real_and_phantom_ids() {
        // One real ("A"), several non-existent. Phantoms ignored; A drives result.
        let r = analyze(&sample(), &[
            "A".to_string(), "NOPE".to_string(), "".to_string(), "ZZZ".to_string(),
        ]);
        assert_eq!(r.impacted_count, 2);
    }

    #[test]
    fn changed_node_that_is_also_a_caller_is_excluded_from_output() {
        // Change BOTH A and C. C is a caller of A but is itself changed, so it must
        // NOT appear in the blast radius (changed_set filter), only T remains.
        let r = analyze(&sample(), &["A".to_string(), "C".to_string()]);
        let m = by_name(&r);
        assert!(!m.contains_key("CalculateTax"), "changed node leaked into blast radius");
        assert_eq!(m.get("TotalWithTax"), Some(&1u32)); // T now 1 hop from C
        assert_eq!(r.impacted_count, 1);
    }

    // --- Non-CALLS edges must be ignored -------------------------------------

    #[test]
    fn non_calls_edges_are_ignored() {
        let g = Graph {
            nodes: vec![node("A", "a", "f"), node("B", "b", "f")],
            edges: vec![Edge { etype: "IMPORTS".into(), from: "B".into(), to: "A".into() }],
        };
        let r = analyze(&g, &["A".to_string()]);
        assert_eq!(r.impacted_count, 0, "IMPORTS edge wrongly treated as CALLS");
    }

    #[test]
    fn empty_type_edge_treated_as_calls() {
        let g = Graph {
            nodes: vec![node("A", "a", "f"), node("B", "b", "f")],
            edges: vec![Edge { etype: "".into(), from: "B".into(), to: "A".into() }],
        };
        let r = analyze(&g, &["A".to_string()]);
        assert_eq!(r.impacted_count, 1);
    }

    // --- Shortest-path correctness across competing paths --------------------

    #[test]
    fn shortest_path_wins_over_longer_path() {
        // X reachable from caller P via TWO paths: P->X (len1) and P->Q->X (len2).
        // P must be reported at distance 1 (shortest), Q at distance 1 too.
        // P calls X, P calls Q, Q calls X.
        let g = Graph {
            nodes: vec![
                node("X", "x", "f"), node("Q", "q", "f"), node("P", "p", "f"),
            ],
            edges: vec![edge("P", "X"), edge("P", "Q"), edge("Q", "X")],
        };
        let r = analyze(&g, &["X".to_string()]);
        let m = by_name(&r);
        assert_eq!(m["q"], 1);
        assert_eq!(m["p"], 1, "P should take the 1-hop path, not the 2-hop one");
    }

    #[test]
    fn multi_source_takes_min_distance() {
        // Chain D->C->B->A (A changed). Also change C. B should be 1 hop from C, not 2 from A.
        let g = Graph {
            nodes: vec![node("A","a","f"), node("B","b","f"), node("C","c","f"), node("D","d","f")],
            edges: vec![edge("B","A"), edge("C","B"), edge("D","C")],
        };
        let r = analyze(&g, &["A".to_string(), "C".to_string()]);
        let m = by_name(&r);
        // B: callers-of... wait, B calls A => B is a caller of A at dist 1.
        assert_eq!(m["b"], 1);
        // D calls C; C is changed (dist 0) => D at dist 1 (not 3 via A path).
        assert_eq!(m["d"], 1);
    }

    // --- Determinism of output ordering on tie cases -------------------------

    #[test]
    fn determinism_across_many_runs_with_duplicate_names() {
        // Several callers at the SAME distance with the SAME (empty) name. The final
        // sort key is (distance, name) which is NOT total here, so tie order depends
        // on HashMap iteration order. Run repeatedly and check stability.
        let mut nodes = vec![node("A", "applyRate", "f")];
        let mut edges = Vec::new();
        for i in 0..50 {
            // 50 distinct nameless callers of A, all at distance 1, all name "".
            nodes.push(raw_node(&format!("E{i}"), "", "", ""));
            edges.push(edge(&format!("E{i}"), "A"));
        }
        let g = Graph { nodes, edges };
        let first: Vec<String> = analyze(&g, &["A".to_string()])
            .blast_radius.iter().map(|x| x.id.clone()).collect();
        let mut stable = true;
        for _ in 0..20 {
            let again: Vec<String> = analyze(&g, &["A".to_string()])
                .blast_radius.iter().map(|x| x.id.clone()).collect();
            if again != first { stable = false; break; }
        }
        // NOTE: This asserts the GOAL (deterministic). If it fails, ordering on
        // (distance, name) ties is non-deterministic — a real reproducibility bug.
        assert!(stable, "output ordering is NON-DETERMINISTIC on (distance,name) ties");
    }

    #[test]
    fn determinism_distinct_names_is_stable() {
        // Sanity: with distinct names the ordering is fully determined and stable.
        let g = linear_chain(100);
        let first: Vec<String> = analyze(&g, &["L0".to_string()])
            .blast_radius.iter().map(|x| x.name.clone()).collect();
        for _ in 0..10 {
            let again: Vec<String> = analyze(&g, &["L0".to_string()])
                .blast_radius.iter().map(|x| x.name.clone()).collect();
            assert_eq!(again, first);
        }
    }

    // --- max_depth / counting invariants ------------------------------------

    #[test]
    fn impacted_count_matches_blast_len() {
        let r = analyze(&linear_chain(500), &["L0".to_string()]);
        assert_eq!(r.impacted_count, r.blast_radius.len());
    }

    #[test]
    fn empty_graph_no_panic() {
        let g = Graph { nodes: vec![], edges: vec![] };
        let r = analyze(&g, &["anything".to_string()]);
        assert_eq!(r.impacted_count, 0);
        assert_eq!(r.max_depth, 0);
    }

    #[test]
    #[ignore] // run: cargo test --release real_path_perf_string_keyed -- --ignored --nocapture
    fn real_path_perf_string_keyed() {
        use std::time::Instant;
        // Exercises the ACTUAL analyze() (String ids, HashMap<&str>) end to end.
        for &n in &[10_000usize, 50_000, 100_000] {
            let g = linear_chain(n);
            let t = Instant::now();
            let r = analyze(&g, &["L0".to_string()]);
            let el = t.elapsed();
            println!(
                "[real analyze() linear] nodes={n} edges={} elapsed={:?} impacted={} max_depth={}",
                g.edges.len(), el, r.impacted_count, r.max_depth
            );
            assert_eq!(r.impacted_count, n - 1);
        }
        // Wide fan-out through the real path (sort of 100k rows by (distance,name)).
        let n = 100_000usize;
        let mut nodes = vec![node("X", "x", "f")];
        let mut edges = Vec::new();
        for i in 0..n {
            nodes.push(node(&format!("C{i}"), &format!("c{i}"), "f"));
            edges.push(edge(&format!("C{i}"), "X"));
        }
        let g = Graph { nodes, edges };
        let t = Instant::now();
        let r = analyze(&g, &["X".to_string()]);
        println!(
            "[real analyze() wide+sort] callers={n} elapsed={:?} impacted={}",
            t.elapsed(), r.impacted_count
        );
        assert_eq!(r.impacted_count, n);
    }

    #[test]
    fn show_nameless_verdict_rows_and_ordering_spread() {
        // Demonstration test: prints the EXACT pretty JSON a verdict would ship,
        // and counts how many distinct id-orderings appear across 50 runs.
        // Run with: cargo test show_nameless_verdict_rows_and_ordering_spread -- --nocapture
        let g = Graph {
            nodes: vec![
                node("A", "applyRate", "calc/tax.go"),
                raw_node("E1", "", "", ""),
                raw_node("E2", "", "", ""),
                raw_node("E3", "", "", ""),
                raw_node("E4", "", "", ""),
                raw_node("E5", "", "", ""),
            ],
            edges: vec![
                edge("E1", "A"), edge("E2", "A"), edge("E3", "A"),
                edge("E4", "A"), edge("E5", "A"),
            ],
        };
        let r = analyze(&g, &["A".to_string()]);
        println!("--- pretty JSON shipped to verdict (note empty name/file_path rows) ---");
        println!("{}", serde_json::to_string_pretty(&r).unwrap());

        let mut seen: HashSet<String> = HashSet::new();
        for _ in 0..50 {
            let order: Vec<String> = analyze(&g, &["A".to_string()])
                .blast_radius.iter().map(|x| x.id.clone()).collect();
            seen.insert(order.join(","));
        }
        println!("--- distinct id-orderings across 50 runs: {} ---", seen.len());
        for o in &seen {
            println!("    {o}");
        }
    }

    #[test]
    fn distances_are_monotonic_along_chain() {
        // In a linear chain, distance must equal exact hop count for every node.
        let n = 2000;
        let g = linear_chain(n);
        let r = analyze(&g, &["L0".to_string()]);
        let by_id: HashMap<&str, u32> =
            r.blast_radius.iter().map(|x| (x.id.as_str(), x.distance)).collect();
        for i in 1..n {
            assert_eq!(by_id[format!("L{i}").as_str()], i as u32);
        }
    }
}
