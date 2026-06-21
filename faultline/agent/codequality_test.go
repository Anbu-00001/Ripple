package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var cqAllowedSeverity = map[string]bool{
	"info": true, "minor": true, "major": true, "critical": true, "blocker": true,
}

func cqSampleInputs() ([]impacted, []cutNode, []coverageRank, map[string]int) {
	untested := []impacted{
		{ID: "a", Name: "applyRate", FilePath: "./calc/tax.go", Distance: 1},
		{ID: "b", Name: "TotalWithTax", FilePath: "calc/order.go", Distance: 2},
	}
	minTestSet := []cutNode{{ID: "a", Name: "applyRate", FilePath: "calc/tax.go"}}
	coverage := []coverageRank{{ID: "a", Covers: 3}, {ID: "b", Covers: 1}}
	lineByID := map[string]int{"a": 10}
	return untested, minTestSet, coverage, lineByID
}

// The report must match GitLab's verified Code Quality schema: a single JSON
// array of findings, each with description, check_name, fingerprint, a severity
// from the allowed set, and a location with a relative path (no "./") + integer
// begin line. A schema slip means GitLab silently drops the report.
func TestBuildCodeQualityValidSchema(t *testing.T) {
	untested, cut, cov, lines := cqSampleInputs()
	out, err := buildCodeQuality(untested, cut, cov, false, lines)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var findings []map[string]any
	if err := json.Unmarshal(out, &findings); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, out)
	}
	if len(findings) != 2 {
		t.Fatalf("want 2 findings, got %d", len(findings))
	}
	for i, f := range findings {
		for _, k := range []string{"description", "check_name", "fingerprint", "severity", "location"} {
			if _, ok := f[k]; !ok {
				t.Errorf("finding %d missing required key %q", i, k)
			}
		}
		if !cqAllowedSeverity[f["severity"].(string)] {
			t.Errorf("finding %d severity %q not in allowed set", i, f["severity"])
		}
		loc := f["location"].(map[string]any)
		path := loc["path"].(string)
		if strings.HasPrefix(path, "./") {
			t.Errorf("finding %d path %q must not start with ./", i, path)
		}
		begin := loc["lines"].(map[string]any)["begin"].(float64)
		if begin < 1 {
			t.Errorf("finding %d begin line %v must be >= 1", i, begin)
		}
		if f["check_name"].(string) != cqCheckName {
			t.Errorf("finding %d check_name %q", i, f["check_name"])
		}
		if f["fingerprint"].(string) == "" {
			t.Errorf("finding %d empty fingerprint", i)
		}
	}
}

func TestBuildCodeQualityDeterministicAndSorted(t *testing.T) {
	untested, cut, cov, lines := cqSampleInputs()
	a, _ := buildCodeQuality(untested, cut, cov, false, lines)
	b, _ := buildCodeQuality(untested, cut, cov, false, lines)
	if !bytes.Equal(a, b) {
		t.Fatal("output is not deterministic")
	}
	// Sorted by path: calc/order.go (b) before calc/tax.go (a).
	var findings []cqFinding
	if err := json.Unmarshal(a, &findings); err != nil {
		t.Fatal(err)
	}
	if findings[0].Location.Path != "calc/order.go" || findings[1].Location.Path != "calc/tax.go" {
		t.Fatalf("findings not sorted by path: %q then %q",
			findings[0].Location.Path, findings[1].Location.Path)
	}
}

func TestBuildCodeQualityEmptyIsArrayNotNull(t *testing.T) {
	out, err := buildCodeQuality(nil, nil, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "[]" {
		t.Fatalf("empty report must be [] (a valid array), got %q", out)
	}
}

// Severity is derived from the algorithm (min-test-set membership) and the gate
// mode — not a magic threshold. Recommended test points outrank merely-impacted
// nodes, and `major` is reserved for when gating is actually on.
func TestBuildCodeQualitySeverityFromAlgorithm(t *testing.T) {
	untested, cut, cov, lines := cqSampleInputs()

	// advisory (non-blocking): recommended -> minor, other -> info
	out, _ := buildCodeQuality(untested, cut, cov, false, lines)
	var adv []cqFinding
	json.Unmarshal(out, &adv)
	sev := map[string]string{}
	for _, f := range adv {
		// path uniquely identifies our two sample findings
		sev[f.Location.Path] = f.Severity
	}
	if sev["calc/tax.go"] != "minor" {
		t.Errorf("recommended test point should be minor when advisory, got %q", sev["calc/tax.go"])
	}
	if sev["calc/order.go"] != "info" {
		t.Errorf("merely-impacted node should be info when advisory, got %q", sev["calc/order.go"])
	}

	// gating (blocking): recommended escalates to major
	out2, _ := buildCodeQuality(untested, cut, cov, true, lines)
	var gated []cqFinding
	json.Unmarshal(out2, &gated)
	for _, f := range gated {
		if f.Location.Path == "calc/tax.go" && f.Severity != "major" {
			t.Errorf("recommended test point should be major when gating, got %q", f.Severity)
		}
	}
}

func TestBuildCodeQualityLineFallbackAndRelPath(t *testing.T) {
	untested, cut, cov, lines := cqSampleInputs()
	out, _ := buildCodeQuality(untested, cut, cov, false, lines)
	var findings []cqFinding
	json.Unmarshal(out, &findings)
	byPath := map[string]cqFinding{}
	for _, f := range findings {
		byPath[f.Location.Path] = f
	}
	if byPath["calc/tax.go"].Location.Lines.Begin != 10 {
		t.Errorf("node a should use Orbit start_line 10, got %d", byPath["calc/tax.go"].Location.Lines.Begin)
	}
	if byPath["calc/order.go"].Location.Lines.Begin != 1 {
		t.Errorf("node b without line data should fall back to line 1, got %d", byPath["calc/order.go"].Location.Lines.Begin)
	}
	// "./calc/tax.go" input must be normalized to "calc/tax.go".
	if _, ok := byPath["calc/tax.go"]; !ok {
		t.Errorf("leading ./ not stripped from path")
	}
}

func TestCqFingerprintStableAndUnique(t *testing.T) {
	if cqFingerprint("a") != cqFingerprint("a") {
		t.Error("fingerprint must be stable for the same id (no re-nagging across runs)")
	}
	if cqFingerprint("a") == cqFingerprint("b") {
		t.Error("fingerprint must differ across ids")
	}
}

// Integration: run the REAL faultline-engine, then feed its actual output
// (minimum_test_set + coverage_ranking) into the Code Quality report — proving
// the engine's findings flow into a schema-valid report, not just the unit pieces.
// Skips if the engine binary is not built.
func TestCodeQualityEndToEndThroughEngine(t *testing.T) {
	engine := findEngineBinary()
	if engine == "" {
		t.Skip("faultline-engine not built; unit tests cover buildCodeQuality")
	}
	// api -> service -> helper. Change helper; service + api are the untested radius,
	// and `service` is the single recommended test point (frontier / min-cut member).
	g := graph{
		Nodes: []gNode{
			{ID: "h", Name: "helper", FilePath: "helper.go", DefinitionType: "Function"},
			{ID: "s", Name: "service", FilePath: "service.go", DefinitionType: "Function"},
			{ID: "a", Name: "api", FilePath: "api.go", DefinitionType: "Function"},
		},
		Edges: []gEdge{{Type: "CALLS", From: "s", To: "h"}, {Type: "CALLS", From: "a", To: "s"}},
	}
	graphPath := filepath.Join(t.TempDir(), "graph.json")
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
	lineByID := map[string]int{"s": 12}             // pretend Orbit gave service's start line
	cq, err := buildCodeQuality(untested, rep.MinimumTestSet, rep.CoverageRanking, false, lineByID)
	if err != nil {
		t.Fatalf("buildCodeQuality: %v", err)
	}
	var findings []cqFinding
	if err := json.Unmarshal(cq, &findings); err != nil {
		t.Fatalf("report not a valid array: %v\n%s", err, cq)
	}
	if len(findings) != 2 {
		t.Fatalf("want 2 findings (service, api), got %d: %s", len(findings), cq)
	}
	byPath := map[string]cqFinding{}
	for _, f := range findings {
		byPath[f.Location.Path] = f
		if !cqAllowedSeverity[f.Severity] {
			t.Errorf("invalid severity %q", f.Severity)
		}
	}
	// `service` is the min-cut member ⇒ recommended ⇒ minor (advisory), at line 12,
	// and its description cites the real dominance count (it covers service + api = 2).
	svc, ok := byPath["service.go"]
	if !ok {
		t.Fatalf("no finding for service.go: %s", cq)
	}
	if svc.Severity != "minor" {
		t.Errorf("service is the recommended test point; want minor, got %q", svc.Severity)
	}
	if svc.Location.Lines.Begin != 12 {
		t.Errorf("service should use the supplied start line 12, got %d", svc.Location.Lines.Begin)
	}
	if !strings.Contains(svc.Description, "covers 2 untested") {
		t.Errorf("service description should cite dominance count 2, got %q", svc.Description)
	}
	// `api` is merely impacted (covered by testing service) ⇒ info, line 1 (no data).
	api, ok := byPath["api.go"]
	if !ok {
		t.Fatalf("no finding for api.go: %s", cq)
	}
	if api.Severity != "info" {
		t.Errorf("api is merely impacted; want info, got %q", api.Severity)
	}
	if api.Location.Lines.Begin != 1 {
		t.Errorf("api has no line data; want fallback line 1, got %d", api.Location.Lines.Begin)
	}
}

// The recommended-test-point description should surface the real dominance count
// (how many untested functions one test there covers) — real data, not invented.
func TestBuildCodeQualityDescriptionUsesCoverage(t *testing.T) {
	untested, cut, cov, lines := cqSampleInputs()
	out, _ := buildCodeQuality(untested, cut, cov, false, lines)
	var findings []cqFinding
	json.Unmarshal(out, &findings)
	var taxDesc string
	for _, f := range findings {
		if f.Location.Path == "calc/tax.go" {
			taxDesc = f.Description
		}
	}
	if !strings.Contains(taxDesc, "covers 3 untested") {
		t.Errorf("recommended point description should cite the dominance count, got %q", taxDesc)
	}
}
