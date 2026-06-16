// faultline-agent assembles a code subgraph from GitLab Orbit and runs the
// faultline-engine to compute the transitive change-impact ("blast radius") of an
// MR's changed definitions.
//
// Orbit's query DSL is capped at 3 hops, so the agent fetches the project's
// full one-hop CALLS edges (plus all definitions) and hands them to the engine,
// which computes the unbounded transitive closure.
//
// Two access modes:
//   - glab (default): shells out to `glab orbit remote query` (local dev).
//   - rest: calls POST /api/v4/orbit/query directly with a bearer token
//     (for the hosted/container run, using $AI_FLOW_GITLAB_TOKEN).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// ---- Orbit raw (`--format raw`) response shapes ----

type orbitNode struct {
	Type           string `json:"type"`
	ID             string `json:"id"`
	Name           string `json:"name"`
	FilePath       string `json:"file_path"`
	DefinitionType string `json:"definition_type"`
}

type orbitEdge struct {
	From   string `json:"from"`
	FromID string `json:"from_id"`
	To     string `json:"to"`
	ToID   string `json:"to_id"`
	Type   string `json:"type"`
}

type orbitResp struct {
	Result struct {
		Nodes []orbitNode `json:"nodes"`
		Edges []orbitEdge `json:"edges"`
	} `json:"result"`
}

// ---- normalized graph consumed by faultline-engine ----

type gNode struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	FilePath       string `json:"file_path"`
	DefinitionType string `json:"definition_type"`
}

type gEdge struct {
	Type string `json:"type"`
	From string `json:"from"`
	To   string `json:"to"`
}

type graph struct {
	Nodes []gNode `json:"nodes"`
	Edges []gEdge `json:"edges"`
}

// ---- engine report (mirror of faultline-engine's Report) ----

type impacted struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	FilePath       string `json:"file_path"`
	DefinitionType string `json:"definition_type"`
	Distance       int    `json:"distance"`
}

type report struct {
	Changed       []string   `json:"changed"`
	ImpactedCount int        `json:"impacted_count"`
	MaxDepth      int        `json:"max_depth"`
	BlastRadius   []impacted `json:"blast_radius"`
}

// normalize merges the definitions + one or more edge query results (CALLS,
// EXTENDS) into one deduped graph. Variadic over edge responses so callers can
// pass just CALLS (back-compat) or CALLS+EXTENDS. Pure function (unit-tested).
func normalize(defs orbitResp, edgeResps ...orbitResp) graph {
	nm := map[string]gNode{}
	addNodes := func(ns []orbitNode) {
		for _, n := range ns {
			if _, ok := nm[n.ID]; ok {
				continue
			}
			dt := n.DefinitionType
			if dt == "" {
				dt = "Function"
			}
			nm[n.ID] = gNode{ID: n.ID, Name: n.Name, FilePath: n.FilePath, DefinitionType: dt}
		}
	}
	addNodes(defs.Result.Nodes)
	for _, r := range edgeResps {
		addNodes(r.Result.Nodes)
	}

	var g graph
	for _, n := range nm {
		g.Nodes = append(g.Nodes, n)
	}
	for _, r := range edgeResps {
		for _, e := range r.Result.Edges {
			// Keep impact edges (CALLS, EXTENDS) with both endpoints. Empty
			// from/to (partial Orbit responses) and other types (IMPORTS, etc.)
			// would pollute the closure, so they are dropped.
			if (e.Type == "CALLS" || e.Type == "EXTENDS") && e.FromID != "" && e.ToID != "" {
				g.Edges = append(g.Edges, gEdge{Type: e.Type, From: e.FromID, To: e.ToID})
			}
		}
	}
	return g
}

// resolveChanged turns explicit Definition IDs and/or changed file paths into a
// deduped set of changed Definition IDs. Pure function.
func resolveChanged(g graph, changedDefs, changedFiles []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range changedDefs {
		add(id)
	}
	if len(changedFiles) > 0 {
		want := map[string]bool{}
		for _, f := range changedFiles {
			want[f] = true
		}
		for _, n := range g.Nodes {
			if want[n.FilePath] {
				add(n.ID)
			}
		}
	}
	return out
}

// mdCell sanitizes text for a Markdown TABLE cell. GitLab's table parser splits
// on '|' before inline parsing, so pipes and newlines must be neutralized to
// prevent column/row injection from attacker-controlled symbol names or paths.
func mdCell(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	return s
}

// mdCode renders s as inline code in a table cell: a backtick would close the
// span, so backticks are replaced; pipes/newlines are neutralized via mdCell.
func mdCode(s string) string {
	return "`" + mdCell(strings.ReplaceAll(s, "`", "'")) + "`"
}

// isTestFile heuristically identifies test files across common languages. Orbit
// does not index test code, so Faultline scans the checked-out repo itself to find
// which transitively-impacted symbols no test references ("untested blast radius").
func isTestFile(name string) bool {
	base := filepath.Base(name)
	suffixes := []string{
		"_test.go", "_test.py", ".test.ts", ".test.tsx", ".test.js", ".test.jsx",
		".spec.ts", ".spec.js", "_spec.rb", "_test.rb", "Test.java", "Tests.java",
		"Test.kt", "Tests.cs", "_test.rs", "_test.cpp", "_test.cc",
	}
	for _, s := range suffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	if strings.HasPrefix(base, "test_") {
		return true
	}
	return strings.Contains(name, "/test/") || strings.Contains(name, "/tests/") ||
		strings.Contains(name, "/spec/") || strings.Contains(name, "/__tests__/")
}

// readTestCorpus concatenates the contents of every test file under root.
func readTestCorpus(root string) string {
	var b strings.Builder
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "target", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if isTestFile(path) {
			if data, e := os.ReadFile(path); e == nil {
				b.Write(data)
				b.WriteByte('\n')
			}
		}
		return nil
	})
	return b.String()
}

// untestedImpact returns the impacted definitions whose symbol name is not
// referenced anywhere in the test corpus (word-boundary match) — the
// transitively-impacted symbols that no test exercises. Pure given the corpus.
func untestedImpact(blast []impacted, testCorpus string) []impacted {
	var out []impacted
	for _, it := range blast {
		if it.Name == "" {
			continue // unresolved nodes can't be assessed by name
		}
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(it.Name) + `\b`)
		if !re.MatchString(testCorpus) {
			out = append(out, it)
		}
	}
	return out
}

// mermaidLabel sanitizes text for a mermaid ["..."] node label.
func mermaidLabel(s string) string {
	s = strings.ReplaceAll(s, "\"", "'")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// mermaidID maps an arbitrary node id to a mermaid-safe identifier.
func mermaidID(id string) string {
	var b strings.Builder
	b.WriteByte('n')
	for _, r := range id {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// buildMermaid renders the blast-radius subgraph (changed seeds + impacted
// definitions, with untested-impacted nodes flagged red) as a GitLab-native
// mermaid diagram. Pure and deterministic (sorted) so verdicts are reproducible.
func buildMermaid(g graph, changed []string, r report, untested []impacted) string {
	if r.ImpactedCount == 0 {
		return ""
	}
	inSet := map[string]bool{}
	changedSet := map[string]bool{}
	for _, id := range changed {
		changedSet[id] = true
		inSet[id] = true
	}
	for _, it := range r.BlastRadius {
		inSet[it.ID] = true
	}
	untestedSet := map[string]bool{}
	for _, it := range untested {
		untestedSet[it.ID] = true
	}
	label := map[string]string{}
	for _, n := range g.Nodes {
		label[n.ID] = n.Name
	}

	type pair struct{ from, to string }
	var edges []pair
	seen := map[string]bool{}
	for _, e := range g.Edges {
		if inSet[e.From] && inSet[e.To] {
			k := e.From + "\x00" + e.To
			if !seen[k] {
				seen[k] = true
				edges = append(edges, pair{e.From, e.To})
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].from != edges[j].from {
			return edges[i].from < edges[j].from
		}
		return edges[i].to < edges[j].to
	})
	var ids []string
	for id := range inSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var b strings.Builder
	b.WriteString("```mermaid\ngraph TD\n")
	for _, id := range ids {
		name := label[id]
		if name == "" {
			name = "unresolved#" + id
		}
		b.WriteString(fmt.Sprintf("  %s[\"%s\"]\n", mermaidID(id), mermaidLabel(name)))
	}
	for _, e := range edges {
		b.WriteString(fmt.Sprintf("  %s --> %s\n", mermaidID(e.from), mermaidID(e.to)))
	}
	b.WriteString("  classDef changed fill:#cfe2ff,stroke:#0a58ca,color:#000;\n")
	b.WriteString("  classDef untested fill:#f8d7da,stroke:#b02a37,color:#000;\n")
	for _, id := range ids {
		if changedSet[id] {
			b.WriteString("  class " + mermaidID(id) + " changed;\n")
		} else if untestedSet[id] {
			b.WriteString("  class " + mermaidID(id) + " untested;\n")
		}
	}
	b.WriteString("```\n")
	return b.String()
}

// recipeComparison renders Faultline's headline differentiator. Orbit's query DSL
// is hard-capped at 3 hops (max_hops <= 3), so even an optimally-written native
// reverse-`CALLS` query can only reach impacted definitions within 3 hops of the
// change. Faultline's engine computes the full transitive closure. This block
// quantifies the gap and lists the impacted definitions that lie beyond ANY
// single Orbit query (>= 4 hops). Returns "" when nothing is beyond reach, so we
// never overclaim on a shallow change. Pure (no I/O).
func recipeComparison(r report) string {
	if r.ImpactedCount == 0 {
		return ""
	}
	var beyond []impacted
	within := 0
	for _, it := range r.BlastRadius {
		if it.Distance >= 4 {
			beyond = append(beyond, it)
		} else {
			within++
		}
	}
	if len(beyond) == 0 {
		return "" // entirely within Orbit's 3-hop reach — no moat to claim
	}
	var b strings.Builder
	b.WriteString("\n**🔭 Orbit 3-hop query vs Faultline closure**\n")
	b.WriteString(fmt.Sprintf(
		"Orbit's query DSL is hard-capped at 3 hops (`max_hops` ≤ 3). A native reverse-`CALLS` query therefore reaches at most **%d of %d** impacted definition(s); the other **%d** sit ≥ 4 hops from the change and are invisible to *any* single Orbit query. Faultline computes the full closure and surfaces them:\n",
		within, r.ImpactedCount, len(beyond)))
	for _, it := range beyond {
		name := it.Name
		if name == "" {
			name = fmt.Sprintf("(unresolved #%s)", it.ID)
		}
		file := it.FilePath
		if file == "" {
			file = "—"
		}
		b.WriteString(fmt.Sprintf("- %s (%s) — **%d hops**\n", mdCode(name), mdCell(file), it.Distance))
	}
	return b.String()
}

// renderMarkdown turns an engine report into a Markdown MR verdict. Pure (no I/O)
// so it can be unit-tested. Unnamed/unresolved nodes (Orbit sometimes returns a
// Definition without name/file metadata) are labeled rather than rendered blank.
func renderMarkdown(r report, changedNames []string, untested []impacted) string {
	var b strings.Builder
	b.WriteString("## 🪨 Faultline — change-impact analysis\n\n")
	if len(changedNames) > 0 {
		b.WriteString("**Changed:** ")
		for i, n := range changedNames {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(mdCode(n))
		}
		b.WriteString("\n\n")
	}
	if r.ImpactedCount == 0 {
		b.WriteString("✅ **Empty blast radius.** No definition transitively calls the changed code in the indexed graph.\n")
		return b.String()
	}
	b.WriteString(fmt.Sprintf("⚠️ **%d definition(s) transitively affected** — max depth **%d**", r.ImpactedCount, r.MaxDepth))
	if r.MaxDepth > 3 {
		b.WriteString(", beyond Orbit's 3-hop query cap")
	}
	b.WriteString(".\n\n| Impacted definition | File | Caller distance |\n|---|---|---|\n")
	for _, it := range r.BlastRadius {
		name := it.Name
		if name == "" {
			name = fmt.Sprintf("(unresolved #%s)", it.ID)
		}
		file := it.FilePath
		if file == "" {
			file = "—"
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %d |\n", mdCode(name), mdCell(file), it.Distance))
	}
	if rc := recipeComparison(r); rc != "" {
		b.WriteString(rc)
	}
	if len(untested) > 0 {
		b.WriteString(fmt.Sprintf("\n🚦 **Untested blast radius — %d impacted definition(s) with NO test coverage** (highest risk):\n", len(untested)))
		for _, it := range untested {
			name := it.Name
			if name == "" {
				name = fmt.Sprintf("(unresolved #%s)", it.ID)
			}
			file := it.FilePath
			if file == "" {
				file = "—"
			}
			b.WriteString(fmt.Sprintf("- %s (%s)\n", mdCode(name), mdCell(file)))
		}
	}
	b.WriteString("\n<sub>Transitive reverse-`CALLS`/`EXTENDS` closure computed by the Faultline engine over GitLab Orbit's knowledge graph.</sub>\n")
	return b.String()
}

// postMRNote posts body as a note on a merge request via the GitLab REST API.
func postMRNote(host, token string, projectID, mrIID int, body string) error {
	endpoint := fmt.Sprintf("https://%s/api/v4/projects/%d/merge_requests/%d/notes", host, projectID, mrIID)
	form := url.Values{}
	form.Set("body", body)
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("MR note POST HTTP %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	return nil
}

// orbitToken resolves the GitLab token from the container/CI env.
func orbitToken() string {
	if t := os.Getenv("AI_FLOW_GITLAB_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GITLAB_TOKEN")
}

func glabPath() string {
	if p := os.Getenv("GLAB"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return home + "/.local/bin/glab"
}

func queryGlab(body string) (orbitResp, error) {
	var out orbitResp
	cmd := exec.Command(glabPath(), "orbit", "remote", "query", "-", "--format", "raw")
	cmd.Stdin = strings.NewReader(body)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		return out, fmt.Errorf("glab query failed: %v: %s", err, se.String())
	}
	return out, json.Unmarshal(so.Bytes(), &out)
}

// httpClient bounds every GitLab/Orbit call so a hung endpoint can't hang the agent.
var httpClient = &http.Client{Timeout: 30 * time.Second}

func queryREST(body, host, token string) (orbitResp, error) {
	var out orbitResp
	req, err := http.NewRequest("POST", "https://"+host+"/api/v4/orbit/query", strings.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("orbit REST HTTP %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	return out, json.Unmarshal(data, &out)
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) > n {
		return string([]rune(s)[:n])
	}
	return s
}

func defsQuery(pid int) string {
	return fmt.Sprintf(`{"query":{"query_type":"traversal","node":{"id":"d","entity":"Definition","columns":["name","file_path","definition_type"],"filters":{"project_id":{"op":"eq","value":%d}}},"limit":1000},"response_format":"raw"}`, pid)
}

func callsQuery(pid int) string {
	return fmt.Sprintf(`{"query":{"query_type":"traversal","nodes":[{"id":"a","entity":"Definition","columns":["name","file_path","definition_type"],"filters":{"project_id":{"op":"eq","value":%d}}},{"id":"b","entity":"Definition","columns":["name","file_path","definition_type"]}],"relationships":[{"type":"CALLS","from":"a","to":"b","min_hops":1,"max_hops":1,"direction":"outgoing"}],"limit":1000},"response_format":"raw"}`, pid)
}

// extendsQuery fetches one-hop EXTENDS edges (subtype -> supertype: inheritance,
// interface implementation, struct embedding). The engine folds these into the
// same transitive closure as CALLS, so a base-type change ripples to subtypes.
func extendsQuery(pid int) string {
	return fmt.Sprintf(`{"query":{"query_type":"traversal","nodes":[{"id":"a","entity":"Definition","columns":["name","file_path","definition_type"],"filters":{"project_id":{"op":"eq","value":%d}}},{"id":"b","entity":"Definition","columns":["name","file_path","definition_type"]}],"relationships":[{"type":"EXTENDS","from":"a","to":"b","min_hops":1,"max_hops":1,"direction":"outgoing"}],"limit":1000},"response_format":"raw"}`, pid)
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "faultline-agent:", err)
		os.Exit(1)
	}
}

func main() {
	pid := flag.Int("project-id", 0, "GitLab project ID")
	changedDefs := flag.String("changed-defs", "", "comma-separated Definition IDs changed")
	changedFiles := flag.String("changed-files", "", "comma-separated changed file paths")
	enginePath := flag.String("engine", "", "path to faultline-engine binary")
	mode := flag.String("mode", "glab", "orbit access mode: glab | rest")
	host := flag.String("host", "gitlab.com", "GitLab host (rest mode)")
	graphOut := flag.String("graph-out", "", "optional path to write the normalized graph JSON")
	format := flag.String("format", "json", "verdict output when not posting: json | md")
	postMR := flag.Int("post-mr", 0, "if >0, POST the verdict as a note to this merge request IID")
	mrProject := flag.Int("mr-project-id", 0, "project ID for --post-mr (defaults to --project-id)")
	repoRoot := flag.String("repo-root", "", "repo checkout to scan for test coverage of impacted symbols (untested blast radius)")
	gateUntested := flag.Int("gate-untested", -1, "if >=0, exit non-zero when untested-impacted count exceeds N (gates the MR)")
	flag.Parse()

	if *pid == 0 || *enginePath == "" {
		fmt.Fprintln(os.Stderr, "usage: faultline-agent --project-id N --engine PATH [--mode glab|rest] [--format md] [--post-mr IID] (--changed-defs IDs | --changed-files paths)")
		os.Exit(2)
	}

	query := queryGlab
	switch *mode {
	case "glab":
		// default: shell out to the glab CLI
	case "rest":
		token := orbitToken()
		if token == "" {
			fatal(fmt.Errorf("rest mode requires AI_FLOW_GITLAB_TOKEN or GITLAB_TOKEN env var"))
		}
		query = func(body string) (orbitResp, error) { return queryREST(body, *host, token) }
	default:
		fatal(fmt.Errorf("unknown --mode %q (want glab or rest)", *mode))
	}

	defs, err := query(defsQuery(*pid))
	fatal(err)
	calls, err := query(callsQuery(*pid))
	fatal(err)
	// EXTENDS is best-effort: older Orbit versions may not expose it, and a
	// project with no inheritance simply returns no edges — neither should abort.
	extends, err := query(extendsQuery(*pid))
	if err != nil {
		fmt.Fprintf(os.Stderr, "faultline-agent: EXTENDS query failed, continuing with CALLS only: %v\n", err)
		extends = orbitResp{}
	}

	g := normalize(defs, calls, extends)
	changed := resolveChanged(g, splitNonEmpty(*changedDefs), splitNonEmpty(*changedFiles))
	if len(changed) == 0 {
		fatal(fmt.Errorf("no changed definitions resolved (project may be unindexed, or files have no definitions)"))
	}

	graphPath := *graphOut
	if graphPath == "" {
		f, err := os.CreateTemp("", "faultline-graph-*.json")
		fatal(err)
		graphPath = f.Name()
		f.Close()
	}
	data, _ := json.MarshalIndent(g, "", "  ")
	fatal(os.WriteFile(graphPath, data, 0o644))

	engineOut, err := exec.Command(*enginePath, "--graph", graphPath, "--changed", strings.Join(changed, ",")).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			fatal(fmt.Errorf("engine failed: %v: %s", err, strings.TrimSpace(string(ee.Stderr))))
		}
		fatal(fmt.Errorf("engine failed: %v", err))
	}

	if *graphOut == "" {
		os.Remove(graphPath) // best-effort cleanup of the auto-created temp graph
	}

	var rep report
	fatal(json.Unmarshal(engineOut, &rep))

	// Map changed IDs back to human-readable names for the verdict.
	nameByID := map[string]string{}
	for _, n := range g.Nodes {
		nameByID[n.ID] = n.Name
	}
	var changedNames []string
	for _, id := range changed {
		if nm := nameByID[id]; nm != "" {
			changedNames = append(changedNames, nm)
		} else {
			changedNames = append(changedNames, id)
		}
	}

	// Untested blast radius: which transitively-impacted symbols no test covers.
	var untested []impacted
	if *repoRoot != "" {
		untested = untestedImpact(rep.BlastRadius, readTestCorpus(*repoRoot))
	}

	md := renderMarkdown(rep, changedNames, untested)
	if mer := buildMermaid(g, changed, rep, untested); mer != "" {
		// Insert the blast-radius diagram just above the attribution footer.
		md = strings.Replace(md, "\n<sub>Transitive", "\n"+mer+"\n<sub>Transitive", 1)
	}

	switch {
	case *postMR > 0:
		token := orbitToken()
		if token == "" {
			fatal(fmt.Errorf("--post-mr requires AI_FLOW_GITLAB_TOKEN or GITLAB_TOKEN env var"))
		}
		proj := *mrProject
		if proj == 0 {
			proj = *pid
		}
		fatal(postMRNote(*host, token, proj, *postMR, md))
		fmt.Printf("posted Faultline verdict to MR !%d (project %d): %d impacted, %d untested, max depth %d\n",
			*postMR, proj, rep.ImpactedCount, len(untested), rep.MaxDepth)
	case *format == "md":
		fmt.Println(md)
	default:
		fmt.Print(string(engineOut))
	}

	// Deterministic gate: fail the pipeline (block the MR) when too many
	// transitively-impacted symbols are untested. This is what makes Faultline a
	// governance GATE, not just a comment.
	if *gateUntested >= 0 && len(untested) > *gateUntested {
		fmt.Fprintf(os.Stderr,
			"faultline-agent: GATE FAILED — %d untested impacted definition(s) exceed threshold %d\n",
			len(untested), *gateUntested)
		os.Exit(1)
	}
}
