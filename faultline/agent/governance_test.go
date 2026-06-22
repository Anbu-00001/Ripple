package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestVerdictIncludesGovernanceAndClosedLoop assembles the verdict exactly as
// main() does — real engine, then ownershipReach + duoHandoff — and asserts the
// rendered Markdown carries the Code-Owners-beyond-the-diff section and the Duo
// closed-loop hand-off. This is the end-to-end scrutiny of the actual output,
// not just the unit pieces. Skips if the engine binary is not built.
func TestVerdictIncludesGovernanceAndClosedLoop(t *testing.T) {
	engine := findEngineBinary()
	if engine == "" {
		t.Skip("faultline-engine not built; unit tests cover the pieces")
	}
	// api -> service -> helper. Change helper; service+api are the untested radius.
	g := graph{
		Nodes: []gNode{
			{ID: "h", Name: "helper", FilePath: "helper.go", DefinitionType: "Function"},
			{ID: "s", Name: "service", FilePath: "service.go", DefinitionType: "Function"},
			{ID: "a", Name: "api", FilePath: "api.go", DefinitionType: "Function"},
		},
		Edges: []gEdge{{Type: "CALLS", From: "s", To: "h"}, {Type: "CALLS", From: "a", To: "s"}},
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "CODEOWNERS"), []byte(
		"/helper.go @core-team\n/service.go @service-team\n/api.go @api-team\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	graphPath := filepath.Join(root, "graph.json")
	data, _ := json.MarshalIndent(g, "", "  ")
	if err := os.WriteFile(graphPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(engine, "--graph", graphPath, "--changed", "h", "--tested", "").Output()
	if err != nil {
		t.Fatalf("engine failed: %v", err)
	}
	var rep report
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("bad engine JSON: %v", err)
	}

	untested := untestedImpact(rep.BlastRadius, "") // empty corpus => all impacted untested
	md := renderMarkdown(rep, []string{"helper"}, untested, false, true, "")
	md += ownershipReach(root, rep.BlastRadius, map[string]bool{"helper.go": true})
	md += duoHandoff(rep.MinimumTestSet)

	for _, want := range []string{
		"Code owners beyond the diff", "@service-team", "@api-team", "service.go", "api.go",
		"Close the loop with GitLab Duo",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("assembled verdict missing %q:\n%s", want, md)
		}
	}
	// @core-team owns the changed file — it must NOT be flagged as beyond the diff.
	if strings.Contains(md, "@core-team") {
		t.Fatalf("@core-team owns the diff; should not appear in ownership escape:\n%s", md)
	}
	// The hand-off names a real minimum-test-set symbol.
	if len(rep.MinimumTestSet) > 0 && !strings.Contains(md, "`"+rep.MinimumTestSet[0].Name+"`") {
		t.Fatalf("closed-loop hand-off should name a min-test-set symbol:\n%s", md)
	}
}
