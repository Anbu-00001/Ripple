# 🪨 Faultline

**Transitive change-impact governance for GitLab merge requests, built on [GitLab Orbit](https://about.gitlab.com/gitlab-orbit/) (the Knowledge Graph).**

> Orbit can *describe* a change's blast radius. **Faultline makes Orbit *enforce* it** — Code Owners for the blast radius, not the diff.

**75 deterministic tests** · Rust engine: 31 example + **3 property tests proving the closure is complete, the minimum test set is provably minimal, and the Shapley risk split is exact** · Go agent: 41 · **polyglot (Go + Python + Ruby) + CODEOWNERS governance + Duo closed loop + native Code Quality report** · runs as a GitLab CI gate · [why it's correct →](CORRECTNESS.md)

Faultline computes the **full transitive set of callers** ("blast radius") of the symbols changed in a merge request, intersects it with the impacted code that **lacks test coverage**, and **fails the pipeline (blocks the merge)** when an untested blast radius is found. A green-looking one-line helper change that silently reaches deep, untested code becomes a *blocked* MR with an explained verdict — written in plain language ("*Changing `parseConfig` could affect 6 functions; 3 have no tests; add 1 test at `parse_tokens` to cover them all*"), with the graph theory tucked into an expandable section for reviewers who want it.

## The problem (documented GitLab pain)

- **Code review only sees the diff.** A one-line change to a shared helper can break code *three files away* that no reviewer looked at. GitLab's own Orbit cookbook frames the question ("*what breaks if I change this service?*") — but its query DSL stops at 3 hops.
- **Code Owners only gate the diff, not the blast radius.** CODEOWNERS requires approval from owners of *changed* files only; owners of transitively-impacted files are never pulled in — a gap implicated in real approval-bypass issues ([gitlab-org/gitlab#437988](https://gitlab.com/gitlab-org/gitlab/-/issues/437988), [#436977](https://gitlab.com/gitlab-org/gitlab/-/issues/436977)).
- **Coverage gates are blunt.** The Premium *Coverage-Check* rule blocks on a global percentage, which developers "game" with low-value tests — it can't say *which* impacted code is the one that actually needs a test.
- **AI writes code faster than humans can vet it.** Agentic coding raises regression risk exactly where review is thinnest; a fast, deterministic "what did this really touch, and is it tested?" check is the missing guardrail.

## How it's different from existing tools (honest)

Change-impact and test-impact analysis aren't new — Microsoft's Test Impact Analysis, CodeScene, Sourcegraph, and prior hackathon entries all do versions of it. Faultline's defensible edge is the **bundle**: (1) genuine depth **past Orbit's 3-hop cap** (verifiable), (2) outputs that are **deterministic and provable** — minimum test set, exact attribution — with **no model in the decision path**, (3) **empirical proof** on real bugs (below), and (4) **radical honesty** — it refuses the cross-domain graph joins Orbit's schema can't support, and states its own soundness boundary. A tool whose pitch is "the AI figures it out" structurally can't make claims (1)–(4).

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
- **Coverage ranking** — beyond the optimal *set*, a per-node ranking of **where one test buys the most** ("a single test at `parse_tokens` covers 5 of 6 untested"), via dominance over the impact graph (same interception model as the cut). It surfaces the single highest-leverage test — and the single choke point when one exists — so a developer short on time knows exactly where to start.
- **Untested-risk attribution** — an **exact Shapley value** per changed symbol ("which change owns the gap": `parseConfig` owns 66%, `loadEnv` 34%). Overlapping blast radii are split fairly, not double-counted; the shares sum to the true untested total (efficiency axiom), verified against the textbook permutation definition.
- **Code owners beyond the diff** — Faultline reads the project's real **CODEOWNERS** file and maps it onto the *blast radius*, surfacing owners of impacted-but-unchanged files that GitLab's diff-only Code Owners approval would never pull in. *Code Owners for the blast radius, not the diff* — the literal promise, enforced (last-match precedence, sections, and the gitignore glob subset, all tested).
- **Native GitLab surface** — Faultline also emits a **Code Quality report** in the exact format GitLab ingests, so every untested impacted function appears in the merge request's **Reports** tab on the **Free** tier (and inline on the diff on Ultimate), each with a stable per-symbol fingerprint so re-runs don't re-nag. Severity is derived from the algorithm — a *recommended test point* (a member of the provably-minimal set) outranks a node that is merely impacted — never a guessed threshold. It is **advisory** (Code Quality never blocks a merge on its own); the deterministic gate does the blocking.
- **Closed loop with GitLab Duo** — the minimum test set is the exact goal to hand to an agent, so the verdict @-mentions a Duo flow (GitLab's documented trigger) to open a **draft** MR adding that test; a human still approves and the gate never auto-merges. See **[CLOSED_LOOP.md](CLOSED_LOOP.md)**.

The first three are deterministic pure functions of the graph — no model in the compute path; the fourth hands off to a model but only to *draft*, never to decide.

## Would it catch real bugs?

We ran Faultline against real, reproduced regressions from [BugsInPy](https://github.com/soarsmu/BugsInPy) (a benchmark of 501 real Python bugs). Treating each fix as a merge request: **on 21 of 32 analyzable real regressions (across `tornado` + `black`), changing the buggy symbol reaches untested code — the gate would have fired and named the minimal test to add.** For example, a one-character `black` tokenizer fix (`_partially_consume_prefix`) silently reaches **5 untested functions up the parse stack**; Faultline prescribes **one** test (`parse_tokens`) to gate them all.

The batch reuses the *exact* `faultline-engine` binary and the same coverage heuristic as the live gate — only the graph source differs (a conservative static analyzer offline vs. Orbit in production), so the numbers are a **lower bound**. Full methodology, honest caveats, verified call chains, and a reproducible script: **[empirical/RESULTS.md](empirical/RESULTS.md)**.

## Polyglot — Go, Python & Ruby (one verdict, many languages)

Faultline is **language-agnostic by construction**, not by per-language plumbing. The engine closes the graph over **opaque definition IDs** — it never reads a file type — and Orbit emits `CALLS`/`EXTENDS` edges for every language it indexes. The *only* language-aware code in Faultline is which filenames count as tests. So a single merge request that bumps a base rate in a **Go**, a **Python**, and a **Ruby** (GitLab's own Rails stack) service at once produces **one verdict whose blast radius and untested gap span all three languages together**.

This is proven, not asserted:

- **Engine is language-blind** — `closure_is_language_blind_across_go_python_ruby` builds a mixed Go/Python/Ruby graph and asserts the closure is byte-identical when every file extension is swapped (file type cannot change the result).
- **The agent's only language-aware surface is verified end-to-end** — `TestPolyglotEndToEndThroughEngine` runs the real `faultline-engine` binary on a mixed graph, recognizes each language's test convention (`_test.go` / `test_*.py` / `*_spec.rb`), and asserts the verdict names files in all three.
- **Orbit's per-language edges were verified live**, not assumed — a public probe project returned full `CALLS` (Python / Go / Ruby) **and** `EXTENDS` inheritance for all three.
- **No overclaim:** Faultline does *not* invent cross-language call edges (a Go→Ruby HTTP call is not a `CALLS` edge, and we don't pretend it is). The runnable three-language example and reproduction steps: **[demo/polyglot/](demo/polyglot/)**.

## Architecture

| Component | Role | Tests |
|---|---|---|
| **Rust engine** (`engine/`) | Pure, deterministic BFS over reverse-`CALLS`/`EXTENDS` edges → the complete transitive caller set with shortest-caller distances (`O(V+E)`, cycle-safe), **plus the provably-minimal minimum test set (min vertex cut), the per-node coverage ranking (dominance), and exact Shapley untested-risk attribution**. | 34 |
| **Go agent** (`agent/`) | Pulls Definitions + 1-hop `CALLS`/`EXTENDS` edges from Orbit (`POST /api/v4/orbit/query`), normalizes, runs the engine, scans the checked-out repo for tests of impacted symbols, renders the **plain-language** verdict (blast radius, minimum test set, coverage ranking, Shapley attribution, **CODEOWNERS owners beyond the diff**, **Duo closed-loop hand-off**) with the math behind progressive disclosure + a de-cluttered mermaid + a self-contained interactive HTML graph, **emits a native GitLab Code Quality report** for the MR Reports tab, posts the verdict to the MR, and exits non-zero to gate. | 41 |

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
| `FAULTLINE_DUO_FLOW` | unset | Duo flow service-account handle to @-mention for the closed-loop test hand-off (see [CLOSED_LOOP.md](CLOSED_LOOP.md)). Unset → the hand-off renders as guidance, never a fake mention. |
| `FAULTLINE_HUB_FANIN` | `10` | Flag a changed symbol as a high-blast-radius "hub" when it has at least this many direct callers. `0` disables the alert. |

## Run the tests

```console
$ (cd engine && cargo test)   # 34 passed (incl. 3 property tests + language-blind closure) — closure, min-cut, coverage, Shapley
$ (cd agent  && go test ./...) # 41 passed — normalize, render, gate, mermaid, interactive graph, polyglot E2E, CODEOWNERS governance, Duo hand-off, Code Quality report
```

## Honesty boundaries (by design)

- **Orbit indexes the default branch.** Faultline traces callers of *modified existing* symbols; a brand-new symbol that exists only on the branch correctly shows an empty radius (not a false alarm).
- **Coverage is a conservative name-reference heuristic** (word-boundary match in test files), not execution coverage. It errs toward flagging. Ingesting `lcov`/`cobertura` is the planned next step.
- **Cross-domain (the honest version):** Orbit's `OWNER` edge is `User→Group` only and security findings store file location as a property, not an edge — so Faultline does **not** fake a security→code or owner→code *graph join*. For ownership it instead reads the project's real **CODEOWNERS** file (a separate, first-class GitLab mechanism) and maps it onto the blast radius — a clearly-labeled property-level join, not an invented Orbit edge. Security/deploy joins stay out of scope rather than overclaimed.

## License

[MIT](LICENSE).
