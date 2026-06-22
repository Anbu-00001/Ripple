package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Phase 4 — polyglot proof.
//
// Faultline is language-agnostic *by construction*: the engine operates on an
// abstract graph of opaque definition IDs (proven language-blind by the engine's
// random-graph property tests), and the ONLY language-aware surface in the agent
// is test-file detection (isTestFile / readTestCorpus). This test pins that whole
// surface end-to-end on a single graph that mixes Go + Python + Ruby — the exact
// shape of the live Orbit probe `faultline-polyglot` and the demo/ project: one
// merge request bumps the base tax rate in all three language services at once,
// and Faultline must produce ONE verdict whose blast radius and untested gap span
// all three languages, with each language's test convention correctly recognized.
//
// The coverage assertions always run. The full closure additionally runs through
// the REAL faultline-engine binary when it has been built (skipped otherwise, so
// `go test` never depends on a Rust build being present).

// langArm is one language's slice of the polyglot graph: base <- standard <- invoice.
type langArm struct {
	lang                       string
	baseID, stdID, invID       string
	baseName, stdName, invName string
	srcFile                    string // source file (all three symbols live here)
	testRelPath                string // test file, in this language's idiom
}

func polyglotArms() []langArm {
	return []langArm{
		{"go", "g_base", "g_std", "g_inv", "BaseRateGo", "StandardRateGo", "InvoiceTotalGo",
			"rates.go", "go/invoice_test.go"}, // Go: *_test.go
		{"python", "p_base", "p_std", "p_inv", "base_rate_py", "standard_rate_py", "invoice_total_py",
			"rates.py", "python/test_invoice.py"}, // pytest: test_*.py
		{"ruby", "r_base", "r_std", "r_inv", "base_rate_rb", "standard_rate_rb", "invoice_total_rb",
			"rates.rb", "ruby/spec/invoice_spec.rb"}, // RSpec: *_spec.rb under spec/
	}
}

// buildPolyglotGraph returns the normalized graph Orbit would yield for the
// polyglot repo, via the agent's real normalize() (not a hand-built struct).
func buildPolyglotGraph(arms []langArm) graph {
	var defs, calls, extends orbitResp
	for _, a := range arms {
		defs.Result.Nodes = append(defs.Result.Nodes,
			orbitNode{Type: "Definition", ID: a.baseID, Name: a.baseName, FilePath: a.srcFile, DefinitionType: "Method"},
			orbitNode{Type: "Definition", ID: a.stdID, Name: a.stdName, FilePath: a.srcFile, DefinitionType: "Method"},
			orbitNode{Type: "Definition", ID: a.invID, Name: a.invName, FilePath: a.srcFile, DefinitionType: "Function"},
		)
		// standard CALLS+EXTENDS base; invoice CALLS standard.
		calls.Result.Edges = append(calls.Result.Edges,
			orbitEdge{FromID: a.stdID, ToID: a.baseID, Type: "CALLS"},
			orbitEdge{FromID: a.invID, ToID: a.stdID, Type: "CALLS"},
		)
		extends.Result.Edges = append(extends.Result.Edges,
			orbitEdge{FromID: a.stdID, ToID: a.baseID, Type: "EXTENDS"},
		)
	}
	return normalize(defs, calls, extends)
}

// writePolyglotTests materializes each language's test file (in its own idiom)
// under a temp repo, each referencing only its invoice symbol. readTestCorpus
// must recognize all three conventions for coverage to span the languages.
func writePolyglotTests(t *testing.T, arms []langArm) string {
	t.Helper()
	root := t.TempDir()
	bodies := map[string]string{
		"go":     "package rates\nimport \"testing\"\nfunc TestInvoiceTotalGo(t *testing.T){ if InvoiceTotalGo(100)<=100 {t.Fatal(\"x\")} }\n",
		"python": "from rates import invoice_total_py\ndef test_invoice_total_py():\n    assert invoice_total_py(100) > 100\n",
		"ruby":   "require_relative '../rates'\nRSpec.describe 'invoice_total_rb' do\n  it('taxes'){ expect(invoice_total_rb(100)).to be > 100 }\nend\n",
	}
	for _, a := range arms {
		p := filepath.Join(root, filepath.FromSlash(a.testRelPath))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(bodies[a.lang]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestPolyglotCoverageSpansThreeLanguages is the always-on core: it proves the
// agent's only language-aware surface recognizes Go, Python and Ruby test
// conventions and correctly partitions a mixed-language graph into tested vs
// untested — no engine binary required.
func TestPolyglotCoverageSpansThreeLanguages(t *testing.T) {
	arms := polyglotArms()
	g := buildPolyglotGraph(arms)

	// Sanity: normalize ingested all three languages into one graph.
	if len(g.Nodes) != 9 {
		t.Fatalf("want 9 nodes across 3 languages, got %d", len(g.Nodes))
	}

	corpus := readTestCorpus(writePolyglotTests(t, arms))
	tested := coveredDefIDs(g.Nodes, corpus)

	for _, a := range arms {
		if !contains(tested, a.invID) {
			t.Fatalf("%s: invoice symbol %q should be detected as tested (its %s test file was not recognized)",
				a.lang, a.invName, a.testRelPath)
		}
		if contains(tested, a.stdID) || contains(tested, a.baseID) {
			t.Fatalf("%s: rate-chain symbol unexpectedly marked tested", a.lang)
		}
	}
	if len(tested) != 3 {
		t.Fatalf("want exactly 3 tested (one invoice per language), got %d: %v", len(tested), tested)
	}
}

// TestPolyglotEndToEndThroughEngine runs the REAL faultline-engine on the mixed
// Go+Python+Ruby graph and asserts the closure + untested gap + minimum test set
// genuinely span all three languages in a single verdict. Skips if the engine
// has not been built (so the suite never hard-depends on a Rust toolchain).
func TestPolyglotEndToEndThroughEngine(t *testing.T) {
	engine := findEngineBinary()
	if engine == "" {
		t.Skip("faultline-engine not built (run `cargo build` in ../engine); coverage path is covered by TestPolyglotCoverageSpansThreeLanguages")
	}
	arms := polyglotArms()
	g := buildPolyglotGraph(arms)
	corpus := readTestCorpus(writePolyglotTests(t, arms))
	tested := coveredDefIDs(g.Nodes, corpus)

	// The merge request bumps the base rate in ALL THREE language services.
	changed := []string{arms[0].baseID, arms[1].baseID, arms[2].baseID}

	graphPath := filepath.Join(t.TempDir(), "graph.json")
	data, _ := json.MarshalIndent(g, "", "  ")
	if err := os.WriteFile(graphPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(engine, "--graph", graphPath,
		"--changed", strings.Join(changed, ","),
		"--tested", strings.Join(tested, ",")).Output()
	if err != nil {
		t.Fatalf("engine failed: %v", err)
	}
	var rep report
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("bad engine JSON: %v", err)
	}

	// Closure spans all three languages: 6 impacted (std+invoice per arm), depth 2.
	if rep.ImpactedCount != 6 {
		t.Fatalf("want 6 impacted across 3 languages, got %d (%+v)", rep.ImpactedCount, rep.BlastRadius)
	}
	if rep.MaxDepth != 2 {
		t.Fatalf("want max depth 2 (base->standard->invoice), got %d", rep.MaxDepth)
	}
	exts := map[string]bool{}
	for _, it := range rep.BlastRadius {
		exts[filepath.Ext(it.FilePath)] = true
	}
	for _, want := range []string{".go", ".py", ".rb"} {
		if !exts[want] {
			t.Fatalf("blast radius missing %s symbols — not polyglot: %+v", want, rep.BlastRadius)
		}
	}

	// Untested gap = the three standard-rate symbols (invoices are tested), one per language.
	untested := untestedImpact(rep.BlastRadius, corpus)
	var unNames []string
	for _, u := range untested {
		unNames = append(unNames, u.Name)
	}
	sort.Strings(unNames)
	want := []string{"standard_rate_py", "standard_rate_rb", "StandardRateGo"}
	sort.Strings(want)
	if strings.Join(unNames, ",") != strings.Join(want, ",") {
		t.Fatalf("untested gap should be the 3 standard-rate symbols, got %v", unNames)
	}

	// One verdict naming all three languages' files.
	md := renderMarkdown(rep, []string{arms[0].baseName, arms[1].baseName, arms[2].baseName}, untested, false, true, "")
	for _, f := range []string{"rates.go", "rates.py", "rates.rb"} {
		if !strings.Contains(md, f) {
			t.Fatalf("verdict should mention %s (polyglot in one report):\n%s", f, md)
		}
	}
}

// findEngineBinary locates a built faultline-engine, preferring release.
func findEngineBinary() string {
	for _, rel := range []string{
		"../engine/target/release/faultline-engine",
		"../engine/target/debug/faultline-engine",
	} {
		if abs, err := filepath.Abs(rel); err == nil {
			if fi, e := os.Stat(abs); e == nil && !fi.IsDir() {
				return abs
			}
		}
	}
	return ""
}
