# Faultline — Devpost writeup

![Faultline — Code Owners for the blast radius, not the diff. A deterministic merge gate on GitLab Orbit: one line changes, Orbit's graph shows everything it reaches through a chain of calls, the untested impact blocks the merge, and Faultline names the single smallest test that closes the gap.](docs/branding/faultline-hero.png)

> **GitLab built Orbit to answer one question — *"What breaks if I change this service?"* — from *indexed facts, not inference*. Faultline takes that answer the last mile: it doesn't just describe a change's blast radius, it enforces it. Code Owners for the blast radius, not the diff.**

A deterministic GitLab CI merge gate, built on **GitLab Orbit** (the code Knowledge Graph), that computes the *full transitive* set of callers of the symbols a merge request changes, finds the impacted code with **no test coverage**, and **blocks the merge** — with a plain-language verdict and the single smallest test to add.

- **Try it live (three real merge requests):**
  - [a one-line tax-rate change that fails the pipeline](https://gitlab.com/anbuchelvanganesan.cse2024-group/faultline-demo/-/merge_requests/1) — untested impact 5 calls deep, the one test to add
  - [one verdict across Go + Python + Ruby](https://gitlab.com/anbuchelvanganesan.cse2024-group/faultline-polyglot/-/merge_requests/1) — a shared rate bumped in three languages, one blast radius
  - [Faultline gating its **own** repo](https://gitlab.com/anbuchelvanganesan.cse2024-group/faultline/-/merge_requests/1) — dogfooded with the same `include:` we ship to users
- **Code (MIT):** the repository this writeup ships in · **110 deterministic tests** · the gate fired on **21 of 32** real BugsInPy regressions
- **Video:** [▶ Watch the 3-min demo](https://youtu.be/zG4Gs0Y3a0Y)

---

## The problem (the three questions the rules ask)

**What pain point does it address?** Code review only sees the **diff**. A one-line change to a shared helper can silently break code three files away that no reviewer opened. GitLab's own tooling has the same shape of gap: **Code Owners only gate the changed files**, not the transitively-impacted ones ([gitlab-org/gitlab#437988](https://gitlab.com/gitlab-org/gitlab/-/issues/437988)), and the Premium **Coverage-Check** rule blocks on a *global* percentage that developers game with low-value tests — it can't say *which* impacted function is the one that actually needs a test. AI coding agents make this worse: they generate code faster than humans can vet its ripple effects.

**How does it fix it?** Faultline asks Orbit for the call graph, then computes the **complete reverse-transitive closure** of the changed symbols — every function that could be affected, at any depth — intersects it with the code that has **no test**, and fails the pipeline. It doesn't just flag the gap; it **prescribes the smallest fix**: a provably-minimal set of test points, ranked by leverage, attributed fairly across the changed symbols.

**What changes for the developer?** Instead of a green MR that hides a deep, untested ripple, they get a blocked MR that says, in plain words: *"Changing `_partially_consume_prefix` could affect 6 functions up to 5 calls away — deeper than any single Orbit query returns. 5 have no tests. Fastest fix: add 1 test at `parse_tokens` to cover them all."* The graph theory is tucked into an expandable section; the owners of impacted-but-unchanged files are pulled into the conversation.

---

## Why it's a *new* capability for Orbit (the innovation)

The *new capability* isn't "see the blast radius." GitLab's own Orbit cookbook ships a blast-radius recipe, and impact *visualizers* are exactly the kind of project this community already celebrates — so seeing it is table stakes. Faultline's leap is from **viewer to enforcer**: it turns Orbit's graph into a **prescription** and a **gate** — a deterministic, **provably-minimal set of tests** (a min vertex cut, minimal with respect to the call graph Orbit returns and the coverage signal in use) that closes the *untested* impact, plus **exact per-symbol risk attribution** (Shapley) — computed, not guessed. No shipping tool does that on Orbit. Reaching it needs the one thing a single interactive Orbit query won't hand you — the *complete* transitive closure — so Faultline composes it.

Orbit's query DSL traverses the `CALLS` edge with a **depth bound** (`max_hops: 3`, to keep interactive queries fast) and **no unbounded transitive-closure operator**. That bound is right for a live query; a merge gate needs the complete set. You can confirm the bound against the live API:

```
max_hops: 3 → HTTP 200.  max_hops: 4 → {"code":"compile_error",
  "message":"schema violation: 4 is greater than the maximum of 3 ..."}
```

So one Orbit query gives bounded reach; the **complete transitive caller set at arbitrary depth** has to be *composed* — which is exactly what a merge gate needs. Faultline does that **offline, in CI**: a deterministic engine that closes the one-hop graph Orbit exposes, over `CALLS` **and** `EXTENDS` (so a base-type change ripples through its whole subtype chain) — completing Orbit's "full blast radius" promise for the gate, where latency isn't the constraint.

---

## How it compares to other Orbit blast-radius agents

Blast-radius-on-Orbit is the most crowded lane in this hackathon — and the strongest entries are deterministic, open-source, and genuinely deep (one traverses transitive callers *across repositories* and even fuses the SDLC graph for production reachability). So neither "deterministic" nor "sees the blast radius" is the differentiator. Three things are.

**It gates the part that matters — the *untested* impact.** Every other entry we've found stops at a risk score on a comment ("N callers — HIGH"); none of them look at your tests. Faultline intersects the blast radius with the code that has **no coverage**, **blocks the merge** on it, and — uniquely — **prescribes the single smallest fix**: a **provably-minimal** test set (a min vertex cut, machine-checked against a brute-force oracle) plus **exact Shapley** attribution per changed symbol. Not "here's the blast radius," but "here are the *K* tests that close the *untested* part of it."

**It's polyglot.** The others are effectively Python-only; Faultline's engine is language-blind, proven on **Go + Python + Ruby** with real Cobertura/lcov coverage — one verdict across the stack.

**It runs where a gate can be trusted.** Faultline runs in **CI on Orbit's free graph API**, with **no model in the decision path**, and **fails closed** when the index is stale or partial. The live-agent reviewers reach Orbit through GitLab Duo credits and the hosted MCP *at review time*, and inherit an LLM-orchestrated run. A comment can afford that; a gate that blocks your merge cannot.

---

## How it works (the technical implementation)

A two-language core, deterministic end to end — **no model in the decision path**:

- **Rust engine** — pure functions over the graph: the complete transitive caller set (BFS, `O(V+E)`, cycle-safe); a **provably-minimal minimum test set** (Even node-splitting → max-flow/min-cut, Menger), so it says "test these *K*, not all *N*"; a **dominance-based coverage ranking** ("one test here covers 5 of 6"); and **exact Shapley** attribution of the untested risk across the changed symbols. Each guarantee is **machine-checked against an independent brute-force/permutation oracle** on hundreds of random graphs.
- **Go agent** — pulls Definitions + one-hop `CALLS`/`EXTENDS` from Orbit, normalizes, runs the engine, determines coverage (**real Cobertura/lcov execution data** when provided, a name-reference heuristic otherwise), and renders the plain-language verdict + a colorblind-safe graph. It also emits a **native GitLab Code Quality report** (every untested impacted function shows in the MR Reports tab on the Free tier) and hands the minimum test set to a **GitLab Duo** flow to open a *draft* test MR — a human still approves.

**Adoption is one line** of `.gitlab-ci.yml` (a remote `include:`), with a companion declarative agent published to the **AI Catalog**. Gating is opt-in; draft MRs are advisory; a `faultline-override` label is an *audited* bypass.

### On the Duo Agent Platform — Tools · Triggers · Context

- **Context — GitLab Orbit, the Knowledge Graph.** Faultline's grounding context is Orbit's indexed `CALLS`/`EXTENDS` edges, definitions, and line ranges — *indexed facts, not inference*. It uses only what the schema supports and refuses the joins it doesn't.
- **Triggers — the merge request.** The gate runs on the `merge_request_event` pipeline; the published **AI Catalog** agent (*Faultline Impact Reviewer*) reacts in review; the closed loop fires on the verdict (and can run as a `pipeline: failed` Duo flow) to hand off the fix.
- **Tools / actions.** Faultline queries Orbit's graph, posts the MR verdict note, emits a native **Code Quality** report, and hands the minimum test set to a **Duo flow** that opens a *draft* test MR (a human approves). The verdict itself is a deterministic engine — *no model in the decision path* — so the platform's context and tools feed a **provable** gate, not a guess.

---

## Does it catch real bugs? (the evidence)

We ran the **exact engine binary** against real, reproduced regressions from **BugsInPy** (501 real Python bugs). Treating each fix as a merge request: **on 21 of 32 analyzable regressions across `tornado` + `black`, changing the buggy symbol reaches untested code — the gate would have fired and named the minimal test.** Because the offline graph comes from a conservative static analyzer (vs Orbit in production), the numbers are a **lower bound**. Full methodology and verified call chains: `empirical/RESULTS.md`.

---

## What we're proud of (the design + the honesty)

The verdict **leads in plain language** — "what this change could affect", "functions with no test", "fastest fix" — with three fixed status badges and the math behind progressive disclosure. The graph uses a **colorblind-safe palette with shape redundancy**, and ships as a zero-dependency interactive HTML artifact whose layout is computed deterministically (byte-identical every run).

And we were **ruthlessly honest with ourselves**: a planned "cost-aware weighted cut" feature was dropped after we *proved* (33,652 random trials) it would be a mathematical no-op in our formulation — shipping it would have been complexity for show. Faultline also **refuses the cross-domain graph joins Orbit's schema can't reliably support** — production reachability, ownership, vulnerability fusion — not because they're uninteresting, but because **a gate that blocks a merge has to be sound**: an advisory comment can speculate across domains and be occasionally wrong; an enforcer cannot. So it gates only on the call graph Orbit makes trustworthy, and states its soundness boundary out loud. None of this is anti-AI — it's about where AI belongs. Put a model in the *decision* path and you inherit a verdict you can't reproduce or audit; keep it out, and the gate can promise the same answer twice. So the decision stays deterministic, and a GitLab Duo flow does what AI is genuinely good at: drafting the test MR for a human to approve.

---

## Built with

GitLab Orbit (Knowledge Graph API) · Rust · Go · GitLab CI · GitLab Code Quality reports · GitLab Duo (AI Catalog) · Cobertura/lcov.

## Try it

1. Open the [live demo MR](https://gitlab.com/anbuchelvanganesan.cse2024-group/faultline-demo/-/merge_requests/1) and read the verdict + red pipeline.
2. Reproduce the BugsInPy numbers offline with `empirical/faultline_batch.py` (no GitLab needed).
3. Add one `include:` to your `.gitlab-ci.yml` and set a `FAULTLINE_TOKEN` — see the README.
