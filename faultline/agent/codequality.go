package main

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// GitLab Code Quality report emission.
//
// GitLab ingests a subset of the CodeClimate JSON spec. Verified against the
// GitLab docs (ci/testing/code_quality): the report file is a SINGLE JSON ARRAY;
// every finding needs `description`, `check_name`, `fingerprint`, `severity`
// (one of info|minor|major|critical|blocker) and a `location` with a
// repo-relative `path` (no leading "./") and an integer begin line. The findings
// render in the merge-request **Reports** tab on the **Free** tier (inline
// annotations in the Changes view are Ultimate-only), and Code Quality is
// **advisory** — it never blocks a merge on its own (Faultline's separate gate
// does that). The file must not begin with a byte-order mark; encoding/json never
// emits one, so writing these bytes verbatim is safe.

// cqLocation is the location object of a Code Quality finding.
type cqLocation struct {
	Path  string `json:"path"`
	Lines struct {
		Begin int `json:"begin"`
	} `json:"lines"`
}

// cqFinding is one entry in the Code Quality report array.
type cqFinding struct {
	Description string     `json:"description"`
	CheckName   string     `json:"check_name"`
	Fingerprint string     `json:"fingerprint"`
	Severity    string     `json:"severity"`
	Location    cqLocation `json:"location"`
}

const cqCheckName = "faultline/untested-impact"

// cqRelPath strips a leading "./" — GitLab requires a clean repo-relative path.
func cqRelPath(p string) string {
	return strings.TrimPrefix(p, "./")
}

// cqFingerprint is a STABLE per-symbol id (hash of the check + Orbit Definition
// id), so re-running on an unchanged gap reuses the same finding instead of
// nagging anew each pipeline — GitLab dedupes findings by fingerprint.
func cqFingerprint(id string) string {
	h := sha1.Sum([]byte("faultline:" + cqCheckName + ":" + id))
	return fmt.Sprintf("%x", h)
}

// cqSeverity is derived from the ALGORITHM, never a magic threshold:
//   - a recommended test point (a member of the provably-minimum test set) is the
//     actionable place to add a test, so it outranks a node that is merely impacted
//     (and would be covered by testing a recommended point);
//   - severities only escalate to `major` when gating is actually ON, honouring
//     "advisory by default — reserve the loud severities for opt-in blocking".
func cqSeverity(recommended, blocking bool) string {
	switch {
	case recommended && blocking:
		return "major"
	case recommended:
		return "minor"
	case blocking:
		return "minor"
	default:
		return "info"
	}
}

// buildCodeQuality turns the untested blast radius into a GitLab Code Quality
// report: one finding per untested impacted function. Deterministic — findings
// are sorted by (path, begin line, fingerprint). `lineByID` supplies exact lines
// when Orbit exposed them, else the finding is placed at line 1 (file-level).
// Returns a single JSON array (an empty `[]` when there are no gaps), ready to
// write verbatim as the `codequality` artifact.
func buildCodeQuality(untested []impacted, minTestSet []cutNode, coverage []coverageRank, blocking bool, lineByID map[string]int) ([]byte, error) {
	inCut := make(map[string]bool, len(minTestSet))
	for _, c := range minTestSet {
		inCut[c.ID] = true
	}
	coversByID := make(map[string]int, len(coverage))
	for _, c := range coverage {
		coversByID[c.ID] = c.Covers
	}

	findings := make([]cqFinding, 0, len(untested))
	for _, u := range untested {
		line := 1
		if l, ok := lineByID[u.ID]; ok && l > 0 {
			line = l
		}
		recommended := inCut[u.ID]

		var b strings.Builder
		name := u.Name
		if name == "" {
			name = u.ID
		}
		fmt.Fprintf(&b, "Faultline: %q is impacted by this change", name)
		if u.Distance > 0 {
			fmt.Fprintf(&b, " (%d call(s) away)", u.Distance)
		}
		b.WriteString(" and has no test on the path.")
		if recommended {
			if cov := coversByID[u.ID]; cov > 1 {
				fmt.Fprintf(&b, " Recommended test point — one test here covers %d untested impacted function(s).", cov)
			} else {
				b.WriteString(" Recommended test point.")
			}
		}

		var f cqFinding
		f.Description = b.String()
		f.CheckName = cqCheckName
		f.Fingerprint = cqFingerprint(u.ID)
		f.Severity = cqSeverity(recommended, blocking)
		f.Location.Path = cqRelPath(u.FilePath)
		f.Location.Lines.Begin = line
		findings = append(findings, f)
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Location.Path != findings[j].Location.Path {
			return findings[i].Location.Path < findings[j].Location.Path
		}
		if findings[i].Location.Lines.Begin != findings[j].Location.Lines.Begin {
			return findings[i].Location.Lines.Begin < findings[j].Location.Lines.Begin
		}
		return findings[i].Fingerprint < findings[j].Fingerprint
	})

	// MarshalIndent on a non-nil empty slice yields "[]" (never "null"), so the
	// artifact is always a valid Code Quality array even with zero findings.
	return json.MarshalIndent(findings, "", "  ")
}
