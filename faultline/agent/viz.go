package main

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
)

// Interactive blast-radius visualization.
//
// GitLab MR notes sanitize JavaScript and gitlab.com serves HTML artifacts as
// text/plain, so an interactive graph cannot run *inside* the MR note. Instead the
// agent emits a single self-contained HTML file (zero dependencies — no D3, no CDN),
// delivered as a CI artifact (`expose_as` link in the MR; opens fully interactive in
// any browser, and renders inline if GitLab Pages is enabled).
//
// The force-directed layout is computed HERE in Go, deterministically, and the final
// coordinates are embedded — so the same (graph, change) yields a byte-identical page,
// the same reproducibility guarantee as the rest of Faultline. The embedded JS only
// renders + handles pan/zoom/drag/hover; it runs no physics.

const (
	vizW   = 960.0
	vizH   = 640.0
	vizPad = 40.0
)

type vizNode struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	File  string  `json:"file"`
	Role  string  `json:"role"` // "changed" | "untested" | "impacted"
	Dist  int     `json:"dist"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

type vizEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// layoutPositions computes a deterministic Fruchterman–Reingold layout for `n` nodes
// connected by undirected `edges` (index pairs). Seeded initial placement + fixed
// iteration count + fixed summation order ⇒ identical coordinates for identical input
// (callers must pass nodes/edges in a canonical sorted order). Coordinates are clamped
// inside [vizPad, dim-vizPad].
func layoutPositions(n int, edges [][2]int) [][2]float64 {
	pos := make([][2]float64, n)
	if n == 0 {
		return pos
	}
	if n == 1 {
		pos[0] = [2]float64{vizW / 2, vizH / 2}
		return pos
	}

	// Reproducible PRNG (SplitMix-style) for the initial jitter only.
	var seed uint64 = 0x9E3779B97F4A7C15
	rnd := func() float64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return float64(seed>>11) / float64(uint64(1)<<53) // [0,1)
	}

	k := 0.8 * math.Sqrt(vizW*vizH/float64(n)) // ideal edge length
	for i := 0; i < n; i++ {
		ang := 2 * math.Pi * float64(i) / float64(n)
		pos[i][0] = vizW/2 + (vizW/3)*math.Cos(ang) + (rnd()-0.5)*k
		pos[i][1] = vizH/2 + (vizH/3)*math.Sin(ang) + (rnd()-0.5)*k
	}

	temp := vizW / 10
	disp := make([][2]float64, n)
	for it := 0; it < 300; it++ {
		for i := range disp {
			disp[i][0], disp[i][1] = 0, 0
		}
		// Repulsion between every pair (small graphs ⇒ O(n^2) is fine).
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				dx := pos[i][0] - pos[j][0]
				dy := pos[i][1] - pos[j][1]
				d2 := dx*dx + dy*dy
				if d2 < 0.01 {
					dx, dy, d2 = 0.01, 0.01, 0.0002 // deterministic nudge for coincident nodes
				}
				d := math.Sqrt(d2)
				f := k * k / d
				ux, uy := dx/d, dy/d
				disp[i][0] += ux * f
				disp[i][1] += uy * f
				disp[j][0] -= ux * f
				disp[j][1] -= uy * f
			}
		}
		// Attraction along edges.
		for _, e := range edges {
			a, b := e[0], e[1]
			dx := pos[a][0] - pos[b][0]
			dy := pos[a][1] - pos[b][1]
			d := math.Sqrt(dx*dx + dy*dy)
			if d < 0.01 {
				d = 0.01
			}
			f := d * d / k
			ux, uy := dx/d, dy/d
			disp[a][0] -= ux * f
			disp[a][1] -= uy * f
			disp[b][0] += ux * f
			disp[b][1] += uy * f
		}
		// Apply, capped by the cooling temperature, then clamp to frame.
		for i := 0; i < n; i++ {
			dl := math.Sqrt(disp[i][0]*disp[i][0] + disp[i][1]*disp[i][1])
			if dl > 0 {
				lim := math.Min(dl, temp)
				pos[i][0] += disp[i][0] / dl * lim
				pos[i][1] += disp[i][1] / dl * lim
			}
			pos[i][0] = math.Max(vizPad, math.Min(vizW-vizPad, pos[i][0]))
			pos[i][1] = math.Max(vizPad, math.Min(vizH-vizPad, pos[i][1]))
		}
		temp *= 0.97 // cool
	}
	return pos
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// buildInteractiveHTML renders the blast-radius subgraph (changed seeds + impacted
// definitions, untested flagged red) as a self-contained interactive HTML page. Pure
// and deterministic. Returns "" when the blast radius is empty (nothing to draw).
func buildInteractiveHTML(g graph, changed []string, r report, untested []impacted) string {
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
	file := map[string]string{}
	dist := map[string]int{}
	for _, n := range g.Nodes {
		label[n.ID] = n.Name
		file[n.ID] = n.FilePath
	}
	for _, it := range r.BlastRadius {
		dist[it.ID] = it.Distance
	}

	// Canonical node order (sorted) ⇒ deterministic layout regardless of input order.
	var ids []string
	for id := range inSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	idx := map[string]int{}
	for i, id := range ids {
		idx[id] = i
	}

	// Deduped, sorted directed edges among in-set nodes (self-edges dropped).
	type pr struct{ from, to string }
	seen := map[string]bool{}
	var prs []pr
	for _, e := range g.Edges {
		if inSet[e.From] && inSet[e.To] && e.From != e.To {
			key := e.From + "\x00" + e.To
			if !seen[key] {
				seen[key] = true
				prs = append(prs, pr{e.From, e.To})
			}
		}
	}
	sort.Slice(prs, func(i, j int) bool {
		if prs[i].from != prs[j].from {
			return prs[i].from < prs[j].from
		}
		return prs[i].to < prs[j].to
	})

	le := make([][2]int, len(prs))
	for i, p := range prs {
		le[i] = [2]int{idx[p.from], idx[p.to]}
	}
	pos := layoutPositions(len(ids), le)

	nodes := make([]vizNode, len(ids))
	for i, id := range ids {
		role := "impacted"
		if changedSet[id] {
			role = "changed"
		} else if untestedSet[id] {
			role = "untested"
		}
		lbl := label[id]
		if lbl == "" {
			lbl = "unresolved#" + id
		}
		nodes[i] = vizNode{
			ID: id, Label: lbl, File: file[id], Role: role, Dist: dist[id],
			X: round2(pos[i][0]), Y: round2(pos[i][1]),
		}
	}
	edges := make([]vizEdge, len(prs))
	for i, p := range prs {
		edges[i] = vizEdge{From: p.from, To: p.to}
	}

	// json.Marshal HTML-escapes <, >, & in string values, so an attacker-controlled
	// symbol name containing "</script>" cannot break out of the data block.
	blob, err := json.Marshal(struct {
		Nodes []vizNode `json:"nodes"`
		Edges []vizEdge `json:"edges"`
	}{nodes, edges})
	if err != nil {
		return ""
	}
	return strings.Replace(vizTemplate, "__DATA__", string(blob), 1)
}

// vizTemplate is the self-contained page. __DATA__ is replaced with HTML-safe JSON.
// No backticks/% so it stays a clean Go raw-string literal and needs no fmt.
const vizTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Faultline — change-impact graph</title>
<style>
  :root{--bg:#0d1117;--fg:#e6edf3;--muted:#8b949e;--changed:#1f6feb;--untested:#f85149;--impacted:#6e7681;--edge:#30363d;}
  html,body{margin:0;height:100%;background:var(--bg);color:var(--fg);font-family:system-ui,Segoe UI,Roboto,Helvetica,Arial,sans-serif;}
  #wrap{display:flex;flex-direction:column;height:100%;}
  header{padding:10px 14px;border-bottom:1px solid var(--edge);}
  header h1{font-size:15px;margin:0;font-weight:600;}
  header p{margin:4px 0 0;font-size:12px;color:var(--muted);}
  .legend{display:flex;gap:14px;margin-top:6px;font-size:12px;flex-wrap:wrap;}
  .legend span{display:inline-flex;align-items:center;gap:5px;}
  .dot{width:10px;height:10px;border-radius:50%;display:inline-block;}
  #svg{flex:1;width:100%;cursor:grab;touch-action:none;}
  #svg.drag{cursor:grabbing;}
  .node circle{stroke:#0d1117;stroke-width:1.5px;cursor:pointer;}
  .node.changed circle{fill:var(--changed);}
  .node.untested circle{fill:var(--untested);}
  .node.impacted circle{fill:var(--impacted);}
  .node text{fill:var(--fg);font-size:11px;pointer-events:none;paint-order:stroke;stroke:#0d1117;stroke-width:3px;}
  line.edge{stroke:var(--edge);stroke-width:1.5px;}
  #tip{position:fixed;pointer-events:none;background:#161b22;border:1px solid var(--edge);border-radius:6px;padding:6px 9px;font-size:12px;display:none;max-width:340px;z-index:9;}
  #tip .m{color:var(--muted);margin-top:2px;}
</style>
</head>
<body>
<div id="wrap">
  <header>
    <h1>🪨 Faultline — change-impact graph</h1>
    <p id="sub"></p>
    <div class="legend">
      <span><i class="dot" style="background:var(--changed)"></i>changed</span>
      <span><i class="dot" style="background:var(--untested)"></i>untested impacted</span>
      <span><i class="dot" style="background:var(--impacted)"></i>impacted (tested)</span>
      <span class="m">scroll = zoom · drag background = pan · drag node = move · hover = details</span>
    </div>
  </header>
  <svg id="svg" viewBox="0 0 960 640" preserveAspectRatio="xMidYMid meet">
    <defs>
      <marker id="arrow" viewBox="0 0 10 10" refX="14" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
        <path d="M0,0 L10,5 L0,10 z" fill="#30363d"></path>
      </marker>
    </defs>
    <g id="view"></g>
  </svg>
</div>
<div id="tip"></div>
<script id="faultline-data" type="application/json">__DATA__</script>
<script>
(function(){
  "use strict";
  var data = JSON.parse(document.getElementById("faultline-data").textContent);
  var NS = "http://www.w3.org/2000/svg";
  var svg = document.getElementById("svg");
  var view = document.getElementById("view");
  var tip = document.getElementById("tip");
  var byId = {};
  data.nodes.forEach(function(n){ byId[n.id] = n; });

  var nUntested = data.nodes.filter(function(n){ return n.role === "untested"; }).length;
  document.getElementById("sub").textContent =
    data.nodes.length + " definitions · " + data.edges.length + " edges · " + nUntested + " untested (red)";

  data.edges.forEach(function(e){
    var a = byId[e.from], b = byId[e.to];
    if(!a || !b){ return; }
    var ln = document.createElementNS(NS, "line");
    ln.setAttribute("class", "edge");
    ln.setAttribute("x1", a.x); ln.setAttribute("y1", a.y);
    ln.setAttribute("x2", b.x); ln.setAttribute("y2", b.y);
    ln.setAttribute("marker-end", "url(#arrow)");
    ln.dataset.from = e.from; ln.dataset.to = e.to;
    view.appendChild(ln);
  });

  data.nodes.forEach(function(n){
    var g = document.createElementNS(NS, "g");
    g.setAttribute("class", "node " + n.role);
    g.setAttribute("transform", "translate(" + n.x + "," + n.y + ")");
    var c = document.createElementNS(NS, "circle");
    c.setAttribute("r", n.role === "changed" ? 9 : 6);
    g.appendChild(c);
    var t = document.createElementNS(NS, "text");
    t.setAttribute("x", 10); t.setAttribute("y", 4);
    t.textContent = n.label;
    g.appendChild(t);
    g.dataset.id = n.id;
    g.addEventListener("mousemove", function(ev){ showTip(ev, n); });
    g.addEventListener("mouseleave", function(){ tip.style.display = "none"; });
    g.addEventListener("mousedown", function(ev){ startNodeDrag(ev, n, g); });
    view.appendChild(g);
  });

  function showTip(ev, n){
    var role = n.role === "untested" ? "untested impacted" : (n.role === "changed" ? "changed" : "impacted (tested)");
    tip.innerHTML = "<b></b><div class='m'></div>";
    tip.querySelector("b").textContent = n.label;
    tip.querySelector(".m").textContent = (n.file || "—") + "  ·  " + role + (n.dist ? ("  ·  " + n.dist + " hops") : "");
    tip.style.display = "block";
    tip.style.left = (ev.clientX + 12) + "px";
    tip.style.top = (ev.clientY + 12) + "px";
  }

  var tx = 0, ty = 0, scale = 1;
  function apply(){ view.setAttribute("transform", "translate(" + tx + "," + ty + ") scale(" + scale + ")"); }
  svg.addEventListener("wheel", function(ev){
    ev.preventDefault();
    var f = ev.deltaY < 0 ? 1.1 : 1 / 1.1;
    scale = Math.max(0.2, Math.min(5, scale * f));
    apply();
  }, { passive: false });

  var panning = false, px = 0, py = 0;
  svg.addEventListener("mousedown", function(ev){
    if(ev.target.closest(".node")){ return; }
    panning = true; px = ev.clientX - tx; py = ev.clientY - ty; svg.classList.add("drag");
  });
  window.addEventListener("mousemove", function(ev){
    if(panning){ tx = ev.clientX - px; ty = ev.clientY - py; apply(); }
  });
  window.addEventListener("mouseup", function(){ panning = false; svg.classList.remove("drag"); });

  var dragN = null, dragG = null;
  function startNodeDrag(ev, n, g){ ev.stopPropagation(); dragN = n; dragG = g; window.addEventListener("mousemove", onNodeDrag); window.addEventListener("mouseup", endNodeDrag); }
  function svgPoint(ev){
    var pt = svg.createSVGPoint(); pt.x = ev.clientX; pt.y = ev.clientY;
    return pt.matrixTransform(view.getScreenCTM().inverse());
  }
  function onNodeDrag(ev){
    if(!dragN){ return; }
    var p = svgPoint(ev); dragN.x = p.x; dragN.y = p.y;
    dragG.setAttribute("transform", "translate(" + p.x + "," + p.y + ")");
    view.querySelectorAll("line.edge").forEach(function(ln){
      if(ln.dataset.from === dragN.id){ ln.setAttribute("x1", p.x); ln.setAttribute("y1", p.y); }
      if(ln.dataset.to === dragN.id){ ln.setAttribute("x2", p.x); ln.setAttribute("y2", p.y); }
    });
  }
  function endNodeDrag(){ dragN = null; dragG = null; window.removeEventListener("mousemove", onNodeDrag); window.removeEventListener("mouseup", endNodeDrag); }
})();
</script>
</body>
</html>
`
