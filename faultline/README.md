# 🪨 Faultline

**Transitive change-impact governance for GitLab merge requests, built on [GitLab Orbit](https://about.gitlab.com/gitlab-orbit/) (the Knowledge Graph).**

> Orbit can *describe* a change's blast radius. **Faultline makes Orbit *enforce* it** — Code Owners for the blast radius, not the diff.

**53 deterministic tests** · Rust engine: 27 example + **3 property tests proving the closure is complete, the minimum test set is provably minimal, and the Shapley risk split is exact** · Go agent: 23 · runs as a GitLab CI gate · [why it's correct →](CORRECTNESS.md)

Faultline computes the **full transitive set of callers** ("blast radius") of the symbols changed in a merge request, intersects it with the impacted code that **lacks test coverage**, and **fails the pipeline (blocks the merge)** when an untested blast radius is found. A green-looking one-line helper change that silently reaches deep, untested code becomes a *blocked* MR with an explained verdict.

---

## Why this is a *new* capability for Orbit

Orbit's graph has a `CALLS` edge between code definitions, and its query DSL can traverse it — but **only up to `max_hops: 3`**, and it has **no transitive-closure / variable-depth reverse-reachability operator** (its only depth operator, `path_finding`, returns a single point-to-point shortest path). You can verify the cap against the live API:

```console
$ curl -s -X POST "$GITLAB/api/v4/orbit/query" -H "Authorization: Bearer $TOKEN" \
    -d '{"query":{"query_type":"traversal","nodes":[...],
         "relationships":[{"type":"CALLS","from":"a","to":"b","max_hops":4}]}}'
{"code":"compile_error","message":"schema violation: 4 is greater than the maximum of 3 at /relationships/0/max_hops"}
# max_hops: 3 → HTTP 200.  max_hops: 4 → rejected.
```

So Orbit can describe *a* path; it **cannot hand you the complete transitive caller set at arbitrary depth**. That set is exactly what governance needs ("what *else* breaks if I change this?"). Faultline adds it: a deterministic engine that closes the graph Orbit exposes.

It closes over **`EXTENDS` edges too** (inheritance / interface implementation / struct embedding), so changing a base type ripples to its *entire* subtype chain — verified live: changing `BaseRate` reaches `Tier5` **5 levels deep**, again past the 3-hop cap. The same governance primitive, now for type hierarchies, not just calls.

## What it does — live

On [a real MR that raises a tax rate by one line](https://gitlab.com/anbuchelvanganesan.cse2024-group/faultline-demo/-/merge_requests/1), Faultline posts this verdict and **fails the pipeline**:

> ⚠️ **6 definition(s) transitively affected** — max depth **5**, beyond Orbit's 3-hop query cap.
>
> **🔭 Orbit 3-hop query vs Faultline closure** — a native reverse-`CALLS` query reaches at most **4 of 6** impacted definitions; the other **2** (`netLevy` @4 hops, `InvoiceTotal` @5 hops) are invisible to *any* single Orbit query. Faultline computes the full closure.
>
> 🚦 **Untested blast radius — 5 impacted definitions with no test coverage** → **GATE FAILED, merge blocked.**

It also renders a **mermaid** diagram of the blast subgraph (changed = blue, untested = red) inline in the note, plus a **self-contained interactive graph** (HTML, zero dependencies — no D3/CDN) delivered as a CI artifact link in the MR: zoom, pan, drag nodes, and hover any definition for its file and hop-distance. The force-directed layout is computed *deterministically in Go*, so the same change yields a byte-identical page — it opens in any browser offline, or renders inline if GitLab Pages is enabled.

Beyond flagging the gap, Faultline **prescribes the fix**:

- **Minimum test set** — a *provably-minimal* vertex cut (Even node-splitting → max-flow / min-cut, Menger) giving the **fewest** definitions to add a test at to gate the *entire* change. One well-placed test often intercepts many untested paths, so the verdict says "test these **K**, not all **N**" — beating a greedy set-cover, machine-checked against a brute-force oracle.
- **Untested-risk attribution** — an **exact Shapley value** per changed symbol ("which change owns the gap": `parseConfig` owns 66%, `loadEnv` 34%). Overlapping blast radii are split fairly, not double-counted; the shares sum to the true untested total (efficiency axiom), verified against the textbook permutation definition.

Both are deterministic pure functions of the graph — no model in the compute path.

## Architecture

| Component | Role | Tests |
|---|---|---|
| **Rust engine** (`engine/`) | Pure, deterministic BFS over reverse-`CALLS`/`EXTENDS` edges → the complete transitive caller set with shortest-caller distances (`O(V+E)`, cycle-safe), **plus the provably-minimal minimum test set (min vertex cut) and exact Shapley untested-risk attribution**. | 30 |
| **Go agent** (`agent/`) | Pulls Definitions + 1-hop `CALLS`/`EXTENDS` edges from Orbit (`POST /api/v4/orbit/query`), normalizes, runs the engine, scans the checked-out repo for tests of impacted symbols, renders the Markdown verdict (blast radius, minimum test set, Shapley attribution) + mermaid + a self-contained interactive HTML graph, posts it to the MR, and exits non-zero to gate. | 23 |

Runs as a GitLab CI job on `merge_request_event`. A companion **declarative GitLab Duo agent** (`agents/faultline-impact-reviewer.yml`) is published to the **AI Catalog** as the always-on, in-platform front door (see `CATALOG.md`).

## Why deterministic, not an LLM

A gate that blocks merges must be **reproducible**, or it is unusable. Faultline's verdict is a *pure function* of `(graph, changed set)` — same inputs, byte-identical Markdown, every run. The engine returns **the** transitive caller set (provably complete relative to the indexed graph), not a plausible-looking subset. There is **no model in the compute path**.

## Install (one CI job)

1. Add a `FAULTLINE_TOKEN` CI/CD variable (an access token with `api` scope), masked.
2. Add to your project's `.gitlab-ci.yml`:

```yaml
include:
  - remote: 'https://gitlab.com/anbuchelvanganesan.cse2024-group/faultline/-/raw/main/ci/faultline-gate.yml'
```

The job pulls the call graph from Orbit for the MR's changed files, computes the blast radius, and posts the verdict. **Gating is opt-in**: by default the job is comment-only (it never blocks a merge on first adoption). Set a `FAULTLINE_GATE` CI/CD variable to a number `N` to fail the pipeline when more than `N` untested-impacted symbols are found (`FAULTLINE_GATE=0` is zero-tolerance).

### Configuration (all optional — nothing repo-specific is baked in)

| Variable | Default | Effect |
|---|---|---|
| `FAULTLINE_GATE` | unset → comment-only | Fail the pipeline above `N` untested-impacted symbols. |
| `FAULTLINE_QUERY_LIMIT` | `1000` | Rows per Orbit query; if a query hits the cap the verdict warns it may be partial (never silent). |
| `FAULTLINE_TEST_PATTERNS` | unset | Comma-separated extra test-file suffixes/path fragments (e.g. `.bats,/it/`) for non-standard conventions. |
| `FAULTLINE_HTTP_TIMEOUT_SEC` | `30` | Per-request timeout for Orbit/GitLab calls. |

## Run the tests

```console
$ (cd engine && cargo test)   # 30 passed (incl. 3 property tests) — closure, min-cut, Shapley
$ (cd agent  && go test ./...) # 23 passed — normalize, render, gate, mermaid, interactive graph, query contract, config
```

## Honesty boundaries (by design)

- **Orbit indexes the default branch.** Faultline traces callers of *modified existing* symbols; a brand-new symbol that exists only on the branch correctly shows an empty radius (not a false alarm).
- **Coverage is a conservative name-reference heuristic** (word-boundary match in test files), not execution coverage. It errs toward flagging. Ingesting `lcov`/`cobertura` is the planned next step.
- **Cross-domain:** Orbit's `OWNER` edge is `User→Group` only (no code-ownership edge), and security findings carry file location as a property, not an edge — so Faultline deliberately stays on the verified `CALLS`/`EXTENDS` code graph rather than overclaiming cross-domain joins.

## License

[MIT](LICENSE).
