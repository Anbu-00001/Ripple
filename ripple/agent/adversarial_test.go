package main

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// normalize() adversarial coverage
// ---------------------------------------------------------------------------

func TestNormalizeEmptyAndMalformed(t *testing.T) {
	// Completely empty responses must yield an empty (non-panicking) graph.
	g := normalize(orbitResp{}, orbitResp{})
	if len(g.Nodes) != 0 || len(g.Edges) != 0 {
		t.Fatalf("empty input -> empty graph, got %d nodes / %d edges", len(g.Nodes), len(g.Edges))
	}

	// Nodes present but zero edges.
	var defs orbitResp
	defs.Result.Nodes = []orbitNode{{ID: "A", Name: "a", FilePath: "f"}}
	g = normalize(defs, orbitResp{})
	if len(g.Nodes) != 1 || len(g.Edges) != 0 {
		t.Fatalf("nodes-only: want 1 node / 0 edges, got %d / %d", len(g.Nodes), len(g.Edges))
	}
}

func TestNormalizeDuplicateIDsAcrossDefsAndCalls(t *testing.T) {
	// Same ID appears in defs and again (twice) in calls. First-writer-wins:
	// the defs version (with the richer fields) must survive.
	var defs, calls orbitResp
	defs.Result.Nodes = []orbitNode{
		{ID: "A", Name: "canonical", FilePath: "real.go", DefinitionType: "Method"},
	}
	calls.Result.Nodes = []orbitNode{
		{ID: "A", Name: "stale1", FilePath: "wrong1.go"},
		{ID: "A", Name: "stale2", FilePath: "wrong2.go"},
	}
	g := normalize(defs, calls)
	if len(g.Nodes) != 1 {
		t.Fatalf("dup ID across defs+calls must collapse to 1 node, got %d", len(g.Nodes))
	}
	n := g.Nodes[0]
	if n.Name != "canonical" || n.FilePath != "real.go" || n.DefinitionType != "Method" {
		t.Fatalf("first-writer-wins violated; defs entry should win, got %+v", n)
	}
}

func TestNormalizeNonCallsEdgesIgnored(t *testing.T) {
	var calls orbitResp
	calls.Result.Nodes = []orbitNode{{ID: "A"}, {ID: "B"}}
	calls.Result.Edges = []orbitEdge{
		{FromID: "A", ToID: "B", Type: "CALLS"},
		{FromID: "A", ToID: "B", Type: "IMPORTS"},
		{FromID: "A", ToID: "B", Type: "REFERENCES"},
		{FromID: "A", ToID: "B", Type: "calls"}, // case-sensitive: not "CALLS"
		{FromID: "A", ToID: "B", Type: ""},       // empty type: ignored by agent normalize
	}
	g := normalize(orbitResp{}, calls)
	if len(g.Edges) != 1 {
		t.Fatalf("only the exact-case CALLS edge should survive, got %d edges: %+v", len(g.Edges), g.Edges)
	}
	if g.Edges[0].Type != "CALLS" {
		t.Fatalf("surviving edge must be CALLS, got %q", g.Edges[0].Type)
	}
}

func TestNormalizeEdgesReferencingAbsentNodes(t *testing.T) {
	// Edges may reference IDs that are not present in any node set. normalize
	// does NOT prune these (it trusts the engine to ignore dangling endpoints).
	// This test documents that behavior so a future change is a conscious one.
	var calls orbitResp
	calls.Result.Nodes = []orbitNode{{ID: "A"}}
	calls.Result.Edges = []orbitEdge{
		{FromID: "A", ToID: "GHOST", Type: "CALLS"},
		{FromID: "PHANTOM", ToID: "A", Type: "CALLS"},
	}
	g := normalize(orbitResp{}, calls)
	if len(g.Nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(g.Nodes))
	}
	if len(g.Edges) != 2 {
		t.Fatalf("normalize keeps dangling edges (engine prunes); want 2, got %d", len(g.Edges))
	}
}

func TestNormalizeEmptyDefinitionTypeDefaultsFunction(t *testing.T) {
	var defs orbitResp
	defs.Result.Nodes = []orbitNode{
		{ID: "A", DefinitionType: ""},
		{ID: "B", DefinitionType: "Class"},
		{ID: "C"}, // omitted entirely
	}
	g := normalize(defs, orbitResp{})
	got := map[string]string{}
	for _, n := range g.Nodes {
		got[n.ID] = n.DefinitionType
	}
	if got["A"] != "Function" {
		t.Fatalf("empty definition_type must default to Function, got %q", got["A"])
	}
	if got["B"] != "Class" {
		t.Fatalf("explicit definition_type must be preserved, got %q", got["B"])
	}
	if got["C"] != "Function" {
		t.Fatalf("omitted definition_type must default to Function, got %q", got["C"])
	}
}

func TestNormalizeUnicodeAndHugeNames(t *testing.T) {
	huge := strings.Repeat("名", 5000) // 5000 multibyte runes
	var defs orbitResp
	defs.Result.Nodes = []orbitNode{
		{ID: "U", Name: "calcΔrate_税金_🌊", FilePath: "src/файл.go"},
		{ID: "H", Name: huge, FilePath: "big.go"},
		{ID: "", Name: "emptyID"}, // empty ID is a valid (if degenerate) map key
	}
	g := normalize(defs, orbitResp{})
	if len(g.Nodes) != 3 {
		t.Fatalf("unicode/huge/empty-id nodes: want 3, got %d", len(g.Nodes))
	}
	byID := map[string]gNode{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	if byID["U"].Name != "calcΔrate_税金_🌊" {
		t.Fatalf("unicode name corrupted: %q", byID["U"].Name)
	}
	if len([]rune(byID["H"].Name)) != 5000 {
		t.Fatalf("huge name truncated: %d runes", len([]rune(byID["H"].Name)))
	}
}

func TestNormalizeEmptyStringIDCollapses(t *testing.T) {
	// Multiple nodes with empty ID collapse to one (map key ""). This is a
	// latent footgun: any unindexed-id node silently overwrites the prior.
	var defs orbitResp
	defs.Result.Nodes = []orbitNode{
		{ID: "", Name: "first"},
		{ID: "", Name: "second"},
	}
	g := normalize(defs, orbitResp{})
	if len(g.Nodes) != 1 {
		t.Fatalf("empty-ID nodes collapse to 1 (first wins), got %d", len(g.Nodes))
	}
	if g.Nodes[0].Name != "first" {
		t.Fatalf("first-writer-wins for empty ID: want 'first', got %q", g.Nodes[0].Name)
	}
}

// ---------------------------------------------------------------------------
// resolveChanged() adversarial coverage
// ---------------------------------------------------------------------------

func TestResolveChangedEmptyInputs(t *testing.T) {
	g := graph{Nodes: []gNode{{ID: "A", FilePath: "f.go"}}}
	if got := resolveChanged(g, nil, nil); len(got) != 0 {
		t.Fatalf("both empty -> 0, got %v", got)
	}
	if got := resolveChanged(graph{}, nil, nil); got != nil && len(got) != 0 {
		t.Fatalf("empty graph + empty inputs -> 0, got %v", got)
	}
}

func TestResolveChangedUnionDedupOrder(t *testing.T) {
	g := graph{Nodes: []gNode{
		{ID: "A", FilePath: "tax.go"},
		{ID: "B", FilePath: "tax.go"},
		{ID: "C", FilePath: "order.go"},
	}}
	// A given explicitly AND lives in tax.go -> must appear exactly once,
	// and explicit IDs must come first (insertion order preserved).
	got := resolveChanged(g, []string{"A"}, []string{"tax.go"})
	if len(got) != 2 {
		t.Fatalf("union+dedup: want 2 (A,B), got %d (%v)", len(got), got)
	}
	if got[0] != "A" {
		t.Fatalf("explicit IDs should be emitted first; got %v", got)
	}
	seen := map[string]int{}
	for _, id := range got {
		seen[id]++
	}
	if seen["A"] != 1 {
		t.Fatalf("A duplicated across id+file paths; got %v", got)
	}
}

func TestResolveChangedFileWithNoDefs(t *testing.T) {
	g := graph{Nodes: []gNode{{ID: "A", FilePath: "tax.go"}}}
	// A file that exists in the changeset but has zero indexed definitions.
	if got := resolveChanged(g, nil, []string{"README.md", "Makefile"}); len(got) != 0 {
		t.Fatalf("files with no defs -> 0, got %v", got)
	}
}

func TestResolveChangedEmptyFilePathNodesNotMatchedByEmptyWant(t *testing.T) {
	// Nodes with empty file_path must NOT be swept in unless an empty path is
	// explicitly requested. splitNonEmpty drops blanks, so this is the real
	// pipeline path; here we exercise resolveChanged directly.
	g := graph{Nodes: []gNode{
		{ID: "A", FilePath: ""},
		{ID: "B", FilePath: "real.go"},
	}}
	if got := resolveChanged(g, nil, []string{"real.go"}); len(got) != 1 || got[0] != "B" {
		t.Fatalf("empty-path node must not leak; want [B], got %v", got)
	}
	// But an explicit empty path in changedFiles WOULD match empty-path nodes.
	if got := resolveChanged(g, nil, []string{""}); len(got) != 1 || got[0] != "A" {
		t.Fatalf("explicit empty path matches empty-path node; want [A], got %v", got)
	}
}

func TestResolveChangedIgnoresEmptyExplicitID(t *testing.T) {
	g := graph{}
	if got := resolveChanged(g, []string{"", "", "X"}, nil); len(got) != 1 || got[0] != "X" {
		t.Fatalf("empty explicit IDs dropped by add(); want [X], got %v", got)
	}
}

// ---------------------------------------------------------------------------
// renderMarkdown() adversarial coverage
// ---------------------------------------------------------------------------

func TestRenderMarkdownDepthExactlyThreeNoCapClaim(t *testing.T) {
	r := report{
		ImpactedCount: 1,
		MaxDepth:      3,
		BlastRadius:   []impacted{{Name: "X", FilePath: "f", Distance: 3}},
	}
	md := renderMarkdown(r, nil)
	if strings.Contains(md, "3-hop") {
		t.Fatalf("depth==3 is WITHIN Orbit's cap; must NOT claim to beat it. got:\n%s", md)
	}
}

func TestRenderMarkdownDepthFourFlagsCap(t *testing.T) {
	r := report{
		ImpactedCount: 1,
		MaxDepth:      4,
		BlastRadius:   []impacted{{Name: "X", FilePath: "f", Distance: 4}},
	}
	md := renderMarkdown(r, nil)
	if !strings.Contains(md, "beyond Orbit's 3-hop query cap") {
		t.Fatalf("depth==4 must flag the 3-hop cap. got:\n%s", md)
	}
}

// REAL PRODUCTION BUG: an impacted node with no name AND no file rendered as a
// raw numeric ID with an empty File cell. This test documents the *current*
// behavior and asserts the row is at least not silently dropped. See report:
// the open question is whether render should label it "(unresolved)".
func TestRenderMarkdownNamelessEmptyFileNode(t *testing.T) {
	r := report{
		ImpactedCount: 1,
		MaxDepth:      1,
		BlastRadius:   []impacted{{ID: "847392", Name: "", FilePath: "", Distance: 1}},
	}
	md := renderMarkdown(r, nil)
	// Current behavior: name falls back to ID, file cell is empty -> "| `847392` |  | 1 |"
	if !strings.Contains(md, "`847392`") {
		t.Fatalf("nameless node must fall back to its ID, not vanish. got:\n%s", md)
	}
	// Document the empty File cell (the production smell).
	if !strings.Contains(md, "| `847392` |  | 1 |") {
		t.Fatalf("expected empty File cell for unresolved node; got:\n%s", md)
	}
	// It should NOT (yet) be labeled "(unresolved)" — flip this to a positive
	// assertion once the hardening recommendation is adopted.
	if strings.Contains(md, "(unresolved)") {
		t.Fatalf("render now labels unresolved nodes — update this test & the report. got:\n%s", md)
	}
}

// MARKDOWN / TABLE INJECTION: names containing pipes, backticks, and newlines.
// This asserts the CURRENT (vulnerable) behavior so the report can quantify the
// risk; the fix is to escape these. If a future change escapes them, these
// assertions will fail loudly and must be updated.
func TestRenderMarkdownPipeInjectionBreaksTable(t *testing.T) {
	r := report{
		ImpactedCount: 1,
		MaxDepth:      1,
		BlastRadius: []impacted{
			{Name: "evil | Caller distance | 9999", FilePath: "f.go", Distance: 1},
		},
	}
	md := renderMarkdown(r, nil)
	// A raw pipe injects extra columns -> the rendered row has MORE pipes than a
	// clean 3-column row. Demonstrate the table is corruptible.
	row := lineContaining(md, "evil")
	if row == "" {
		t.Fatalf("injected row missing entirely. got:\n%s", md)
	}
	if strings.Count(row, "|") <= 4 {
		t.Fatalf("expected pipe injection to add columns (vulnerable); row=%q", row)
	}
	// Confirm the agent does NOT currently escape pipes.
	if strings.Contains(row, `\|`) {
		t.Fatalf("agent now escapes pipes — update this test & the report. row=%q", row)
	}
}

func TestRenderMarkdownNewlineInNameBreaksRow(t *testing.T) {
	r := report{
		ImpactedCount: 1,
		MaxDepth:      1,
		BlastRadius: []impacted{
			{Name: "line1\n## Injected heading\nline2", FilePath: "f.go", Distance: 1},
		},
	}
	md := renderMarkdown(r, nil)
	// A newline inside the name splits one logical row across multiple physical
	// lines, and "## Injected heading" escapes the table entirely.
	if !strings.Contains(md, "\n## Injected heading\n") {
		t.Fatalf("expected raw newline+heading injection to leak (vulnerable). got:\n%s", md)
	}
}

func TestRenderMarkdownBacktickInChangedNameBreaksCode(t *testing.T) {
	// Changed names are wrapped in `backticks`; a backtick inside the name
	// closes the code span prematurely.
	md := renderMarkdown(report{ImpactedCount: 0}, []string{"weird`name"})
	if !strings.Contains(md, "`weird`name`") {
		t.Fatalf("expected unescaped backtick to leak into the code span. got:\n%s", md)
	}
}

// lineContaining returns the first physical line containing sub, or "".
func lineContaining(s, sub string) string {
	for _, ln := range strings.Split(s, "\n") {
		if strings.Contains(ln, sub) {
			return ln
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// splitNonEmpty() — feeds resolveChanged from the CLI flags
// ---------------------------------------------------------------------------

func TestSplitNonEmpty(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{",,,", nil},
		{"  ", nil},
		{"A", []string{"A"}},
		{" A , B ,, C ", []string{"A", "B", "C"}},
		{"a,,b", []string{"a", "b"}},
	}
	for _, c := range cases {
		got := splitNonEmpty(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("splitNonEmpty(%q) len: want %v, got %v", c.in, c.want, got)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("splitNonEmpty(%q)[%d]: want %q, got %q", c.in, i, c.want[i], got[i])
			}
		}
	}
}

// truncate() guards the error-message size on REST/MR failures.
func TestTruncate(t *testing.T) {
	if got := truncate("hello", 200); got != "hello" {
		t.Fatalf("short string unchanged; got %q", got)
	}
	if got := truncate("abcdef", 3); got != "abc" {
		t.Fatalf("truncate to 3; got %q", got)
	}
	// NOTE: truncate slices by BYTES, not runes — multibyte input can be cut
	// mid-rune. This asserts the (current) byte-slicing behavior.
	if got := truncate("Δ", 1); got == "Δ" {
		t.Fatalf("expected byte-truncation to split the 2-byte rune; got %q", got)
	}
}
