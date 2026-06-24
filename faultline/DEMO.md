# Faultline — demo scenario & ≤3-minute video storyboard

This is a **read-through, not a guess**: a shot-by-shot storyboard with timings, what's
on screen, and the exact voiceover for each beat. It is optimized for the four equal
judging criteria (Technological Implementation · Design & Usability · Potential Impact ·
Quality of Idea) and for what these GitLab judges have rewarded before: *"agents that
act, not chatbots"*, *"feels like a product"*, heavy testing, quantified impact — and the
change-impact-on-the-graph project (**GraphDev**) that won the previous GitLab AI Hackathon's
Grand Prize for *showing* "what gets impacted by changes", which Faultline turns from a
**viewer** into an **enforcer**.

**Hard rules (from the host + demo-craft research):** ≤ 3:00 (judges may stop at 3:00,
so **front-load** everything); upload to YouTube/Vimeo **public**, marked *Not for Kids*;
no copyrighted music; **state the hackathon name in the first seconds**; screencast +
voiceover (no talking-head, no slides); the voiceover adds information the screen doesn't
(never read the screen aloud); jargon (`vertex cut`, `Shapley`) stays **on-screen only**,
never spoken. Speak slightly fast and edit out dead air.

Every on-screen claim below is **live and re-verifiable** by a judge — no mocks.

---

## The storyboard (target 2:50, hard cap 3:00)

| # | Time | On screen | Voiceover (one breath each) | Criteria |
|---|------|-----------|------------------------------|----------|
| **0 · Cold open — the result first** | 0:00–0:12 | A real GitLab MR, pipeline **RED**, zoom on the verdict: **⛔ Faultline · Blocked**. Lower-third caption: *"GitLab Transcend Hackathon · Faultline · built on GitLab Orbit."* | "This one-line change just got **blocked from merging** — because it breaks code that nobody tested. Here's the agent that caught it." | Impact · Design |
| **1 · The stakes** | 0:12–0:30 | Split screen: the tiny diff (one constant in `calc/tax.go`) ↔ a fan-out of impacted functions; badge **"7 impacted · 5 untested."** | "A one-line helper change can ripple across the whole call graph. Review only shows the diff — the blast radius is invisible. Faultline computes it from GitLab Orbit, your code's knowledge graph." | Impact · Idea |
| **2 · It ACTS — and hands you the fix** | 0:30–0:52 | The pipeline job log: Faultline reads the MR, queries Orbit, posts the verdict; zoom the plain-language lines, then the **prescription** (one test at `Rate`). Optionally flash the **Duo draft test-MR** it opens. | "It's not a chatbot you ask — it runs as a CI gate and acts on the merge. And it doesn't just say no: it names the **one** test that closes all five untested paths, and hands a **draft test-MR to GitLab Duo** for a human to approve. AI proposes; a person disposes." | Tech · Impact · Design |
| **3 · Deterministic** | 0:52–1:10 | Same MR re-run twice, side by side → **byte-identical** verdict. Caption: *"No model in the decision path — same change, same verdict."* | "The decision is deterministic. No model in the loop — so no bias, no flake. The same change always produces the same verdict, and a human can re-run and audit it." | Tech · Idea |
| **4 · Polyglot, one verdict** | 1:10–1:33 | The polyglot MR: one change in **Go + Python + Ruby**; a single verdict; language badges light up. | "One change touching Go, Python, and Ruby — and **one** blast radius across all three, because the graph is language-blind." | Tech · Idea |
| **5 · THE WOW — deeper than one Orbit query** | 1:33–2:00 | Live terminal: `max_hops: 4` → **`compile_error`** from Orbit. Then the verdict showing `InvoiceTotal` at **5 hops**. | "Orbit caps a single query at three hops, for speed — push it to four and it refuses. So Faultline pulls the edges and closes the whole graph offline, reaching code five calls deep that no single Orbit query can — and blocks on the part that's untested." | Idea · Tech |
| **6 · Honest by construction (fail-closed)** | 2:00–2:18 | Run against a project Orbit hasn't finished indexing → **🟡 Faultline · Can't vouch — index incomplete**. | "And when Orbit's index is incomplete, it **won't** give you a false green. It fails closed and says it can't be sure — the same instinct GitLab's own indexer uses." | Idea · Tech |
| **7 · Dogfood + one-line adoption** | 2:18–2:40 | Faultline gating **its own repo** (advisory ⚠️). Then the install: one `include:` in `.gitlab-ci.yml`; the **AI Catalog** listing. Badge **"110 tests · MIT."** | "It gates its own repo. Adopt it in **one line** of CI, or enable the published Catalog agent. A hundred and ten tests, MIT-licensed." | Design · Tech |
| **8 · Close** | 2:40–2:50 | Recap card, red→green; tagline. Repo URL + MIT + AI Catalog. | "Faultline: know what your change breaks, get the one test that closes it — and an honest answer when it can't be sure." | Idea · Impact |

**Timing protection (2:50 is tight):** the beats that must survive are **0 (cold open)**,
**2 (it acts)**, and **5 (the wow)**. If you overrun: merge **6** into **5** as a single
"honest / fails-closed" montage, and cut **3** to a 4-second caption. Don't add beats.

**Why this order (research-backed):** outcome-first cold open and hackathon name in the
first seconds (YC / Devpost / MLH); problem → it-acts → wow, fast (AngelHack); "acts, not
chatbot" answers Veenhof directly; quantified badges echo the "43 tests" praise; the
`max_hops:4 → compile_error` and fail-closed beats are our unique, fully-demonstrable
"wow" that separates us from the other blast-radius entries; the one-line install answers
Design/Usability and "feels like a product."

**Final judge-aligned framings (verified 2026-06-23 — weave into the VO, don't add beats):**
- **Open as GraphDev's successor (Veenhof):** GraphDev won the previous hackathon's Grand Prize for *showing* "what gets impacted by changes." One early caption or the close should land *"see the impact → now **enforce** it."*
- **Lead with the fix, not the "no" (Veenhof — Design is ¼ of the score):** beat 2 now foregrounds the prescription + the **Duo draft test-MR**; frame every block as "here's the one smallest fix," never just "rejected."
- **Show one test go red→green (Haradon):** in beat 2, run the prescribed test once *failing*, then passing — proof it's load-bearing, not coverage theater.
- **Supervised loop (Hook / Michaux):** say *"AI proposes; a person disposes"* — the decision is deterministic, Duo only **drafts** the fix for a human to approve.
- **One reproducible number (Meister):** keep the BugsInPy **21/32** badge and the byte-identical re-run; he scores what he can re-run. Keep "provably minimal *given the call graph Orbit returns*" — never unqualified.
- **Real, native Duo (Michaux):** the published Catalog agent now declares Orbit's `mcp_tools` (`orbit_query_graph`) — if you can, Chat-invoke it live so the Orbit usage reads as native, not bolted-on.

---

## The hero MR (what beats 0–3, 5 capture) — live & re-verifiable

A one-line change to `standardRate` in `calc/tax.go`. Faultline maps the changed *line*
to the single symbol it edits, then shows the change reaches **7 functions, up to 5 calls
away** — including `netLevy` (4 hops) and `InvoiceTotal` (5 hops), **past what one bounded
Orbit query returns**. **5 are untested**, so the gate **fails the pipeline**, and the
verdict prescribes **1 test, at `Rate`** (the provably-minimal cut). Public:
[faultline-demo MR !1](https://gitlab.com/anbuchelvanganesan.cse2024-group/faultline-demo/-/merge_requests/1).

Polyglot (beat 4): [faultline-polyglot MR !1](https://gitlab.com/anbuchelvanganesan.cse2024-group/faultline-polyglot/-/merge_requests/1).
Dogfood (beat 7): [faultline MR !1](https://gitlab.com/anbuchelvanganesan.cse2024-group/faultline/-/merge_requests/1).

## "Would it catch real bugs?" — BugsInPy (a one-line on-screen badge, or a held beat)

A *one-character* fix to `black`'s tokenizer `_partially_consume_prefix` silently reaches
**5 untested functions** up the parse stack; Faultline prescribes **1 test at
`parse_tokens`** to gate them all. Across `tornado` + `black`, **21 of 32** analyzable real
regressions would have fired the gate. Reproduce offline (no GitLab needed):

```bash
( cd engine && cargo build --release )
git clone --depth 1 https://github.com/soarsmu/BugsInPy /tmp/BugsInPy
git clone https://github.com/psf/black /tmp/black
python3 empirical/faultline_batch.py --bugsinpy /tmp/BugsInPy --project black \
  --project-src /tmp/black --engine engine/target/release/faultline-engine --bugs 10
```

## How to film beat 6 (fail-closed) reproducibly

Point the agent at a project Orbit has **not** finished indexing (0 code definitions) —
it returns the **🟡 Can't vouch** verdict instead of a false ✅:

```bash
faultline-agent --mode rest --project-id <unindexed-project-id> \
  --changed-files main.go --engine engine/target/release/faultline-engine --format md
# → "🟡 Can't vouch — Orbit index incomplete … Faultline will not report this change as safe."
```

---

## On-screen checklist (don't get dinged)
- **State the hackathon name** in the first 5 seconds (lower-third on beat 0).
- Show a **live** Orbit query / the gate running in a **real** pipeline — never mocked.
- Keep the **voiceover jargon-free**; `vertex cut` / `Shapley` stay on-screen in the
  expandable details, never spoken.
- Show the verdict **in the MR**, where a developer actually works.
- Keep one **honest** beat (6 — fail-closed) out loud; it builds the trust judges reward.
- **Direct the eye**: zoom/highlight the verdict; don't show full-screen noise.
- Upload **public**, *Not for Kids*, no copyrighted music, **upload early** (processing
  can take time). End under **3:00**.
