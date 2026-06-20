# Faultline — demo scenario & 3-minute video script

The goal of the video (judging weighs it heavily): a **real** developer story on
**real** code, showing Faultline catch something a normal review would miss — then
the plain-language fix, the depth past Orbit's cap, and the honesty boundary. No
slides, no contrived repo, no jargon in the voiceover.

---

## The demo (use a real, reproducible bug)

**Primary story — `black` #10 (already reproduced in `empirical/`).** A *one-character*
fix to the tokenizer function `_partially_consume_prefix` silently reaches **5 untested
functions up the parse stack** (`parse_tokens`, `parse_string`, `parse_stream`,
`parse_stream_raw`, `parse_file`). Faultline:

- flags the untested blast radius and **fails the gate**,
- says, in plain words, "**add 1 test — at `parse_tokens` — to cover the whole change**"
  (the provably-minimal cut; the coverage ranking shows `parse_tokens` covers all 5),
- shows the impact is **deeper than Orbit's 3-hop query** can see.

Reproduce the numbers offline (no GitLab needed):

```bash
( cd engine && cargo build --release )
git clone --depth 1 https://github.com/soarsmu/BugsInPy /tmp/BugsInPy
git clone https://github.com/psf/black /tmp/black
python3 empirical/faultline_batch.py --bugsinpy /tmp/BugsInPy --project black \
  --project-src /tmp/black --engine engine/target/release/faultline-engine --bugs 10
```

**Live story (for the "Orbit used live" gate) — `demo/polyglot/`.** Push the three-language
sample to a GitLab project with Orbit indexing on, open an MR that bumps the base rate in
`go/rates.go`, `python/rates.py`, and `ruby/rates.rb` at once, and let the CI job post the
verdict. One MR → one verdict spanning Go + Python + Ruby. (See `demo/polyglot/README.md`.)

---

## The script (≤ 2:50, public, no copyrighted music)

**0:00–0:30 — The pain (hook).** *"Alex fixes a one-line bug in a helper, tests pass, the
MR is green, they merge. That night, production breaks — in a function three files away
that nobody in the review ever opened. Code review only shows the diff. The blast radius
is invisible."* (Show a tiny diff, then a red production alert.)

**0:30–1:30 — Faultline catches it (live).** Open the same MR with Faultline enabled. The
pipeline **fails** and posts the verdict. Read the **plain-language summary** on screen:
*"Changing `_partially_consume_prefix` could affect 6 functions — up to 5 calls away, past
Orbit's 3-hop limit. 5 have no tests. Fastest fix: add 1 test at `parse_tokens` to cover
them all."* Expand the details once to flash the impact graph (blue = changed, red =
untested) and the minimum-test-set math — then collapse it. Point out **"Code owners beyond
the diff"**: the owners GitLab's own approval rules would never have pulled in.

**1:30–2:10 — Why it's trustworthy (the moat).** *"This isn't an AI guess. The verdict is
computed deterministically from GitLab's Orbit graph — same change, same answer, every time.
It reaches impact deeper than any single Orbit query can (3-hop cap), and the recommended
test set is provably the smallest one."* Flash the BugsInPy line: *"On 21 of 32 real,
reproduced regressions, this gate would have fired and named the test."*

**2:10–2:40 — Close the loop + honesty.** Show the **"Close the loop with GitLab Duo"**
hand-off — Faultline names the exact test, a Duo flow opens a **draft** MR, a human approves.
Then the honesty beat: *"It won't pretend to know what Orbit can't show it — it states its
limits, and refuses the cross-domain guesses other tools fake."*

**2:40–2:50 — Wrap/CTA.** *"Faultline: see the blast radius before you merge. Deterministic,
open-source, built on GitLab Orbit."* Show the repo URL + MIT.

---

## On-screen checklist (don't get dinged)
- Show a **live** Orbit query / the gate running in a real pipeline (not mocked).
- Keep the **voiceover jargon-free**; the words "vertex cut" / "Shapley" stay on screen
  inside the expandable details, never in narration.
- Show the verdict **in the MR**, where a developer actually works.
- State one honest limitation out loud (coverage heuristic / dynamic dispatch) — it builds trust.
- End under 3:00; public link; no copyrighted music.
