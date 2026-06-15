//! Performance / scaling harness (added by test engineer; not merged unless taken).
//!
//! These are `#[ignore]`d so they don't run in the normal suite. Run explicitly:
//!   cargo test --release --test perf -- --ignored --nocapture
//!
//! They re-implement a tiny in-process copy of the graph types so the harness is
//! self-contained (the binary's `analyze` is not exposed as a library). The graph
//! shapes and the BFS contract are identical to src/main.rs, so timings reflect the
//! real algorithm's asymptotics. We deliberately test:
//!   * a 100k-node / ~100k-edge LINEAR chain (worst case for BFS depth),
//!   * a 100k-node WIDE star + relay (worst case for fan-out),
//!   * a 50k-node dense random DAG (~500k edges) for general throughput,
//!   * a single 200k-node cycle (termination under heavy revisits).

use std::collections::{HashMap, HashSet, VecDeque};
use std::time::Instant;

/// Peak resident set size in KB (Linux VmHWM), or 0 if unavailable.
fn peak_rss_kb() -> u64 {
    let s = std::fs::read_to_string("/proc/self/status").unwrap_or_default();
    for line in s.lines() {
        if let Some(rest) = line.strip_prefix("VmHWM:") {
            return rest.trim().trim_end_matches(" kB").trim().parse().unwrap_or(0);
        }
    }
    0
}

struct Graph {
    /// id -> (name, distinct existence)
    node_ids: Vec<String>,
    /// CALLS edges as (from, to)
    edges: Vec<(usize, usize)>,
}

/// Faithful re-implementation of src/main.rs::analyze over integer ids
/// (string ids in the real engine only add hashing constant factor).
/// Returns (impacted_count, max_depth, elapsed_build, elapsed_bfs).
fn analyze_ints(g: &Graph, changed: &[usize]) -> (usize, u32) {
    // reverse adjacency: callee -> callers
    let mut callers: HashMap<usize, Vec<usize>> = HashMap::new();
    for &(from, to) in &g.edges {
        callers.entry(to).or_default().push(from);
    }
    let mut dist: HashMap<usize, u32> = HashMap::new();
    let mut q: VecDeque<usize> = VecDeque::new();
    for &c in changed {
        if c < g.node_ids.len() && dist.insert(c, 0).is_none() {
            q.push_back(c);
        }
    }
    while let Some(cur) = q.pop_front() {
        let d = dist[&cur];
        if let Some(cs) = callers.get(&cur) {
            for &caller in cs {
                if !dist.contains_key(&caller) {
                    dist.insert(caller, d + 1);
                    q.push_back(caller);
                }
            }
        }
    }
    let changed_set: HashSet<usize> = changed.iter().copied().collect();
    let mut out: Vec<u32> =
        dist.iter().filter(|(id, _)| !changed_set.contains(id)).map(|(_, d)| *d).collect();
    out.sort_unstable();
    let max_depth = out.last().copied().unwrap_or(0);
    (out.len(), max_depth)
}

fn linear_chain(n: usize) -> Graph {
    let node_ids = (0..n).map(|i| format!("L{i}")).collect();
    // L(i+1) calls L(i)
    let edges = (0..n.saturating_sub(1)).map(|i| (i + 1, i)).collect();
    Graph { node_ids, edges }
}

#[test]
#[ignore]
fn perf_linear_chain_100k() {
    let n = 100_000;
    let t0 = Instant::now();
    let g = linear_chain(n);
    let build = t0.elapsed();
    let t1 = Instant::now();
    let (count, depth) = analyze_ints(&g, &[0]);
    let bfs = t1.elapsed();
    println!(
        "[linear 100k] nodes={n} edges={} build={:?} bfs={:?} impacted={count} max_depth={depth}",
        g.edges.len(), build, bfs
    );
    assert_eq!(count, n - 1);
    assert_eq!(depth, (n - 1) as u32);
}

#[test]
#[ignore]
fn perf_wide_star_100k() {
    // One changed leaf X(0); 100k direct callers all at distance 1.
    let n = 100_001;
    let node_ids: Vec<String> = (0..n).map(|i| format!("V{i}")).collect();
    let edges: Vec<(usize, usize)> = (1..n).map(|i| (i, 0)).collect();
    let g = Graph { node_ids, edges };
    let t = Instant::now();
    let (count, depth) = analyze_ints(&g, &[0]);
    println!(
        "[wide-star 100k] nodes={n} edges={} bfs={:?} impacted={count} max_depth={depth}",
        g.edges.len(), t.elapsed()
    );
    assert_eq!(count, n - 1);
    assert_eq!(depth, 1);
}

#[test]
#[ignore]
fn perf_dense_random_dag_50k() {
    // 50k nodes, ~10 outgoing edges each (~500k edges), strictly increasing id to
    // keep it a DAG. Change node 0; measure full traversal + sort cost.
    let n = 50_000usize;
    let fanout = 10usize;
    let node_ids: Vec<String> = (0..n).map(|i| format!("D{i}")).collect();
    let mut edges = Vec::with_capacity(n * fanout);
    // cheap deterministic LCG to pick callees < i (so node i is a caller of some j<i)
    let mut seed: u64 = 0x9e3779b97f4a7c15;
    let mut next = || {
        seed = seed.wrapping_mul(6364136223846793005).wrapping_add(1442695040888963407);
        (seed >> 33) as usize
    };
    for i in 1..n {
        for _ in 0..fanout {
            let j = next() % i; // i calls j (j < i) -> reverse edge makes i a caller of j
            edges.push((i, j));
        }
    }
    let g = Graph { node_ids, edges };
    let t = Instant::now();
    let (count, depth) = analyze_ints(&g, &[0]);
    println!(
        "[dense-dag 50k] nodes={n} edges={} bfs={:?} impacted={count} max_depth={depth}",
        g.edges.len(), t.elapsed()
    );
    // Node 0 is reachable as callee from nearly everything; expect near-total impact.
    assert!(count > n / 2, "expected broad impact, got {count}");
}

#[test]
#[ignore]
fn perf_single_cycle_200k() {
    let k = 200_000usize;
    let node_ids: Vec<String> = (0..k).map(|i| format!("N{i}")).collect();
    let edges: Vec<(usize, usize)> = (0..k).map(|i| (i, (i + 1) % k)).collect();
    let g = Graph { node_ids, edges };
    let t = Instant::now();
    let (count, depth) = analyze_ints(&g, &[0]);
    println!(
        "[cycle 200k] nodes={k} edges={} bfs={:?} impacted={count} max_depth={depth}",
        g.edges.len(), t.elapsed()
    );
    assert_eq!(count, k - 1);
}

#[test]
#[ignore]
fn perf_peak_memory_200k_dense() {
    // Build a 200k-node graph with ~1M edges and report peak RSS of the test process.
    // This is a coarse upper bound (includes test harness overhead) but catches
    // pathological memory blowups.
    let n = 200_000usize;
    let fanout = 5usize;
    let before = peak_rss_kb();
    let node_ids: Vec<String> = (0..n).map(|i| format!("M{i}")).collect();
    let mut edges = Vec::with_capacity(n * fanout);
    let mut seed: u64 = 12345;
    let mut next = || {
        seed = seed.wrapping_mul(6364136223846793005).wrapping_add(1442695040888963407);
        (seed >> 33) as usize
    };
    for i in 1..n {
        for _ in 0..fanout {
            edges.push((i, next() % i));
        }
    }
    let g = Graph { node_ids, edges };
    let t = Instant::now();
    let (count, depth) = analyze_ints(&g, &[0]);
    let after = peak_rss_kb();
    println!(
        "[mem 200k/{}edges] bfs={:?} impacted={count} max_depth={depth} peak_rss={}MB (delta~{}MB)",
        g.edges.len(), t.elapsed(), after / 1024, (after.saturating_sub(before)) / 1024
    );
    assert!(count > 0);
}
