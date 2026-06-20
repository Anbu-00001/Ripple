package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeDedupAndEdgeMapping(t *testing.T) {
	var defs, calls orbitResp
	defs.Result.Nodes = []orbitNode{
		{Type: "Definition", ID: "A", Name: "applyRate", FilePath: "calc/tax.go", DefinitionType: "Function"},
		{Type: "Definition", ID: "C", Name: "CalculateTax", FilePath: "calc/tax.go", DefinitionType: "Function"},
		{Type: "Definition", ID: "D", Name: "ApplyDiscount", FilePath: "calc/tax.go"}, // no definition_type
	}
	calls.Result.Nodes = []orbitNode{
		{ID: "A", Name: "applyRate", FilePath: "calc/tax.go"}, // duplicate of A
		{ID: "C", Name: "CalculateTax", FilePath: "calc/tax.go"},
	}
	calls.Result.Edges = []orbitEdge{
		{From: "Definition", FromID: "C", To: "Definition", ToID: "A", Type: "CALLS"},
		{FromID: "C", ToID: "A", Type: "IMPORTS"}, // non-CALLS must be ignored
	}

	g := normalize(defs, calls)

	if len(g.Nodes) != 3 {
		t.Fatalf("want 3 deduped nodes, got %d", len(g.Nodes))
	}
	if len(g.Edges) != 1 {
		t.Fatalf("want 1 CALLS edge (IMPORTS ignored), got %d", len(g.Edges))
	}
	if g.Edges[0].From != "C" || g.Edges[0].To != "A" {
		t.Fatalf("edge maps from_id/to_id wrong: %+v", g.Edges[0])
	}
	for _, n := range g.Nodes {
		if n.ID == "D" && n.DefinitionType != "Definition" {
			t.Fatalf("missing definition_type should default to Definition (not Function), got %q", n.DefinitionType)
		}
	}
}

func TestResolveChanged(t *testing.T) {
	g := graph{Nodes: []gNode{
		{ID: "A", FilePath: "calc/tax.go"},
		{ID: "C", FilePath: "calc/tax.go"},
		{ID: "T", FilePath: "calc/order.go"},
	}}

	if got := resolveChanged(g, nil, []string{"calc/tax.go"}); len(got) != 2 {
		t.Fatalf("by-file: want 2 defs in tax.go, got %d (%v)", len(got), got)
	}
	if got := resolveChanged(g, []string{"A", "A"}, nil); len(got) != 1 || got[0] != "A" {
		t.Fatalf("by-id dedup: want [A], got %v", got)
	}
	if got := resolveChanged(g, nil, []string{"missing.go"}); len(got) != 0 {
		t.Fatalf("unknown file: want 0, got %v", got)
	}
	// union of ids + files, deduped (A appears in both)
	if got := resolveChanged(g, []string{"A"}, []string{"calc/order.go"}); len(got) != 2 {
		t.Fatalf("union: want 2 (A + T), got %d (%v)", len(got), got)
	}
}

func TestRenderMarkdownEmptyBlastRadius(t *testing.T) {
	md := renderMarkdown(report{ImpactedCount: 0}, []string{"ApplyDiscount"}, nil, false)
	if !strings.Contains(md, "Safe to merge") {
		t.Fatalf("empty radius should be called out, got:\n%s", md)
	}
	if strings.Contains(md, "Affected function |") {
		t.Fatalf("empty radius must not render an impact table, got:\n%s", md)
	}
	if !strings.Contains(md, "`ApplyDiscount`") {
		t.Fatalf("changed name should be shown, got:\n%s", md)
	}
}

func TestRenderMarkdownDeepChainFlagsCap(t *testing.T) {
	r := report{
		ImpactedCount: 2,
		MaxDepth:      5,
		BlastRadius: []impacted{
			{Name: "CalculateTax", FilePath: "calc/tax.go", Distance: 1},
			{ID: "999", FilePath: "calc/order.go", Distance: 5}, // no name -> labeled unresolved
		},
	}
	md := renderMarkdown(r, []string{"applyRate"}, nil, false)
	if !strings.Contains(md, "could affect **2** other function(s)") {
		t.Fatalf("missing impact count, got:\n%s", md)
	}
	if !strings.Contains(md, "past Orbit's 3-call query limit") {
		t.Fatalf("depth>3 should flag the 3-hop cap (the moat), got:\n%s", md)
	}
	if !strings.Contains(md, "| `CalculateTax` | calc/tax.go | 1 |") {
		t.Fatalf("table row malformed, got:\n%s", md)
	}
	if !strings.Contains(md, "| `(unresolved #999)` | calc/order.go | 5 |") {
		t.Fatalf("nameless impacted node should be labeled (unresolved #id), got:\n%s", md)
	}
}

func TestRenderMarkdownEscapesTableInjection(t *testing.T) {
	// Attacker-controlled symbol name with pipes/newlines/backticks must not
	// break out of the Markdown table cell when posted to a live MR note.
	r := report{
		ImpactedCount: 1,
		MaxDepth:      1,
		BlastRadius: []impacted{
			{Name: "evil | col | inj\n## heading `x`", FilePath: "a|b\nc.go", Distance: 1},
		},
	}
	md := renderMarkdown(r, []string{"changed | name"}, nil, false)
	if strings.Contains(md, "evil | col") {
		t.Fatalf("raw pipe leaked into table cell (injection), got:\n%s", md)
	}
	if strings.Contains(md, "\n## heading") {
		t.Fatalf("newline/heading injection escaped the row, got:\n%s", md)
	}
	if !strings.Contains(md, "\\|") {
		t.Fatalf("pipes should be escaped as \\|, got:\n%s", md)
	}
	// the verdict must still be a single well-formed table (one data row line)
	if strings.Count(md, "| `") < 1 {
		t.Fatalf("expected a code-wrapped cell, got:\n%s", md)
	}
}

func TestUntestedImpact(t *testing.T) {
	blast := []impacted{
		{Name: "CalculateTax", FilePath: "calc/tax.go", Distance: 1},
		{Name: "TotalWithTax", FilePath: "calc/order.go", Distance: 2},
		{Name: "InvoiceTotal", FilePath: "calc/pipeline.go", Distance: 5},
		{ID: "999", Name: "", Distance: 1}, // unresolved -> skipped (can't assess)
	}
	// test corpus references CalculateTax but not the others.
	corpus := "func TestCalculateTax(t *testing.T){ CalculateTax(100) }\n"
	got := untestedImpact(blast, corpus)
	names := map[string]bool{}
	for _, it := range got {
		names[it.Name] = true
	}
	if names["CalculateTax"] {
		t.Fatalf("CalculateTax is referenced in tests; must NOT be untested: %v", got)
	}
	if !names["TotalWithTax"] || !names["InvoiceTotal"] {
		t.Fatalf("TotalWithTax and InvoiceTotal lack tests; must be untested: %v", got)
	}
	if len(got) != 2 {
		t.Fatalf("want exactly 2 untested (unresolved skipped), got %d: %v", len(got), got)
	}
	// word-boundary: a name that is only a substring of a tested token is untested.
	if u := untestedImpact([]impacted{{Name: "Tax"}}, "CalculateTax()"); len(u) != 1 {
		t.Fatalf("'Tax' is only a substring of CalculateTax; must be untested, got %v", u)
	}
}

func TestIsTestFile(t *testing.T) {
	// The test-file detector is the ONLY language-aware surface in the agent (the
	// engine is language-agnostic), so it must recognize each ecosystem's idiom
	// AND not over-match ordinary source files — a false positive would count a
	// non-test file as coverage and silently hide a real untested blast radius.
	cases := []struct {
		lang string
		yes  []string
		no   []string
	}{
		{"Go", []string{"calc/tax_test.go"}, []string{"calc/tax.go", "main.go"}},
		{"Python", []string{"x/test_foo.py", "py/api_tests.py", "pkg/foo_test.py", "tests/foo.py"},
			[]string{"py/foo.py", "py/contest.py"}}, // "contest" must not trip the test_ prefix
		{"Ruby", []string{"svc/user_spec.rb", "models/user_test.rb", "spec/rails_helper.rb", "test/models/user.rb"},
			[]string{"app/models/user.rb", "lib/spec.rb"}}, // a source file named spec.rb is not a test
		{"JS/TS", []string{"a/foo.test.ts", "b/Foo.spec.js", "core/Foo.spec.mjs", "pkg/__tests__/x.js"},
			[]string{"src/foo.ts", "src/app.js"}},
		{"Java/Kotlin", []string{"m/BarTest.java", "m/BarTests.java", "m/FooIT.java", "k/BarTest.kt"},
			[]string{"m/Bar.java", "k/Bar.kt"}},
		{"other", []string{"app/UserTest.php", "lib/calc_test.exs", "ios/LoginTests.swift", "r/calc_test.rs", "d/calc_test.dart", "cs/FooTests.cs"},
			[]string{"app/User.php", "r/calc.rs"}},
		// Path precision: substrings that merely contain "test"/"spec" must NOT match.
		{"precision", nil, []string{"internal/latest/cache.go", "api/apispec/handler.go", "src/contestant/x.go"}},
	}
	for _, c := range cases {
		for _, f := range c.yes {
			if !isTestFile(f) {
				t.Errorf("[%s] %q should be detected as a test file", c.lang, f)
			}
		}
		for _, f := range c.no {
			if isTestFile(f) {
				t.Errorf("[%s] %q must NOT be a test file (a false positive hides coverage gaps)", c.lang, f)
			}
		}
	}
	// configurable: a project-specific convention via FAULTLINE_TEST_PATTERNS.
	if isTestFile("qa/check.bats") {
		t.Fatalf(".bats is not built-in; must not match without an extra pattern")
	}
	if !isTestFile("qa/check.bats", ".bats") {
		t.Fatalf("extra pattern .bats should mark the file as a test")
	}
}

func TestQueryLimitRespectsEnvAndBuildersHonorIt(t *testing.T) {
	t.Setenv("FAULTLINE_QUERY_LIMIT", "5000")
	if queryLimit() != 5000 {
		t.Fatalf("env override ignored, got %d", queryLimit())
	}
	t.Setenv("FAULTLINE_QUERY_LIMIT", "")
	if queryLimit() != defaultQueryLimit {
		t.Fatalf("empty env should use default %d, got %d", defaultQueryLimit, queryLimit())
	}
	t.Setenv("FAULTLINE_QUERY_LIMIT", "-3")
	if queryLimit() != defaultQueryLimit {
		t.Fatalf("invalid env must fall back to default, got %d", queryLimit())
	}
	// no hardcoded 1000: the limit arg must appear in every built query.
	for _, body := range []string{defsQuery(7, 5000), callsQuery(7, 5000), extendsQuery(7, 5000)} {
		if !strings.Contains(body, `"limit":5000`) {
			t.Fatalf("query did not honor the limit arg: %s", body)
		}
	}
}

func TestRenderMarkdownShowsUntestedSection(t *testing.T) {
	r := report{ImpactedCount: 1, MaxDepth: 2, BlastRadius: []impacted{{Name: "TotalWithTax", FilePath: "calc/order.go", Distance: 1}}}
	md := renderMarkdown(r, []string{"CalculateTax"}, []impacted{{Name: "TotalWithTax", FilePath: "calc/order.go", Distance: 1}}, false)
	if !strings.Contains(md, "Heads-up (won't block your merge)") {
		t.Fatalf("non-blocking untested verdict should show the Heads-up badge:\n%s", md)
	}
	if !strings.Contains(md, "Functions with no test that this could break") {
		t.Fatalf("untested list missing:\n%s", md)
	}
	if !strings.Contains(md, "`TotalWithTax`") {
		t.Fatalf("untested symbol not listed:\n%s", md)
	}
	// blocking=true must flip the badge to Blocked.
	if blk := renderMarkdown(r, []string{"CalculateTax"}, []impacted{{Name: "TotalWithTax", Distance: 1}}, true); !strings.Contains(blk, "⛔ Blocked") {
		t.Fatalf("blocking verdict should show the Blocked badge:\n%s", blk)
	}
}

func TestBuildMermaid(t *testing.T) {
	g := graph{
		Nodes: []gNode{
			{ID: "C", Name: "standardRate"}, {ID: "R", Name: "Rate"},
			{ID: "L", Name: "levyBase"}, {ID: "X", Name: "ApplyDiscount"},
		},
		Edges: []gEdge{
			{Type: "CALLS", From: "R", To: "C"}, // Rate calls standardRate
			{Type: "CALLS", From: "L", To: "R"}, // levyBase calls Rate
			{Type: "CALLS", From: "X", To: "C"}, // unrelated edge to a non-impacted node stays out
		},
	}
	rep := report{
		ImpactedCount: 2,
		BlastRadius:   []impacted{{ID: "R", Name: "Rate", Distance: 1}, {ID: "L", Name: "levyBase", Distance: 2}},
	}
	untested := []impacted{{ID: "L", Name: "levyBase", Distance: 2}}
	out := buildMermaid(g, []string{"C"}, rep, untested)

	// changed = circle (( )), untested = diamond { }, tested/other = rect [ ]
	for _, want := range []string{"```mermaid", "graph TD", "((\"standardRate\"))", "[\"Rate\"]", "{\"levyBase\"}"} {
		if !strings.Contains(out, want) {
			t.Fatalf("mermaid missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, mermaidID("R")+" --> "+mermaidID("C")) {
		t.Fatalf("missing edge R->C:\n%s", out)
	}
	if strings.Contains(out, "[\"ApplyDiscount\"]") {
		t.Fatalf("non-impacted node X should not appear:\n%s", out)
	}
	if !strings.Contains(out, "class "+mermaidID("C")+" changed;") {
		t.Fatalf("changed node not styled:\n%s", out)
	}
	if !strings.Contains(out, "class "+mermaidID("L")+" untested;") {
		t.Fatalf("untested node not styled red:\n%s", out)
	}
	// determinism: same input -> identical output
	if buildMermaid(g, []string{"C"}, rep, untested) != out {
		t.Fatalf("buildMermaid is non-deterministic")
	}
	// empty blast radius -> no diagram
	if buildMermaid(g, []string{"C"}, report{ImpactedCount: 0}, nil) != "" {
		t.Fatalf("empty blast radius should produce no mermaid")
	}
}

func TestNormalizeSkipsEmptyAndNonCallsEdges(t *testing.T) {
	var defs, calls orbitResp
	defs.Result.Nodes = []orbitNode{{ID: "A", Name: "a"}, {ID: "B", Name: "b"}}
	calls.Result.Edges = []orbitEdge{
		{FromID: "A", ToID: "B", Type: "CALLS"},   // keep
		{FromID: "", ToID: "B", Type: "CALLS"},    // drop: empty from (partial Orbit response)
		{FromID: "A", ToID: "", Type: "CALLS"},    // drop: empty to
		{FromID: "A", ToID: "B", Type: "IMPORTS"}, // drop: not CALLS
	}
	g := normalize(defs, calls)
	if len(g.Edges) != 1 {
		t.Fatalf("want 1 clean CALLS edge, got %d (%v)", len(g.Edges), g.Edges)
	}
	if g.Edges[0].From != "A" || g.Edges[0].To != "B" {
		t.Fatalf("edge mapped wrong: %+v", g.Edges[0])
	}
}

func TestRecipeComparison(t *testing.T) {
	// Deep chain: depths 1..5 -> 3 within Orbit's reach, 2 beyond (>=4 hops).
	r := report{
		ImpactedCount: 5,
		MaxDepth:      5,
		BlastRadius: []impacted{
			{Name: "Rate", FilePath: "calc/pipeline.go", Distance: 1},
			{Name: "levyBase", FilePath: "calc/pipeline.go", Distance: 2},
			{Name: "grossLevy", FilePath: "calc/pipeline.go", Distance: 3},
			{Name: "netLevy", FilePath: "calc/pipeline.go", Distance: 4},
			{Name: "InvoiceTotal", FilePath: "calc/pipeline.go", Distance: 5},
		},
	}
	out := recipeComparison(r)
	if !strings.Contains(out, "3 of 5") {
		t.Fatalf("should report 3 of 5 within Orbit's reach, got:\n%s", out)
	}
	if !strings.Contains(out, "`netLevy`") || !strings.Contains(out, "`InvoiceTotal`") {
		t.Fatalf("beyond-cap symbols (>=4 hops) must be listed, got:\n%s", out)
	}
	if strings.Contains(out, "`grossLevy`") {
		t.Fatalf("grossLevy is at 3 hops (within reach) and must NOT be in the beyond list, got:\n%s", out)
	}
	if !strings.Contains(out, "5 hops") {
		t.Fatalf("should show per-symbol hop distance, got:\n%s", out)
	}
	// Shallow change: nothing beyond 3 hops -> no comparison block (no overclaim).
	shallow := report{ImpactedCount: 2, MaxDepth: 3, BlastRadius: []impacted{
		{Name: "A", Distance: 1}, {Name: "B", Distance: 3},
	}}
	if recipeComparison(shallow) != "" {
		t.Fatalf("shallow change must produce no recipe block, got:\n%s", recipeComparison(shallow))
	}
	if recipeComparison(report{ImpactedCount: 0}) != "" {
		t.Fatalf("empty blast radius must produce no recipe block")
	}
}

func TestRenderMarkdownShallowDoesNotClaimCap(t *testing.T) {
	r := report{ImpactedCount: 1, MaxDepth: 2, BlastRadius: []impacted{{Name: "X", Distance: 2}}}
	md := renderMarkdown(r, nil, nil, false)
	if strings.Contains(md, "3-hop") {
		t.Fatalf("depth<=3 must NOT claim to beat the 3-hop cap, got:\n%s", md)
	}
}

func TestNormalizeMergesCallsAndExtends(t *testing.T) {
	var defs, calls, extends orbitResp
	defs.Result.Nodes = []orbitNode{{ID: "B", Name: "Base"}, {ID: "T", Name: "Sub"}, {ID: "X", Name: "Caller"}}
	calls.Result.Edges = []orbitEdge{
		{FromID: "X", ToID: "B", Type: "CALLS"},   // keep
		{FromID: "X", ToID: "B", Type: "IMPORTS"}, // drop: not an impact edge
	}
	extends.Result.Edges = []orbitEdge{
		{FromID: "T", ToID: "B", Type: "EXTENDS"}, // keep: subtype edge
		{FromID: "", ToID: "B", Type: "EXTENDS"},  // drop: empty endpoint
	}
	g := normalize(defs, calls, extends)
	if len(g.Edges) != 2 {
		t.Fatalf("want 2 impact edges (1 CALLS + 1 EXTENDS), got %d (%v)", len(g.Edges), g.Edges)
	}
	var hasCalls, hasExtends bool
	for _, e := range g.Edges {
		if e.Type == "CALLS" && e.From == "X" && e.To == "B" {
			hasCalls = true
		}
		if e.Type == "EXTENDS" && e.From == "T" && e.To == "B" {
			hasExtends = true
		}
	}
	if !hasCalls || !hasExtends {
		t.Fatalf("expected both a CALLS and an EXTENDS edge, got %+v", g.Edges)
	}
	// back-compat: single edge response still works
	if g2 := normalize(defs, calls); len(g2.Edges) != 1 {
		t.Fatalf("back-compat normalize(defs, calls): want 1 edge, got %d", len(g2.Edges))
	}
}

// TestEdgeQueriesAreValidAndOneHop locks down the contract Faultline relies on:
// the agent pulls only 1-hop CALLS/EXTENDS edges from Orbit and the Rust engine
// computes the transitive closure. If someone "optimizes" by raising max_hops to
// reach deeper, they would hit Orbit's hard cap of 3 (compile_error) — so the
// 1-hop invariant is load-bearing, not incidental. Each builder must also embed
// the project_id filter and emit valid JSON.
func TestEdgeQueriesAreValidAndOneHop(t *testing.T) {
	cases := []struct {
		name string
		body string
		typ  string
	}{
		{"callsQuery", callsQuery(42, defaultQueryLimit), "CALLS"},
		{"extendsQuery", extendsQuery(42, defaultQueryLimit), "EXTENDS"},
	}
	for _, c := range cases {
		var v any
		if err := json.Unmarshal([]byte(c.body), &v); err != nil {
			t.Fatalf("%s: not valid JSON: %v", c.name, err)
		}
		if !strings.Contains(c.body, `"type":"`+c.typ+`"`) {
			t.Errorf("%s: missing relationship type %q", c.name, c.typ)
		}
		if !strings.Contains(c.body, `"value":42`) {
			t.Errorf("%s: missing project_id filter (value:42)", c.name)
		}
		// load-bearing: 1-hop only, never approaching Orbit's max_hops cap of 3.
		if !strings.Contains(c.body, `"max_hops":1`) {
			t.Errorf("%s: expected max_hops:1 (engine does the closure), got %s", c.name, c.body)
		}
	}
}

func TestCoveredDefIDs(t *testing.T) {
	nodes := []gNode{
		{ID: "1", Name: "CalculateTax"},
		{ID: "2", Name: "InvoiceTotal"},
		{ID: "3", Name: ""}, // unnamed nodes are skipped
	}
	corpus := "func TestCalculateTax(t *testing.T){ CalculateTax(100) }\n"
	got := coveredDefIDs(nodes, corpus)
	if len(got) != 1 || got[0] != "1" {
		t.Fatalf("expected only CalculateTax (id 1) covered, got %v", got)
	}
	if len(coveredDefIDs(nodes, "")) != 0 {
		t.Errorf("empty corpus must cover nothing")
	}
}

func TestRenderMarkdownShowsMinimumTestSet(t *testing.T) {
	// 4 untested impacted, but the engine's min-cut says one test gates them all.
	r := report{
		ImpactedCount: 4, MaxDepth: 2,
		BlastRadius:    []impacted{{Name: "CalculateTax", Distance: 1}},
		UntestedCount:  4,
		MinimumTestSet: []cutNode{{ID: "M", Name: "CalculateTax", FilePath: "calc/tax.rb"}},
	}
	md := renderMarkdown(r, []string{"standardRate"}, []impacted{{Name: "CalculateTax", Distance: 1}}, false)
	if !strings.Contains(md, "Fastest fix") || !strings.Contains(md, "`CalculateTax`") {
		t.Errorf("expected the plain-language fix headline, got:\n%s", md)
	}
	if !strings.Contains(md, "Smallest set of tests that gates the whole change") || !strings.Contains(md, "vs 4 untested") {
		t.Errorf("expected the minimum-test-set detail with 'vs 4 untested', got:\n%s", md)
	}
	if !strings.Contains(md, "minimum vertex cut") {
		t.Errorf("expected the provably-minimal attribution in details, got:\n%s", md)
	}
	// when there is no cut, the section must be absent.
	if strings.Contains(renderMarkdown(report{ImpactedCount: 1, BlastRadius: []impacted{{Name: "X"}}}, nil, nil, false), "Smallest set of tests") {
		t.Errorf("min-test-set section must not render when empty")
	}
}

func TestRenderMarkdownShowsRiskAttribution(t *testing.T) {
	r := report{
		ImpactedCount: 3, MaxDepth: 2,
		BlastRadius:   []impacted{{Name: "startServer", Distance: 1}},
		UntestedCount: 3,
		RiskAttribution: []riskShare{
			{ID: "P", Name: "parseConfig", FilePath: "cfg.go", Shapley: 2.0, SharePct: 66.7},
			{ID: "Q", Name: "loadEnv", FilePath: "cfg.go", Shapley: 1.0, SharePct: 33.3},
		},
		RiskAttributionExact: true,
	}
	md := renderMarkdown(r, []string{"parseConfig", "loadEnv"}, []impacted{{Name: "startServer", Distance: 1}}, false)
	if !strings.Contains(md, "Who owns the gap") {
		t.Fatalf("risk-attribution section missing:\n%s", md)
	}
	if !strings.Contains(md, "`parseConfig`") || !strings.Contains(md, "67%") {
		t.Fatalf("top owner + rounded percentage not shown:\n%s", md)
	}
	if !strings.Contains(md, "Shapley value") {
		t.Fatalf("expected the Shapley attribution note:\n%s", md)
	}
	if strings.Contains(md, "approximate") {
		t.Fatalf("exact attribution must NOT be labeled approximate:\n%s", md)
	}

	// A single changed symbol owns 100% trivially — that's noise, so it must be hidden.
	one := report{ImpactedCount: 1, BlastRadius: []impacted{{Name: "X"}},
		RiskAttribution: []riskShare{{ID: "P", Name: "p", Shapley: 1, SharePct: 100}}}
	if strings.Contains(renderMarkdown(one, nil, nil, false), "Who owns the gap") {
		t.Errorf("attribution must be hidden for a single changed symbol")
	}

	// Sampled (large changed set) attribution must be flagged honestly.
	r.RiskAttributionExact = false
	if !strings.Contains(renderMarkdown(r, nil, []impacted{{Name: "startServer"}}, false), "approximate") {
		t.Errorf("sampled attribution must be labeled approximate:\n%s", renderMarkdown(r, nil, []impacted{{Name: "startServer"}}, false))
	}
}

func TestBuildMermaidDeHairballsLargeRadius(t *testing.T) {
	// 30 callers of a changed node: the diagram must collapse to essential nodes.
	nodes := []gNode{{ID: "CH", Name: "changed"}}
	edges := []gEdge{}
	radius := []impacted{}
	untested := []impacted{}
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("n%d", i)
		nodes = append(nodes, gNode{ID: id, Name: id})
		edges = append(edges, gEdge{Type: "CALLS", From: id, To: "CH"})
		radius = append(radius, impacted{ID: id, Name: id, Distance: 1})
		if i < 2 { // only 2 are untested
			untested = append(untested, impacted{ID: id, Name: id, Distance: 1})
		}
	}
	g := graph{Nodes: nodes, Edges: edges}
	r := report{ImpactedCount: 30, MaxDepth: 1, BlastRadius: radius,
		MinimumTestSet: []cutNode{{ID: "n0", Name: "n0"}, {ID: "n1", Name: "n1"}}}
	md := buildMermaid(g, []string{"CH"}, r, untested)
	if !strings.Contains(md, "more impacted node(s) hidden") {
		t.Fatalf("large radius should truncate the diagram, got:\n%s", md)
	}
	// essential = changed + 2 untested (= min test set); the other 28 must be gone.
	if strings.Contains(md, "\"n5\"") || strings.Contains(md, "\"n20\"") {
		t.Fatalf("non-essential node leaked into the de-hairballed diagram:\n%s", md)
	}
	if !strings.Contains(md, "\"changed\"") || !strings.Contains(md, "\"n0\"") {
		t.Fatalf("essential nodes (change + untested) must remain:\n%s", md)
	}
}

func TestHubNotes(t *testing.T) {
	// `hub` is called directly by 3 functions; threshold 3 flags it, 4 doesn't.
	g := graph{
		Nodes: []gNode{{ID: "H", Name: "hub"}, {ID: "a"}, {ID: "b"}, {ID: "c"}},
		Edges: []gEdge{{Type: "CALLS", From: "a", To: "H"}, {Type: "CALLS", From: "b", To: "H"}, {Type: "CALLS", From: "c", To: "H"}},
	}
	if s := hubNotes(g, []string{"H"}, 3); !strings.Contains(s, "Hub change") || !strings.Contains(s, "`hub`") || !strings.Contains(s, "**3**") {
		t.Fatalf("fan-in 3 at threshold 3 should flag the hub, got:\n%s", s)
	}
	if s := hubNotes(g, []string{"H"}, 4); s != "" {
		t.Fatalf("fan-in 3 below threshold 4 must not flag, got:\n%s", s)
	}
	if s := hubNotes(g, []string{"H"}, 0); s != "" {
		t.Fatalf("threshold 0 disables the alert, got:\n%s", s)
	}
}
