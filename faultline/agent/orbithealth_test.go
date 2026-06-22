package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// mustGS builds an orbitGraphStatus fixture from real JSON so the schema tags are
// exercised alongside the assessment logic.
func mustGS(t *testing.T, js string) *orbitGraphStatus {
	t.Helper()
	var gs orbitGraphStatus
	if err := json.Unmarshal([]byte(js), &gs); err != nil {
		t.Fatalf("bad graph_status fixture: %v", err)
	}
	return &gs
}

func mustStatus(t *testing.T, js string) *orbitStatusResp {
	t.Helper()
	var st orbitStatusResp
	if err := json.Unmarshal([]byte(js), &st); err != nil {
		t.Fatalf("bad status fixture: %v", err)
	}
	return &st
}

const gsHealthy = `{"projects":{"indexed":1,"total_known":1},"domains":[{"name":"ci","items":[{"name":"Job","count":38}]},{"name":"source_code","items":[{"name":"Definition","count":207},{"name":"File","count":45}]}]}`

func TestAssessIndexHealth_OK(t *testing.T) {
	if h := assessIndexHealth(mustGS(t, gsHealthy), nil); h.state != idxOK {
		t.Fatalf("want idxOK, got state=%d reason=%q", h.state, h.reason)
	}
}

func TestAssessIndexHealth_EmptyCodeIndexIsDegraded(t *testing.T) {
	gs := mustGS(t, `{"projects":{"indexed":1,"total_known":1},"domains":[{"name":"source_code","items":[{"name":"Definition","count":0}]}]}`)
	h := assessIndexHealth(gs, nil)
	if h.state != idxDegraded {
		t.Fatalf("empty code index must be degraded, got state=%d", h.state)
	}
	if !strings.Contains(h.reason, "0 code definitions") {
		t.Errorf("reason should explain the empty index: %q", h.reason)
	}
}

func TestAssessIndexHealth_PartialProjectsIsDegraded(t *testing.T) {
	gs := mustGS(t, `{"projects":{"indexed":1,"total_known":3},"domains":[{"name":"source_code","items":[{"name":"Definition","count":10}]}]}`)
	h := assessIndexHealth(gs, nil)
	if h.state != idxDegraded || !strings.Contains(h.reason, "1 of 3") {
		t.Fatalf("partial coverage must be degraded with 1 of 3, got state=%d reason=%q", h.state, h.reason)
	}
}

func TestAssessIndexHealth_MissingSourceCodeIsUnknown(t *testing.T) {
	// Schema drift (Beta): no source_code domain → don't block, flag unknown.
	gs := mustGS(t, `{"projects":{"indexed":1,"total_known":1},"domains":[{"name":"ci","items":[{"name":"Job","count":1}]}]}`)
	if h := assessIndexHealth(gs, nil); h.state != idxUnknown {
		t.Fatalf("missing source_code domain must be unknown, got state=%d reason=%q", h.state, h.reason)
	}
}

func TestAssessIndexHealth_NilGraphStatusIsUnknown(t *testing.T) {
	if h := assessIndexHealth(nil, nil); h.state != idxUnknown {
		t.Fatalf("nil graph_status must be unknown, got state=%d", h.state)
	}
}

func TestAssessIndexHealth_ClusterUnhealthyIsDegraded(t *testing.T) {
	h := assessIndexHealth(mustGS(t, gsHealthy), mustStatus(t, `{"status":"degraded"}`))
	if h.state != idxDegraded {
		t.Fatalf("unhealthy cluster must be degraded, got state=%d reason=%q", h.state, h.reason)
	}
}

func TestAssessIndexHealth_UserNotAvailableIsDegraded(t *testing.T) {
	h := assessIndexHealth(mustGS(t, gsHealthy), mustStatus(t, `{"status":"healthy","user":{"available":false}}`))
	if h.state != idxDegraded {
		t.Fatalf("user.available=false must be degraded, got state=%d reason=%q", h.state, h.reason)
	}
}

func TestAssessIndexHealth_HealthyStatusDoesNotFlipGoodIndex(t *testing.T) {
	// A healthy status + available user must NOT turn a populated index into degraded.
	h := assessIndexHealth(mustGS(t, gsHealthy), mustStatus(t, `{"status":"healthy","user":{"available":true}}`))
	if h.state != idxOK {
		t.Fatalf("healthy status over a good index must stay OK, got state=%d reason=%q", h.state, h.reason)
	}
}

// --- gate: can't-vouch fails closed only when gating is on, and the same escapes apply ---

func TestGateDecision_CantVouchBlocksWhenGatingOn(t *testing.T) {
	block, reason := gateDecision(0, 0, true, false, nil, "")
	if !block {
		t.Fatal("can't-vouch with gating on must block (fail closed)")
	}
	if reason != "" {
		t.Errorf("a hard block carries no advisory reason, got %q", reason)
	}
}

func TestGateDecision_CantVouchAdvisoryWhenGatingOff(t *testing.T) {
	if block, _ := gateDecision(-1, 0, true, false, nil, ""); block {
		t.Fatal("can't-vouch with gating OFF must stay advisory (never blocks first adoption)")
	}
}

func TestGateDecision_CantVouchSuppressedByDraft(t *testing.T) {
	block, reason := gateDecision(0, 0, true, true, nil, "")
	if block {
		t.Fatal("a draft MR must suppress the can't-vouch block")
	}
	if !strings.Contains(reason, "Draft") {
		t.Errorf("reason should mark it draft-advisory: %q", reason)
	}
}

func TestGateDecision_CantVouchSuppressedByOverride(t *testing.T) {
	block, reason := gateDecision(0, 0, true, false, []string{"faultline-override"}, "indexing, will re-run")
	if block {
		t.Fatal("the override label must suppress the can't-vouch block")
	}
	if !strings.Contains(reason, "overridden") {
		t.Errorf("override must be recorded for audit: %q", reason)
	}
}

// --- render: a clean result over an untrustworthy index is never a green Pass ---

func TestRenderMarkdown_CantVouchCleanResultIsNotPass(t *testing.T) {
	md := renderMarkdown(report{ImpactedCount: 0}, []string{"helper"}, nil, false, false,
		"Orbit holds 0 code definitions for this project")
	if strings.Contains(md, "✅ Pass") {
		t.Fatalf("an empty radius over a degraded index must not Pass:\n%s", md)
	}
	if !strings.Contains(md, "Can't vouch") {
		t.Fatalf("want the can't-vouch badge:\n%s", md)
	}
	if !strings.Contains(md, "0 code definitions") {
		t.Errorf("the reason should be surfaced to the developer:\n%s", md)
	}
}

func TestRenderMarkdown_CantVouchBlockingBadge(t *testing.T) {
	md := renderMarkdown(report{ImpactedCount: 1, BlastRadius: []impacted{{Name: "X"}}}, nil, nil, true, false,
		"Orbit has indexed 1 of 3 known project(s); the call graph would be incomplete")
	if !strings.Contains(md, "⛔ Blocked — can't vouch") {
		t.Fatalf("gating on + can't-vouch must render a blocking badge:\n%s", md)
	}
}

func TestRenderMarkdown_CantVouchWithFindingsKeepsCaveatAndImpact(t *testing.T) {
	r := report{ImpactedCount: 1, BlastRadius: []impacted{{Name: "TotalWithTax", Distance: 1}}}
	md := renderMarkdown(r, []string{"CalculateTax"},
		[]impacted{{Name: "TotalWithTax", Distance: 1}}, false, false,
		"Orbit has indexed 1 of 2 known project(s)")
	if !strings.Contains(md, "Index caveat") {
		t.Fatalf("findings over a degraded index must carry an undercount caveat:\n%s", md)
	}
	if !strings.Contains(md, "TotalWithTax") {
		t.Errorf("real findings must still render:\n%s", md)
	}
	if strings.Contains(md, "✅ Pass") {
		t.Errorf("must not read as Pass:\n%s", md)
	}
}

func TestRenderMarkdown_VouchedEmptyResultStillPasses(t *testing.T) {
	md := renderMarkdown(report{ImpactedCount: 0}, []string{"helper"}, nil, false, true, "")
	if !strings.Contains(md, "✅ Pass") {
		t.Fatalf("a trusted empty result must still Pass:\n%s", md)
	}
}
