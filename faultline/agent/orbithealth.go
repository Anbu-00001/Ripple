package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Fail-closed on a stale, partial, or absent Orbit index.
//
// A merge gate must not show a green ✅ when the graph it reasoned over wasn't there.
// An empty blast radius over a HEALTHY index is a true negative (nothing calls the
// changed symbol) and Faultline keeps passing it; the SAME empty result over an
// absent/partial/still-indexing index is a false negative we must refuse to vouch for.
// This mirrors Orbit's own code indexer, which refuses to run stale cleanup on a
// degraded re-index rather than silently tombstoning good data
// (gitlab-org/orbit/knowledge-graph!1813).
//
// The signal is Orbit's own free (no query-quota) status surface:
//   - GET /api/v4/orbit/graph_status?project_id=N  → projects.{indexed,total_known}
//     and a per-domain count incl. source_code.Definition (the decisive check).
//   - GET /api/v4/orbit/status                      → cluster health (coarse check).
// Both shapes were verified against the live API. Orbit is Beta ("ontology may
// change"), so an unrecognized shape degrades to "unknown" (never a false block).

type idxState int

const (
	idxOK       idxState = iota // positive evidence the code index is usable
	idxDegraded                 // positive evidence it is absent/partial → fail closed
	idxUnknown                  // could not tell (fetch/parse failure, schema drift)
)

// idxHealth is the trust verdict for a project's Orbit code index, plus a
// human-readable reason surfaced in the MR verdict when it is not OK.
type idxHealth struct {
	state  idxState
	reason string
}

func (h idxHealth) degraded() bool { return h.state == idxDegraded }

// orbitStatusResp is the subset of GET /orbit/status we read. Extra fields are
// ignored. `user.available` is a pointer so an absent field is distinguishable from
// an explicit false (we only act on an explicit negative).
type orbitStatusResp struct {
	Status string `json:"status"`
	User   *struct {
		Available *bool `json:"available"`
	} `json:"user"`
}

// orbitGraphStatus is the subset of GET /orbit/graph_status we read.
type orbitGraphStatus struct {
	Projects struct {
		Indexed    int `json:"indexed"`
		TotalKnown int `json:"total_known"`
	} `json:"projects"`
	Domains []struct {
		Name  string `json:"name"`
		Items []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"items"`
	} `json:"domains"`
}

// sourceCodeDefinitions returns Orbit's indexed Definition count for the project and
// whether that domain/item was present. The names ("source_code", "Definition") are
// Orbit's verified schema; a missing domain means we don't recognize the shape, not
// that the index is empty — the caller treats that as "unknown", not "degraded".
func sourceCodeDefinitions(gs *orbitGraphStatus) (count int, found bool) {
	for _, d := range gs.Domains {
		if !strings.EqualFold(d.Name, "source_code") {
			continue
		}
		for _, it := range d.Items {
			if strings.EqualFold(it.Name, "Definition") {
				return it.Count, true
			}
		}
	}
	return 0, false
}

// assessIndexHealth is a PURE function of Orbit's status + graph_status responses.
// It uses only principled binary checks — no magic thresholds, no language guessing:
// is the cluster up, are all known projects indexed, and does the code graph hold any
// definitions? (st may be nil; gs is required.)
func assessIndexHealth(gs *orbitGraphStatus, st *orbitStatusResp) idxHealth {
	if gs == nil {
		return idxHealth{idxUnknown, "could not read Orbit's index status (graph_status unavailable)"}
	}

	// Cluster availability (coarse): act only on an EXPLICIT not-available / unhealthy
	// signal. Absence of the field is not evidence of a problem.
	if st != nil {
		if st.User != nil && st.User.Available != nil && !*st.User.Available {
			return idxHealth{idxDegraded, "Orbit reports the knowledge graph is not available to this token yet"}
		}
		if st.Status != "" && !strings.EqualFold(st.Status, "healthy") {
			return idxHealth{idxDegraded, fmt.Sprintf("Orbit cluster status is %q, not healthy", st.Status)}
		}
	}

	// Project coverage: a target project Orbit knows about isn't indexed yet.
	if gs.Projects.TotalKnown > 0 && gs.Projects.Indexed < gs.Projects.TotalKnown {
		return idxHealth{idxDegraded, fmt.Sprintf(
			"Orbit has indexed %d of %d known project(s); the call graph would be incomplete",
			gs.Projects.Indexed, gs.Projects.TotalKnown)}
	}

	// Code graph populated? The decisive check.
	defs, found := sourceCodeDefinitions(gs)
	if !found {
		return idxHealth{idxUnknown, "Orbit's graph_status reported no source_code domain (schema may have changed)"}
	}
	if defs == 0 {
		return idxHealth{idxDegraded, "Orbit holds 0 code definitions for this project — it may still be indexing, or the language isn't supported yet"}
	}

	return idxHealth{idxOK, ""}
}

// orbitGet performs a free, read-only Orbit GET (status / graph_status carry no query
// quota) against the --host flag (not hardcoded) and decodes JSON into v.
func orbitGet(host, token, path string, v any) error {
	req, err := http.NewRequest("GET", "https://"+host+"/api/v4/orbit/"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("orbit %s HTTP %d: %s", path, resp.StatusCode, truncate(string(data), 200))
	}
	return json.Unmarshal(data, v)
}

// fetchIndexHealth reads Orbit's free status + graph_status for a project and assesses
// whether its code index can be trusted. graph_status is the decisive signal; status
// is a coarse availability cross-check. Any fetch/parse failure degrades to "unknown"
// — never a false block.
func fetchIndexHealth(host, token string, projectID int) idxHealth {
	var gs orbitGraphStatus
	if err := orbitGet(host, token, fmt.Sprintf("graph_status?project_id=%d", projectID), &gs); err != nil {
		return idxHealth{idxUnknown, fmt.Sprintf("could not read Orbit graph_status: %v", err)}
	}
	var st orbitStatusResp
	var stp *orbitStatusResp
	if err := orbitGet(host, token, "status", &st); err == nil {
		stp = &st
	}
	return assessIndexHealth(&gs, stp)
}
