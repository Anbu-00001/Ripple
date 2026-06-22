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
untested impacted code and (b) is of **minimum** cardinality — minimum over the graph
supplied to the engine (the closure built from Orbit's indexed `CALLS`/`EXTENDS`
edges). Minimality is relative to that graph and to the coverage signal in use: if
Orbit has not indexed a call, no downstream tool can cut an edge it cannot see. The
math is exact; its inputs are the indexed facts.

**How it's enforced:** `cut_is_minimal_and_valid_vs_bruteforce` (`engine/src/main.rs`)
generates 300 random graphs and checks the returned cut against an **independent
brute-force vertex-cut oracle** — it must both separate (no untested sink remains
reachable once the cut is removed) *and* equal the true minimum size over all subsets.
Construction inputs are sorted, so the chosen minimum cut is canonical (reproducible),
not just *a* minimum cut.

## The coverage-ranking guarantee (dominance)

The minimum cut gives the optimal *set*; the coverage ranking answers the everyday
question "**if I write only one test, where?**". A test at node `X` gates every
untested node that `X` **dominates** — a node that becomes unreachable from the
changed set once `X` is removed lies on *every* impact path from the change to it,
so testing `X` intercepts them all. This is the *same* interception model as the
minimum cut (Menger), exposed per node and ranked by how many untested definitions
each single test would gate. When one node dominates *all* untested code, that is the
single choke point (and necessarily the size-1 minimum cut).

**Invariant:** `coverage_ranking` reports, for each currently-untested impacted node,
the exact count of untested nodes a single test there would gate — computed by
removal-reachability (dominance), deterministic and sorted by (covers, name, id).

**How it's enforced:** `coverage_ranking_finds_single_choke_point`,
`coverage_ranking_diamond_has_no_false_choke`, and
`coverage_ranking_skips_tested_and_empty_when_all_tested` (`engine/src/main.rs`)
check the choke-point, the two-independent-paths case (no false single test), and that
already-tested nodes are excluded as candidates.

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

## The language-agnostic guarantee (Go, Python, Ruby, …)

Faultline closes the graph over **opaque definition IDs**. The engine never reads a
node's `file_path` or `definition_type` when computing the closure, the minimum cut,
or the Shapley split — those fields are carried through for display only. So the
verdict is identical no matter which language the symbols come from, and a single
merge request that spans several languages yields one closure over all of them.

**Invariant:** `analyze`/`min_test_cut`/`shapley_risk` depend only on the *(id,
edge)* topology, never on file type.

**How it's enforced:** `closure_is_language_blind_across_go_python_ruby`
(`engine/src/main.rs`) builds a mixed Go/Python/Ruby graph and asserts the impacted
*(id, distance)* set is byte-identical when every file extension is swapped to a
single language. On the agent side — where the *one* language-aware surface lives,
test-file detection — `TestPolyglotEndToEndThroughEngine` (`agent/polyglot_test.go`)
runs the real engine on a mixed graph and asserts each language's test convention
(`_test.go` / `test_*.py` / `*_spec.rb`) is recognized and the verdict spans all
three. Orbit's emission of `CALLS`/`EXTENDS` for each language was verified live, not
assumed (see `demo/polyglot/`). Faultline does **not** synthesize cross-language call
edges — calls that cross a language boundary (e.g. an HTTP RPC) are not `CALLS` edges
and are out of scope, by design.

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
- **Deterministic Code Quality report.** The native GitLab Code Quality artifact is a
  faithful pass-through of the engine's findings: one entry per untested impacted
  function, sorted by `(path, begin line, fingerprint)`, with a **stable per-symbol
  fingerprint** (hash of the check name + Orbit Definition id) so an unchanged gap is
  the *same* finding across runs rather than a new nag. Severity is derived from the
  algorithm — a recommended test point (a member of the provably-minimal set) outranks
  a merely-impacted node, and the loud severities are reserved for when gating is on —
  never a guessed threshold. The schema (single JSON array; `description`, `check_name`,
  `fingerprint`, `severity`, `location.path` relative + `location.lines.begin`) is the
  format GitLab natively ingests, and it is **advisory**: Code Quality never blocks a
  merge on its own — only Faultline's separate deterministic gate does. Line numbers
  come from a best-effort Orbit `start_line` lookup; when absent, findings degrade to
  line 1 (file-level) rather than inventing a position.

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
- **Fail closed on an incomplete index.** An empty blast radius is only trustworthy if
  the graph it was computed over is actually there. Before reporting a clean result,
  Faultline reads Orbit's free (no-quota) `GET /orbit/status` and
  `GET /orbit/graph_status?project_id=…` and checks three principled, threshold-free
  signals: the cluster is reachable, every known project is indexed
  (`projects.indexed == total_known`), and the code graph holds definitions
  (`source_code.Definition > 0`). If the index is absent, partial, or still indexing,
  a clean result is **not** shown as a green ✅ — it degrades to a distinct
  *🟡 "can't vouch"* verdict that **fails the gate closed** when gating is on (advisory
  otherwise, so first adoption on an unindexed repo never blocks), with the
  `faultline-override` label as the audited escape. A shape Orbit does not return
  (Beta: "ontology may change") degrades to *unknown* — noted, never a false block.
  This is the same instinct as Orbit's own code indexer, which refuses stale cleanup on
  a degraded re-index rather than silently tombstoning good data.
- **Determinism is relative to the indexed snapshot.** Same MR + same Orbit graph ⇒
  a byte-identical verdict — that is the guarantee. Because Orbit re-indexes the
  default branch over time, the verdict can legitimately change between runs when the
  *graph* changes (a newly-merged caller appears, a symbol is renamed): that is a
  correct reflection of new facts, not nondeterminism. Faultline analyzes the graph
  as indexed at run time; it never silently serves a stale snapshot.
- **Coverage: real execution data when provided, else a conservative name heuristic.**
  When a `cobertura`/`lcov` report is supplied (`FAULTLINE_COVERAGE`), "untested" means
  an impacted symbol's executed line range has no covered lines — real execution
  coverage, mapped onto the symbol via Orbit's best-effort `start_line`/`end_line`. With
  no report it falls back to a name-reference heuristic: the symbol's name does not
  appear (word-boundary match) in any test file. Both err toward flagging. The minimum
  test set is therefore *provably minimal with respect to the coverage signal in use* —
  the math is exact; the fallback's input is a heuristic.
- **Attribution is exact for ≤ 20 changed symbols.** Beyond that the Shapley value is
  estimated by deterministic permutation sampling and the verdict is explicitly marked
  *approximate* (`risk_attribution_exact: false`) — never a falsely-exact number.
- **Code graph only (on Orbit).** Faultline's *graph* computation stays on Orbit's
  verified `CALLS`/`EXTENDS` edges. It does not fake cross-domain graph joins Orbit's
  schema can't support — `OWNER` is `User→Group` only, and security findings store
  file location as a property, not an edge.
- **Ownership is a CODEOWNERS file join, not an Orbit edge.** Because Orbit has no
  code-ownership edge, the "Code owners beyond the diff" section reads the project's
  real **CODEOWNERS** file — GitLab's own mechanism — and maps owners onto the blast
  radius (last-match precedence, sections, and the common gitignore glob subset,
  tested in `agent/codeowners_test.go`). This is a labeled property-level join over a
  first-class GitLab artifact, deliberately *not* presented as an Orbit graph edge.
- **The Duo closed loop drafts, it does not decide.** When the gate finds an untested
  blast radius it can hand the minimum test set to a Duo flow (GitLab's documented
  mention trigger). The flow opens a **draft** MR a human must approve; no model is in
  the verdict's compute path and the gate never auto-merges (see `CLOSED_LOOP.md`).

## Why the decision path is deterministic (and where AI still helps)

An LLM reviewer can *summarize* likely impact, and that is genuinely useful — but it
cannot promise the same answer twice, and it cannot assert completeness. GitLab makes
the same point about graph traversal versus inference: lean on a model to walk the
structure and "error compounds with every hop." A merge-blocking gate is the wrong
place to inherit that drift; a flaky or incomplete gate is worse than no gate. So
Faultline keeps the *decision* deterministic — a narrow, provable property,
**the complete transitive closure, computed the same way every time** — that a human
can re-run and audit. AI is not banished; it is kept out of the verdict. Once the gate
decides, a GitLab Duo flow drafts the test MR for a human to approve: AI where it
accelerates, not where it adjudicates.
