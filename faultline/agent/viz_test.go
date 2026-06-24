package main

import (
	"math"
	"strings"
	"testing"
)

func vizSampleGraph() (graph, report, []impacted) {
	g := graph{
		Nodes: []gNode{
			{ID: "C", Name: "standardRate", FilePath: "calc/tax.go"},
			{ID: "R", Name: "Rate", FilePath: "calc/tax.go"},
			{ID: "L", Name: "levyBase", FilePath: "calc/pipeline.go"},
		},
		Edges: []gEdge{
			{Type: "CALLS", From: "R", To: "C"}, // Rate calls standardRate
			{Type: "CALLS", From: "L", To: "R"}, // levyBase calls Rate
		},
	}
	rep := report{
		ImpactedCount: 2,
		BlastRadius:   []impacted{{ID: "R", Name: "Rate", Distance: 1}, {ID: "L", Name: "levyBase", Distance: 2}},
	}
	untested := []impacted{{ID: "L", Name: "levyBase", Distance: 2}}
	return g, rep, untested
}

func TestBuildInteractiveHTMLStructure(t *testing.T) {
	g, rep, untested := vizSampleGraph()
	html := buildInteractiveHTML(g, []string{"C"}, rep, untested)

	for _, want := range []string{
		"<!DOCTYPE html>", `id="fl-data"`, `viewBox="0 0 960 640"`,
		"standardRate", "Rate", "levyBase",
		`"type":"changed"`, `"type":"untested"`, `"type":"tested"`,
		`"nodes":`, `"edges":`, `"meta":`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("interactive HTML missing %q", want)
		}
	}
	// the changed seed C must be present in the data with its label.
	if !strings.Contains(html, `"id":"C","label":"standardRate"`) {
		t.Fatalf("changed node not present in data block")
	}
}

func TestBuildInteractiveHTMLEmptyRadius(t *testing.T) {
	g, _, _ := vizSampleGraph()
	if got := buildInteractiveHTML(g, []string{"C"}, report{ImpactedCount: 0}, nil); got != "" {
		t.Fatalf("empty blast radius must produce no HTML, got %d bytes", len(got))
	}
}

func TestBuildInteractiveHTMLEscapesInjection(t *testing.T) {
	// Attacker-controlled symbol name must not break out of the <script> data block.
	evil := "</script><img src=x onerror=alert(1)>"
	g := graph{
		Nodes: []gNode{{ID: "C", Name: "c"}, {ID: "R", Name: evil, FilePath: "x.go"}},
		Edges: []gEdge{{Type: "CALLS", From: "R", To: "C"}},
	}
	rep := report{ImpactedCount: 1, BlastRadius: []impacted{{ID: "R", Name: evil, Distance: 1}}}
	html := buildInteractiveHTML(g, []string{"C"}, rep, nil)

	if strings.Contains(html, "</script><img") {
		t.Fatalf("raw script break-out leaked into the page:\n%s", html)
	}
	if !strings.Contains(html, `</script>`) {
		t.Fatalf("the dangerous name should be unicode-escaped in the JSON data block")
	}
}

func TestBuildInteractiveHTMLDeterministic(t *testing.T) {
	g, rep, untested := vizSampleGraph()
	a := buildInteractiveHTML(g, []string{"C"}, rep, untested)

	// Reverse node and edge order: a sorted/canonical build must be byte-identical.
	rg := graph{
		Nodes: []gNode{g.Nodes[2], g.Nodes[1], g.Nodes[0]},
		Edges: []gEdge{g.Edges[1], g.Edges[0]},
	}
	b := buildInteractiveHTML(rg, []string{"C"}, rep, untested)
	if a != b {
		t.Fatalf("interactive HTML is not deterministic under input permutation")
	}
}

func TestLayoutPositionsDeterministicAndBounded(t *testing.T) {
	edges := [][2]int{{0, 1}, {1, 2}, {0, 2}, {2, 3}}
	a := layoutPositions(4, edges)
	b := layoutPositions(4, edges)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("layout not deterministic at %d: %v vs %v", i, a[i], b[i])
		}
		x, y := a[i][0], a[i][1]
		if math.IsNaN(x) || math.IsInf(x, 0) || math.IsNaN(y) || math.IsInf(y, 0) {
			t.Fatalf("non-finite coordinate at %d: %v", i, a[i])
		}
		if x < vizPad-0.5 || x > vizW-vizPad+0.5 || y < vizPad-0.5 || y > vizH-vizPad+0.5 {
			t.Fatalf("coordinate %v out of frame bounds", a[i])
		}
	}
	// edge cases
	if len(layoutPositions(0, nil)) != 0 {
		t.Fatalf("n=0 should yield no positions")
	}
	if one := layoutPositions(1, nil); one[0][0] != vizW/2 || one[0][1] != vizH/2 {
		t.Fatalf("single node should be centered, got %v", one[0])
	}
}
