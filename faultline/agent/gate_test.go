package main

import (
	"strings"
	"testing"
)

func TestGateDecisionNotTriggered(t *testing.T) {
	// 1 untested, threshold 1 → not over → no gate, no advisory note.
	block, reason := gateDecision(1, 1, false, false, nil, "")
	if block || reason != "" {
		t.Fatalf("under threshold should not block: block=%v reason=%q", block, reason)
	}
}

func TestGateDecisionBlocks(t *testing.T) {
	// 3 untested, threshold 0, not draft, no override → block.
	block, reason := gateDecision(0, 3, false, false, nil, "")
	if !block || reason != "" {
		t.Fatalf("over threshold should block with no advisory: block=%v reason=%q", block, reason)
	}
}

func TestGateDecisionDisabled(t *testing.T) {
	// gateUntested < 0 means gating off entirely.
	if block, _ := gateDecision(-1, 99, false, false, nil, ""); block {
		t.Fatal("gate disabled (-1) must never block")
	}
}

func TestGateDecisionDraftIsAdvisory(t *testing.T) {
	block, reason := gateDecision(0, 3, false, true, nil, "")
	if block {
		t.Fatal("draft MR must never block")
	}
	if !strings.Contains(reason, "Draft") {
		t.Fatalf("draft advisory reason should mention Draft: %q", reason)
	}
}

func TestGateDecisionOverrideLabelIsAuditedAdvisory(t *testing.T) {
	block, reason := gateDecision(0, 3, false, false, []string{"bug", "faultline-override"}, "hotfix, owner approved")
	if block {
		t.Fatal("override label must turn the gate advisory")
	}
	if !strings.Contains(reason, "overridden") || !strings.Contains(reason, "hotfix, owner approved") {
		t.Fatalf("override reason must record the bypass and the reason: %q", reason)
	}
}

func TestGateDecisionOverrideWithoutReason(t *testing.T) {
	_, reason := gateDecision(0, 3, false, false, []string{"faultline-override"}, "")
	if !strings.Contains(reason, "no reason provided") {
		t.Fatalf("missing reason should be recorded explicitly: %q", reason)
	}
}

func TestGateDecisionUnrelatedLabelsStillBlock(t *testing.T) {
	if block, _ := gateDecision(0, 3, false, false, []string{"bug", "needs-review"}, ""); !block {
		t.Fatal("unrelated labels must not suppress the gate")
	}
}

func TestLabelsContainCaseInsensitive(t *testing.T) {
	if !labelsContain([]string{" Faultline-Override "}, overrideLabel) {
		t.Fatal("label match should be case-insensitive and trimmed")
	}
	if labelsContain([]string{"faultline-override-2"}, overrideLabel) {
		t.Fatal("must match the whole label, not a prefix")
	}
}
