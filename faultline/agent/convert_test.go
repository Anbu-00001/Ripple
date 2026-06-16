package main

import (
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
		if n.ID == "D" && n.DefinitionType != "Function" {
			t.Fatalf("missing definition_type should default to Function, got %q", n.DefinitionType)
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
	md := renderMarkdown(report{ImpactedCount: 0}, []string{"ApplyDiscount"}, nil)
	if !strings.Contains(md, "Empty blast radius") {
		t.Fatalf("empty radius should be called out, got:\n%s", md)
	}
	if strings.Contains(md, "| Impacted definition |") {
		t.Fatalf("empty radius must not render a table, got:\n%s", md)
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
	md := renderMarkdown(r, []string{"applyRate"}, nil)
	if !strings.Contains(md, "2 definition(s) transitively affected") {
		t.Fatalf("missing impact count, got:\n%s", md)
	}
	if !strings.Contains(md, "beyond Orbit's 3-hop query cap") {
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
	md := renderMarkdown(r, []string{"changed | name"}, nil)
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
	yes := []string{"calc/tax_test.go", "x/test_foo.py", "a/foo.test.ts", "b/Foo.spec.js", "m/BarTest.java", "pkg/__tests__/x.js"}
	no := []string{"calc/tax.go", "main.go", "README.md", "src/foo.ts"}
	for _, f := range yes {
		if !isTestFile(f) {
			t.Fatalf("%q should be a test file", f)
		}
	}
	for _, f := range no {
		if isTestFile(f) {
			t.Fatalf("%q should NOT be a test file", f)
		}
	}
}

func TestRenderMarkdownShowsUntestedSection(t *testing.T) {
	r := report{ImpactedCount: 1, MaxDepth: 2, BlastRadius: []impacted{{Name: "TotalWithTax", FilePath: "calc/order.go", Distance: 1}}}
	md := renderMarkdown(r, []string{"CalculateTax"}, []impacted{{Name: "TotalWithTax", FilePath: "calc/order.go", Distance: 1}})
	if !strings.Contains(md, "Untested blast radius") {
		t.Fatalf("untested section missing:\n%s", md)
	}
	if !strings.Contains(md, "`TotalWithTax`") {
		t.Fatalf("untested symbol not listed:\n%s", md)
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

	for _, want := range []string{"```mermaid", "graph TD", "[\"standardRate\"]", "[\"Rate\"]", "[\"levyBase\"]"} {
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
		{FromID: "A", ToID: "B", Type: "CALLS"}, // keep
		{FromID: "", ToID: "B", Type: "CALLS"},  // drop: empty from (partial Orbit response)
		{FromID: "A", ToID: "", Type: "CALLS"},  // drop: empty to
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
	md := renderMarkdown(r, nil, nil)
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
