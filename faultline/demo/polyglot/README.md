# Polyglot demo — one merge request, three languages, one verdict

Faultline is **language-agnostic by construction**. The engine computes the
transitive closure over an abstract graph of opaque definition IDs — it never
looks at file types — and the agent pulls that graph from GitLab Orbit, which
emits `CALLS` and `EXTENDS` edges for every language it indexes. The *only*
language-aware code in Faultline is test-file detection (which conventions count
as "covered").

This directory is the example that proves it. The **same rate chain** is
implemented three times — once in **Go**, once in **Python**, once in **Ruby**
(GitLab's own Rails stack):

```
BaseRate.rate()                # the "merge request" changes this one line
   ▲  (CALLS + EXTENDS)
StandardRate.rate()            # untested
   ▲  (CALLS)
invoice_total(amount)          # the only symbol with a test
```

In each language only `invoice_total` is tested, so a one-line change to
`BaseRate.rate` has an **untested blast radius** (`StandardRate.rate`). An MR that
bumps the base rate in all three services at once produces a **single Faultline
verdict whose blast radius and untested gap span Go + Python + Ruby together.**

| Arm | Source | Test (its idiom) | Verified here |
|---|---|---|---|
| Go | [go/rates.go](go/rates.go) | [go/invoice_test.go](go/invoice_test.go) (`_test.go`) | ✅ `go test` passes |
| Python | [python/rates.py](python/rates.py) | [python/test_invoice.py](python/test_invoice.py) (`test_*.py`) | ✅ `pytest` passes |
| Ruby | [ruby/rates.rb](ruby/rates.rb) | [ruby/spec/invoice_spec.rb](ruby/spec/invoice_spec.rb) (`*_spec.rb` under `spec/`) | idiomatic RSpec (no Ruby runtime in this build env; see note) |

## Run the language arms

```bash
( cd go     && go test ./... )                  # ok
( cd python && python3 -m pytest -q )            # 1 passed
( cd ruby   && rspec spec/ )                     # requires rspec
```

## Reproduce the Faultline verdict on this repo

1. Push this `demo/polyglot/` tree to a GitLab project with **Orbit indexing** on.
2. Orbit indexes all three languages and emits `CALLS` + `EXTENDS` edges for each
   (**verified live** on the probe project `faultline-polyglot`, public, under the
   contest group: Orbit returned full `CALLS` — Python 4 / Go 7 / Ruby 3 — and
   `EXTENDS` inheritance for **all three** languages; Ruby and Python are not
   imports-only).
3. Open an MR that changes the base `rate` in one or more arms and run the gate:

   ```bash
   faultline-agent --project-id <PID> --engine ./faultline-engine \
     --repo-root . --changed-files go/rates.go,python/rates.py,ruby/rates.rb \
     --format md
   ```

   The verdict's blast radius spans `rates.go`, `rates.py` and `rates.rb`, flags the
   three untested `StandardRate.rate` symbols, and prescribes the minimum test set.

## What proves the polyglot claim (and what doesn't)

- **Engine language-blindness** is machine-checked, always-on:
  `closure_is_language_blind_across_go_python_ruby` ([../../engine/src/main.rs](../../engine/src/main.rs))
  asserts the closure over a mixed Go/Python/Ruby graph is byte-identical when the
  file extensions are swapped — file type never affects the computation.
- **The agent's language-aware surface** (recognizing each language's test
  convention, partitioning a mixed graph into tested/untested, and running the
  **real** engine binary end-to-end) is machine-checked by
  `TestPolyglotCoverageSpansThreeLanguages` and `TestPolyglotEndToEndThroughEngine`
  ([../../agent/polyglot_test.go](../../agent/polyglot_test.go)).
- **Orbit's per-language edges** were verified live (step 2 above), not assumed.
- **Honesty note:** Faultline does *not* claim cross-language call edges. Orbit
  links calls *within* a language; a Go service calling a Ruby service over HTTP is
  not a `CALLS` edge and Faultline does not pretend otherwise. The polyglot proof is
  "one indexed repo, three languages, one correct verdict" — not "calls that cross a
  language boundary." The Ruby arm is idiomatic RSpec but was not executed in this
  build environment (no Ruby runtime installed); its role is to be indexed by Orbit
  and read by a human — the live probe above is what confirms Orbit handles it.
