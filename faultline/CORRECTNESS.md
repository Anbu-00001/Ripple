# Correctness & Determinism

Faultline is a **merge gate**. A gate that blocks merges must be *trustworthy*: its
answer has to be **complete** (it must not miss impacted code) and **deterministic**
(the same change must always produce the same verdict). This document states the
guarantees and points at the tests that enforce them.

## The reachability invariant (completeness)

A `CALLS` edge `A → B` means "A calls B". If `B` changes, every definition that
*transitively calls* `B` may be affected. So the blast radius of a changed set `C`
is the set of definitions with a directed `CALLS` path **to** some node in `C`
(reverse reachability), and each node's `distance` is the length of the shortest
such path.

**Invariant:** `analyze(graph, C)` returns *exactly* that set, with exact shortest
distances — no node missing, no node spurious.

**How it's enforced:** `analyze_matches_naive_reachability_on_random_graphs`
(`engine/src/main.rs`) generates 400 random graphs (random nodes, ~35%-density
directed edges, cycles allowed, random changed sets) with a seeded PRNG, computes
the expected result with an **independent** naive oracle (per-node forward BFS to
the nearest changed node), and asserts the engine's output matches it set-for-set
and distance-for-distance. This is a machine-checked statement that the engine
computes *the* complete closure — not a plausible-looking subset, which is all a
capped traversal (Orbit's `max_hops ≤ 3`) or an LLM reviewer can offer.

The 11 example-based tests cover the named cases: transitivity, cycles, self-loops,
diamonds (shared caller counted once), depth > 3, multi-root union, duplicate
edges, and empty/missing inputs.

## The minimum-test-set guarantee (provably minimal)

When impacted code is untested, Faultline prescribes the *fewest* definitions to add a
test at so that **every** known path from the change to untested code is intercepted.
This is a minimum s–t **vertex cut**: changed symbols are the source, untested symbols
the sink, already-tested symbols are free interceptors (capacity 0), and every other
impacted symbol costs 1. By **Even's node-splitting** reduction it becomes a max-flow /
min-cut (Menger 1927; Ford–Fulkerson 1956), so the returned set is the smallest
possible — strictly stronger than the greedy set-cover other tools ship.

**Invariant:** `min_test_cut` returns a set that (a) *separates* the change from all
untested impacted code and (b) is of **minimum** cardinality.

**How it's enforced:** `cut_is_minimal_and_valid_vs_bruteforce` (`engine/src/main.rs`)
generates 300 random graphs and checks the returned cut against an **independent
brute-force vertex-cut oracle** — it must both separate (no untested sink remains
reachable once the cut is removed) *and* equal the true minimum size over all subsets.
Construction inputs are sorted, so the chosen minimum cut is canonical (reproducible),
not just *a* minimum cut.

## The risk-attribution guarantee (exact Shapley)

When several symbols change together their blast radii overlap, so a naive per-symbol
untested count double-counts shared downstream code. Faultline attributes the untested
risk with the **Shapley value** over the coverage function `v(S)` = number of untested
definitions reachable from coalition `S`. This is the unique attribution that is
*efficient* (the shares sum to the true untested total), *symmetric*, and respects the
*null player* (a symbol that reaches no untested code gets exactly zero).

**Invariant:** `shapley_risk` returns each changed symbol's exact Shapley value — by
coalition enumeration for up to 20 changed symbols, with integer `n!`-scaled arithmetic
so there is no floating-point drift in the values.

**How it's enforced:** `shapley_matches_permutation_definition_and_is_efficient`
compares the engine's values, over random graphs, to the textbook permutation-average
**definition** (averaged over all orderings), asserting they match symbol-for-symbol
and that the shares sum to the true untested total. Sorted inputs make the ranking
reproducible.

## Determinism guarantees

The verdict is a **pure function** of `(graph, changed set)` — there is no model,
clock, network, or randomness in the compute path. Same inputs → byte-identical
Markdown, every run.

- **Total ordering of output.** Results are sorted by `(distance, name, id)`. `id`
  is unique, so the order is total even when several impacted nodes share a name or
  distance (e.g. multiple unnamed nodes) — see `analyze()` in `engine/src/main.rs`.
- **Cycle-safe traversal.** BFS marks nodes on first visit and never revisits, so
  cyclic and self-referential graphs terminate with correct shortest distances.
- **Saturating distance.** Depth increments use `saturating_add` — no overflow even
  on pathological graphs.
- **Deterministic visualization.** The interactive graph's force-directed layout is
  computed in Go with a fixed seed, a fixed iteration count, and a canonical (sorted)
  node/edge order, so the same change yields a byte-identical HTML page — the diagram
  is reproducible too, not just the verdict text.

## Complexity

`O(V + E)` time and space for the closure: one reverse-adjacency pass over edges,
one BFS visiting each node and edge at most once.

## Honesty boundaries (by design)

These are deliberate, documented limits — stated so that an empty or partial result
reads as *correct*, not broken:

- **Default-branch index.** Orbit indexes the repository's default branch. Faultline
  therefore traces callers of *modified existing* symbols; a symbol that exists only
  on the MR's source branch correctly shows an empty radius (it has no indexed
  callers yet) rather than a false alarm.
- **Coverage is a conservative name-reference heuristic.** "Untested" means an
  impacted symbol's name does not appear (word-boundary match) in any test file —
  not execution coverage. It errs toward flagging. Ingesting `lcov`/`cobertura` is
  the planned next step. The minimum test set is therefore *provably minimal with
  respect to this coverage signal* — the math is exact; its input is a heuristic.
- **Attribution is exact for ≤ 20 changed symbols.** Beyond that the Shapley value is
  estimated by deterministic permutation sampling and the verdict is explicitly marked
  *approximate* (`risk_attribution_exact: false`) — never a falsely-exact number.
- **Code graph only.** Faultline stays on Orbit's verified `CALLS`/`EXTENDS` code
  edges. It does not claim cross-domain graph joins that Orbit's schema does not
  support (e.g. `OWNER` is `User→Group` only; security findings store file location
  as a property, not an edge) — those would be overclaims.

## Why deterministic beats an LLM here

An LLM reviewer can *summarize* likely impact, but it cannot promise the same answer
twice, and it cannot assert completeness. A flaky or incomplete gate is worse than
no gate. Faultline trades open-ended reasoning for a narrow, provable property —
**the complete transitive closure, computed the same way every time** — which is
exactly what a merge-blocking control plane requires.
