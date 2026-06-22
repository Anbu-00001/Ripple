# Faultline — demo scenario & 3-minute video script

The goal of the video (judging weighs it heavily): a **real** developer story on
**real** code, showing Faultline catch something a normal review would miss — then
the plain-language fix, the depth past Orbit's cap, and the honesty boundary. No
slides, no contrived repo, no jargon in the voiceover.

---

## The demo (use a real, reproducible bug)

**Live hero — the public demo MR (this is the on-screen capture).** A one-line change to
`standardRate` in `calc/tax.go` raises a tax rate. Faultline maps that line to the single
symbol it edits, then shows the change reaches **7 functions, up to 5 calls away** — including
`netLevy` (4 hops) and `InvoiceTotal` (5 hops), **past Orbit's 3-hop query cap**. **5 are
untested**, so the gate **fails the pipeline**, and the verdict prescribes **1 test, at `Rate`,
to cover the whole change** (the provably-minimal cut). It's public and re-verifiable by a judge:
[faultline-demo MR !1](https://gitlab.com/anbuchelvanganesan.cse2024-group/faultline-demo/-/merge_requests/1).

**Empirical evidence (the "would it catch real bugs?" beat) — `black` #10 + BugsInPy.** A
*one-character* fix to the tokenizer `_partially_consume_prefix` silently reaches **5 untested
functions up the parse stack**; Faultline prescribes **1 test at `parse_tokens`** to gate them
all. Across `tornado` + `black`, **21 of 32** analyzable real regressions would have fired the
gate. Reproduce offline (no GitLab needed):

```bash
( cd engine && cargo build --release )
git clone --depth 1 https://github.com/soarsmu/BugsInPy /tmp/BugsInPy
git clone https://github.com/psf/black /tmp/black
python3 empirical/faultline_batch.py --bugsinpy /tmp/BugsInPy --project black \
  --project-src /tmp/black --engine engine/target/release/faultline-engine --bugs 10
```

**Polyglot flash (optional, ~5s) — `demo/polyglot/`.** One MR that bumps the base rate in
`go/rates.go`, `python/rates.py`, and `ruby/rates.rb` at once → one verdict spanning Go +
Python + Ruby. (See `demo/polyglot/README.md`.)

---

## The script (≤ 2:55, public, no copyrighted music)

> The live screen-capture is the **public demo MR** (re-verifiable by a judge), not a mock.
> Voiceover stays jargon-free; "vertex cut" / "Shapley" appear only on-screen in the
> expandable details, never spoken.

**0:00–0:20 — The pain (hook).** *"Alex changes one line in a helper. Tests pass, the MR is
green, they merge. That night production breaks — in a function three files away that nobody
in the review opened. Code review only shows the diff; the blast radius is invisible."* (Show
the one-line diff, then a red production alert.)

**0:20–1:05 — Faultline catches it, live.** Open the real, public demo MR. The pipeline
**fails**. Read the plain-language verdict on screen: *"Changing `standardRate` could affect
7 functions — up to 5 calls away, past Orbit's 3-call limit. 5 have no test. Fastest fix:
add 1 test, at `Rate`, to cover the whole change."* Expand the details once to flash the
impact graph (blue = changed, red = untested) and the minimum-test-set math — then collapse it.

**1:05–1:45 — Why it goes deeper than anything else (the moat + the field).** *"This isn't a
guess, and it isn't shallow."* Show the live proof Orbit's own query can't do this:
`max_hops:4 → compile_error`. *"Orbit answers up to three hops. Other impact agents query it
directly and report the direct callers, then comment a risk score. Faultline pulls the edges
and closes the whole graph — so it surfaces `InvoiceTotal`, five calls deep, that no single
Orbit query can reach — and it doesn't just comment, it **blocks** on the part that's untested."*

**1:45–2:15 — Trustworthy + proven (Technological Implementation).** *"The verdict is computed
deterministically — same change, same answer, every run, with no model in the decision. And the
recommended test set is provably the smallest one, machine-checked against brute force across
107 tests."* Flash BugsInPy: *"On 21 of 32 real, reproduced Python regressions, this gate would
have fired and named the test to add."*

**2:15–2:40 — Close the loop + honesty.** Show the **GitLab Duo** hand-off — Faultline names the
exact test, a Duo flow opens a **draft** MR, a human approves. Then the honesty beat: *"It states
its limits. A project-wide count of findings or owners is a correlation, not impact — so it
refuses the joins Orbit's graph can't actually support, instead of faking them."*

**2:40–2:55 — Wrap/CTA.** *"Faultline: see the blast radius before you merge — and the one test
that closes it. Deterministic, open-source, published to the AI Catalog, built on GitLab Orbit."*
Show the repo URL + MIT + the AI Catalog listing.

---

## Optional beats (use if time allows — they land "native" + "honest")
- **Native surface (≈5s):** flash the MR **Reports** tab showing the Code Quality findings (every untested impacted function, on the Free tier) — Faultline feels like part of GitLab, not a bolted-on bot.
- **Real coverage (1 line of VO):** *"Point it at your existing coverage report and it uses real execution data — the name match is just the fallback."*
- **Adoption comfort (1 line):** *"Draft MRs are advisory; an override label is an audited bypass — nothing is blocked, or skipped, silently."*

## On-screen checklist (don't get dinged)
- Show a **live** Orbit query / the gate running in a real pipeline (not mocked).
- Keep the **voiceover jargon-free**; the words "vertex cut" / "Shapley" stay on screen
  inside the expandable details, never in narration.
- Show the verdict **in the MR**, where a developer actually works.
- State one honest limitation out loud (coverage heuristic / dynamic dispatch) — it builds trust.
- End under 3:00; public link; no copyrighted music.
