package main

import (
	"fmt"
	"strings"
)

// overrideLabel is the MR label that turns Faultline's gate into an audited,
// non-blocking advisory — an explicit, reviewable bypass (not a silent skip).
const overrideLabel = "faultline-override"

// gateDecision applies the deterministic gate plus two adoption-comfort escapes.
// The raw gate triggers when EITHER more untested-impacted definitions are found than
// the threshold allows, OR Orbit's index can't be vouched for (cantVouch) while gating
// is on — failing closed rather than passing a result computed over an absent/partial
// graph. It is suppressed (advisory, never blocks the merge) when:
//   - the MR is a DRAFT — work in progress should never be blocked; the gate will
//     enforce once the MR is marked Ready; or
//   - the MR carries the faultline-override label — an explicit, auditable bypass.
//
// cantVouch only ever blocks when gating is enabled (gateUntested >= 0); with gating
// off it stays advisory, so dropping the include into an unindexed repo never blocks.
//
// Returns whether the pipeline should block, and a human-readable reason to record
// in the verdict when the gate triggered but was suppressed (empty otherwise). The
// reason is posted into the MR note, so an override leaves a permanent audit trail.
func gateDecision(gateUntested, untestedCount int, cantVouch bool, draft bool, labels []string, overrideReason string) (block bool, advisoryReason string) {
	gatingOn := gateUntested >= 0
	triggered := gatingOn && (untestedCount > gateUntested || cantVouch)
	if !triggered {
		return false, ""
	}
	if draft {
		return false, "🚧 **Draft MR — advisory only.** Faultline will block this merge once the MR is marked Ready."
	}
	if labelsContain(labels, overrideLabel) {
		r := strings.TrimSpace(overrideReason)
		if r == "" {
			r = "no reason provided"
		}
		return false, fmt.Sprintf(
			"🔓 **Gate overridden** via the `%s` label — merge allowed despite the untested blast radius. Reason: %s _(recorded here for audit)._",
			overrideLabel, r)
	}
	return true, ""
}

// labelsContain reports whether want is present in labels (case-insensitive, trimmed).
func labelsContain(labels []string, want string) bool {
	for _, l := range labels {
		if strings.EqualFold(strings.TrimSpace(l), want) {
			return true
		}
	}
	return false
}
