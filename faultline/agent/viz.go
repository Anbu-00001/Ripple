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
// agent emits a single self-contained HTML file (zero dependencies — no D3, no CDN,
// no web fonts), delivered as a CI artifact (`expose_as` link in the MR; download to
// open it fully interactive in any browser, and renders inline where GitLab Pages is
// enabled).
//
// The force-directed layout is computed HERE in Go, deterministically, and the final
// coordinates are embedded — so the same (graph, change) yields a byte-identical page,
// the same reproducibility guarantee as the rest of Faultline. The embedded JS only
// renders + handles pan/zoom/drag/hover; it runs no physics, randomness, or clocks, so
// it never changes the served bytes.

const (
	vizW   = 960.0
	vizH   = 640.0
	vizPad = 40.0
)

type vizNode struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Type  string  `json:"type"` // "changed" | "untested" | "tested"
	File  string  `json:"file"`
	Role  string  `json:"role"` // definition kind (function/method/...), may be ""
	Hops  int     `json:"hops"`
}

type vizEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// vizMeta drives the header verdict label + footer stats. Every field is a fact
// about the rendered subgraph; the artifact reports the untested *count* and never
// asserts a block/advisory verdict it can't be sure of — the gate decision lives in
// the MR note, not here.
type vizMeta struct {
	Changed  string `json:"changed"`
	Untested int    `json:"untested"`
	Total    int    `json:"total"`
	MaxDepth int    `json:"maxDepth"`
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
// definitions, untested flagged) as a self-contained interactive HTML page. Pure and
// deterministic. Returns "" when the blast radius is empty (nothing to draw).
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
	defType := map[string]string{}
	dist := map[string]int{}
	for _, n := range g.Nodes {
		label[n.ID] = n.Name
		file[n.ID] = n.FilePath
		defType[n.ID] = n.DefinitionType
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

	maxDepth := 0
	nodes := make([]vizNode, len(ids))
	for i, id := range ids {
		typ := "tested"
		if changedSet[id] {
			typ = "changed"
		} else if untestedSet[id] {
			typ = "untested"
		}
		lbl := label[id]
		if lbl == "" {
			lbl = "unresolved#" + id
		}
		if dist[id] > maxDepth {
			maxDepth = dist[id]
		}
		nodes[i] = vizNode{
			ID: id, Label: lbl, X: round2(pos[i][0]), Y: round2(pos[i][1]),
			Type: typ, File: file[id], Role: defType[id], Hops: dist[id],
		}
	}
	edges := make([]vizEdge, len(prs))
	for i, p := range prs {
		edges[i] = vizEdge{From: p.from, To: p.to}
	}

	changedLabel := ""
	for _, id := range changed {
		if l := label[id]; l != "" {
			changedLabel = l
			break
		}
	}

	// json.Marshal HTML-escapes <, >, & in string values, so an attacker-controlled
	// symbol name containing "</script>" cannot break out of the <script> data block.
	// (The data lives in a <script type="application/json"> island, NOT an HTML
	// attribute — an attribute would need quote-escaping json.Marshal does not do.)
	blob, err := json.Marshal(struct {
		Nodes []vizNode `json:"nodes"`
		Edges []vizEdge `json:"edges"`
		Meta  vizMeta   `json:"meta"`
	}{nodes, edges, vizMeta{
		Changed:  changedLabel,
		Untested: len(untested),
		Total:    len(ids),
		MaxDepth: maxDepth,
	}})
	if err != nil {
		return ""
	}
	return strings.Replace(vizTemplate, "__DATA__", string(blob), 1)
}

// vizTemplate is the self-contained page. __DATA__ (the single literal occurrence,
// in the <script type="application/json"> island) is replaced with HTML-safe JSON.
// No backticks anywhere — this is a Go raw-string literal delimited by backticks.
const vizTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Faultline — Blast Radius</title>
<style>
  :root{
    --bg:#0d1117; --panel:#161b22; --panel2:#0f141b; --edge:#30363d; --text:#e6edf3; --muted:#8b949e;
    --changed:#56B4E9; --untested:#E69F00; --tested:#009E73; --block:#d55e00;
    --sans: system-ui,-apple-system,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;
    --mono: ui-monospace,SFMono-Regular,Menlo,Consolas,"Liberation Mono",monospace;
  }
  *{box-sizing:border-box}
  html,body{margin:0;height:100%}
  body{background:var(--bg);color:var(--text);font-family:var(--sans);-webkit-font-smoothing:antialiased;overflow:hidden}
  .fl-app{display:flex;flex-direction:column;height:100vh}

  /* ---------- header ---------- */
  .fl-head{display:flex;align-items:center;justify-content:space-between;gap:18px;padding:15px 22px;border-bottom:1px solid var(--edge);background:#0d1117;flex-wrap:wrap}
  .fl-brand{display:flex;align-items:center;gap:13px;min-width:0}
  .fl-mark{display:flex;align-items:center;gap:5px}
  .fl-mark i{display:block;width:11px;height:11px}
  .fl-mark .m1{border-radius:50%;background:var(--changed)}
  .fl-mark .m2{transform:rotate(45deg);background:var(--untested)}
  .fl-mark .m3{border-radius:2px;background:var(--tested)}
  .fl-titles{display:flex;flex-direction:column;gap:2px;min-width:0}
  .fl-title{font-size:16px;font-weight:600;letter-spacing:.01em;display:flex;align-items:center;gap:9px}
  .fl-title .sep{color:#3a4250}
  .fl-title .sub{color:var(--muted);font-weight:500}
  .fl-desc{font-size:12.5px;color:var(--muted)}
  .fl-right{display:flex;align-items:center;gap:16px;flex-wrap:wrap}
  .fl-verdict{display:inline-flex;align-items:center;gap:8px;padding:6px 12px;border-radius:999px;background:rgba(213,94,0,.14);border:1px solid rgba(213,94,0,.5);color:#f0a878;font-size:12.5px;font-weight:600;white-space:nowrap}
  .fl-verdict .dot{width:15px;height:15px;border-radius:50%;background:var(--block);display:inline-flex;align-items:center;justify-content:center}
  .fl-verdict .dot b{width:8px;height:2.5px;background:#fff;border-radius:1px;display:block}
  .fl-verdict.clear{background:rgba(0,158,115,.14);border-color:rgba(0,158,115,.5);color:#5fd3b2}
  .fl-verdict.clear .dot{background:var(--tested)}
  .fl-legend{display:flex;gap:8px;flex-wrap:wrap}
  .fl-chip{display:inline-flex;align-items:center;gap:8px;padding:5px 11px;border:1px solid var(--edge);border-radius:999px;font-size:12px;color:#c9d1d9;background:var(--panel2);white-space:nowrap}
  .fl-chip svg{display:block}

  /* ---------- canvas ---------- */
  .fl-canvas{position:relative;flex:1;overflow:hidden;background-color:var(--bg);
    background-image:radial-gradient(rgba(139,148,158,.10) 1px, transparent 1.4px);
    background-size:26px 26px;background-position:center}
  .fl-canvas::after{content:"";position:absolute;inset:0;pointer-events:none;
    background:radial-gradient(120% 120% at 50% 44%, transparent 54%, rgba(2,5,10,.66) 100%)}
  .fl-svg{position:absolute;inset:0;width:100%;height:100%;cursor:grab;display:block}
  .fl-svg.grabbing{cursor:grabbing}

  /* ---------- edges ---------- */
  .fl-edge{stroke:var(--edge);stroke-width:1.6;fill:none;transition:stroke .15s ease,stroke-width .15s ease,opacity .15s ease}
  .fl-svg.is-hover .fl-edge{opacity:.14}
  .fl-svg.is-hover .fl-edge.lit{opacity:1;stroke:#6e7681;stroke-width:2.3}

  /* ---------- nodes ---------- */
  .fl-node{cursor:grab}
  .fl-node .fl-shape{transition:opacity .15s ease}
  .fl-node--changed .fl-shape{fill:var(--changed);stroke:#0a3550;stroke-width:1.5}
  .fl-node--changed .fl-core{fill:rgba(255,255,255,.30)}
  .fl-node--changed .fl-ring{fill:none;stroke:var(--changed);stroke-width:2;opacity:.5;animation:flpulse 3s ease-in-out infinite}
  .fl-node--untested .fl-shape{fill:#3a2a06;stroke:var(--untested);stroke-width:2}
  .fl-node--tested .fl-shape{fill:#0e2a22;stroke:var(--tested);stroke-width:2}
  .fl-label{fill:var(--text);font-family:var(--mono);font-size:13px;font-weight:500;paint-order:stroke;stroke:var(--bg);stroke-width:3.5px;stroke-linejoin:round}
  .fl-svg.is-hover .fl-node{opacity:.2;transition:opacity .15s ease}
  .fl-svg.is-hover .fl-node.lit{opacity:1}
  @keyframes flpulse{0%,100%{opacity:.5}50%{opacity:.14}}

  /* ---------- tooltip ---------- */
  .fl-tip{position:fixed;left:0;top:0;pointer-events:none;opacity:0;transform:translateY(4px);transition:opacity .12s ease,transform .12s ease;
    background:var(--panel);border:1px solid var(--edge);border-radius:11px;padding:11px 13px;min-width:184px;max-width:280px;
    box-shadow:0 14px 34px rgba(0,0,0,.55);z-index:60}
  .fl-tip.on{opacity:1;transform:none}
  .fl-tip-name{font-family:var(--mono);font-weight:700;font-size:14px;color:#fff;letter-spacing:.01em}
  .fl-tip-meta{margin-top:5px;font-size:12px;color:var(--muted);font-family:var(--mono);line-height:1.5}
  .fl-tip-type{display:inline-block;margin-top:9px;font-size:10.5px;font-weight:600;letter-spacing:.04em;text-transform:uppercase;padding:2px 9px;border-radius:999px}
  .t-changed{background:rgba(86,180,233,.16);color:#9fd4f2}
  .t-untested{background:rgba(230,159,0,.16);color:#f0c266}
  .t-tested{background:rgba(0,158,115,.16);color:#5fd3b2}

  /* ---------- toolbar ---------- */
  .fl-tools{position:absolute;right:16px;bottom:54px;display:flex;flex-direction:column;gap:6px;z-index:30}
  .fl-tools button{width:34px;height:34px;border-radius:9px;border:1px solid var(--edge);background:var(--panel);color:var(--text);font-size:18px;line-height:1;cursor:pointer;display:flex;align-items:center;justify-content:center;font-family:var(--sans);transition:border-color .12s ease,color .12s ease}
  .fl-tools button:hover{border-color:var(--changed);color:#9fd4f2}
  .fl-tools button:active{transform:translateY(1px)}
  .fl-reset svg{display:block}

  /* ---------- footer ---------- */
  .fl-foot{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:9px 22px;border-top:1px solid var(--edge);font-size:11.5px;color:var(--muted);font-family:var(--mono);background:#0d1117}
  .fl-foot .det{display:inline-flex;align-items:center;gap:8px}
  .fl-foot .det b{width:6px;height:6px;border-radius:50%;background:var(--tested);display:inline-block}
  .fl-foot .hint{color:#5b636e}
</style>
</head>
<body>
  <div class="fl-app">

    <header class="fl-head">
      <div class="fl-brand">
        <div class="fl-mark"><i class="m1"></i><i class="m2"></i><i class="m3"></i></div>
        <div class="fl-titles">
          <div class="fl-title"><span>Faultline</span><span class="sep">/</span><span class="sub">blast radius</span></div>
          <div class="fl-desc">Everything this change reaches through the call &amp; inheritance graph.</div>
        </div>
      </div>
      <div class="fl-right">
        <div class="fl-verdict"><span class="dot"><b></b></span><span id="fl-verdict-text">untested in blast radius</span></div>
        <div class="fl-legend">
          <span class="fl-chip"><svg width="14" height="14" viewBox="0 0 14 14"><circle cx="7" cy="7" r="6" fill="#56B4E9"></circle></svg>Changed</span>
          <span class="fl-chip"><svg width="15" height="15" viewBox="0 0 15 15"><rect x="2.6" y="2.6" width="9.8" height="9.8" rx="1.5" transform="rotate(45 7.5 7.5)" fill="none" stroke="#E69F00" stroke-width="2"></rect></svg>Untested</span>
          <span class="fl-chip"><svg width="14" height="14" viewBox="0 0 14 14"><rect x="2" y="2" width="10" height="10" rx="1.5" fill="none" stroke="#009E73" stroke-width="2"></rect></svg>Impacted · tested</span>
        </div>
      </div>
    </header>

    <div class="fl-canvas">
      <svg id="fl-svg" class="fl-svg" viewBox="0 0 960 640" preserveAspectRatio="xMidYMid meet" aria-label="Blast radius graph">
        <defs>
          <marker id="fl-arrow" markerWidth="8" markerHeight="8" refX="7" refY="3" orient="auto" markerUnits="userSpaceOnUse">
            <path d="M0,0 L7,3 L0,6 Z" fill="#6e7681"></path>
          </marker>
          <marker id="fl-arrow-lit" markerWidth="8" markerHeight="8" refX="7" refY="3" orient="auto" markerUnits="userSpaceOnUse">
            <path d="M0,0 L7,3 L0,6 Z" fill="#adbac7"></path>
          </marker>
        </defs>
        <rect id="fl-bg" x="-3000" y="-3000" width="7000" height="7000" fill="transparent" pointer-events="all"></rect>
        <g id="fl-viewport">
          <g id="fl-edges"></g>
          <g id="fl-nodes"></g>
        </g>
      </svg>

      <div class="fl-tools">
        <button id="fl-zin" title="Zoom in" aria-label="Zoom in">+</button>
        <button id="fl-zout" title="Zoom out" aria-label="Zoom out">&#8722;</button>
        <button id="fl-zreset" class="fl-reset" title="Reset view" aria-label="Reset view">
          <svg width="15" height="15" viewBox="0 0 15 15" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M12.5 6.2A5.2 5.2 0 1 0 13 9"></path><path d="M12.7 2.4v3.8H8.9"></path></svg>
        </button>
      </div>
    </div>

    <footer class="fl-foot">
      <span class="det"><b></b>Layout computed deterministically — byte-identical every run.</span>
      <span class="hint" id="fl-foot-stats">scroll to zoom · drag to explore</span>
    </footer>
  </div>

  <div class="fl-tip" id="fl-tip">
    <div class="fl-tip-name" id="fl-tip-name">symbol</div>
    <div class="fl-tip-meta" id="fl-tip-meta">file · role · hop</div>
    <span class="fl-tip-type t-tested" id="fl-tip-type">type</span>
  </div>

  <!-- Data injection point: the placeholder below is replaced server-side with HTML-escaped JSON. -->
  <script id="fl-data" type="application/json">__DATA__</script>

  <script>
  (function(){
    "use strict";
    var NS = "http://www.w3.org/2000/svg";

    /* Fallback sample so the file renders standalone before server substitution.
       In production the placeholder above is replaced; this branch is never taken. */
    var SAMPLE = {
      meta:{ changed:"tax_rate", untested:2, total:8, maxDepth:5 },
      nodes:[
        {id:"tax_rate",   label:"tax_rate",   x:120, y:322, type:"changed",  file:"pricing/rates.py",   role:"rate constant", hops:0},
        {id:"price_of",   label:"price_of",   x:300, y:222, type:"tested",   file:"pricing/price.py",   role:"pure function", hops:1},
        {id:"line_total", label:"line_total", x:300, y:430, type:"tested",   file:"billing/line.py",    role:"pure function", hops:1},
        {id:"cart_total", label:"cart_total", x:470, y:196, type:"tested",   file:"cart/total.py",      role:"aggregator",    hops:2},
        {id:"audit_row",  label:"audit_row",  x:470, y:476, type:"tested",   file:"audit/row.py",       role:"writer",        hops:2},
        {id:"order_total",label:"order_total",x:632, y:330, type:"tested",   file:"orders/total.py",    role:"aggregator",    hops:3},
        {id:"tax_report", label:"tax_report", x:792, y:242, type:"untested", file:"reports/tax.py",     role:"report builder",hops:4},
        {id:"invoice_pdf",label:"invoice_pdf",x:836, y:452, type:"untested", file:"invoices/pdf.py",    role:"renderer",      hops:5}
      ],
      edges:[
        {from:"tax_rate",   to:"price_of"},
        {from:"tax_rate",   to:"line_total"},
        {from:"price_of",   to:"cart_total"},
        {from:"line_total", to:"audit_row"},
        {from:"cart_total", to:"order_total"},
        {from:"audit_row",  to:"order_total"},
        {from:"order_total",to:"tax_report"},
        {from:"tax_report", to:"invoice_pdf"}
      ]
    };

    function getData(){
      var holder = document.getElementById("fl-data");
      var raw = holder ? holder.textContent : "";
      var sentinel = "__" + "DATA" + "__";
      if(!raw || raw.replace(/\s/g, "") === sentinel){ return SAMPLE; }
      try { return JSON.parse(raw); } catch(err){ return SAMPLE; }
    }

    function cel(name, attrs){
      var e = document.createElementNS(NS, name);
      if(attrs){ for(var k in attrs){ if(attrs.hasOwnProperty(k)){ e.setAttribute(k, attrs[k]); } } }
      return e;
    }
    function clamp(v,a,b){ return v<a?a:(v>b?b:v); }

    var data   = getData();
    var svg    = document.getElementById("fl-svg");
    var vp     = document.getElementById("fl-viewport");
    var gEdges = document.getElementById("fl-edges");
    var gNodes = document.getElementById("fl-nodes");
    var bg     = document.getElementById("fl-bg");
    var tip    = document.getElementById("fl-tip");

    var nodeById = {};
    data.nodes.forEach(function(n){ nodeById[n.id] = n; });

    /* adjacency (used only for hover highlight — not for layout) */
    var nbr = {};
    data.nodes.forEach(function(n){ nbr[n.id] = { nodes:{}, edges:[] }; });
    data.edges.forEach(function(e,i){
      if(nbr[e.from]){ nbr[e.from].nodes[e.to] = 1; nbr[e.from].edges.push(i); }
      if(nbr[e.to]){   nbr[e.to].nodes[e.from] = 1; nbr[e.to].edges.push(i); }
    });

    function radiusOf(n){ return n.type === "changed" ? 26 : 21; }

    /* ----- build edges ----- */
    var edgeEls = [];
    data.edges.forEach(function(e,i){
      var ln = cel("line", { "class":"fl-edge", "marker-end":"url(#fl-arrow)" });
      ln.setAttribute("data-i", i);
      gEdges.appendChild(ln);
      edgeEls.push(ln);
    });
    function redrawEdges(){
      data.edges.forEach(function(e,i){
        var a = nodeById[e.from], b = nodeById[e.to];
        if(!a || !b){ return; }
        var dx = b.x - a.x, dy = b.y - a.y;
        var L = Math.sqrt(dx*dx + dy*dy) || 1;
        var ux = dx/L, uy = dy/L;
        var r1 = radiusOf(a) + 4, r2 = radiusOf(b) + 11;
        var ln = edgeEls[i];
        ln.setAttribute("x1", a.x + ux*r1);
        ln.setAttribute("y1", a.y + uy*r1);
        ln.setAttribute("x2", b.x - ux*r2);
        ln.setAttribute("y2", b.y - uy*r2);
      });
    }

    /* ----- build nodes ----- */
    var nodeEls = {};
    data.nodes.forEach(function(n){
      var g = cel("g", { "class":"fl-node fl-node--" + n.type, "data-id":n.id });
      g.setAttribute("transform", "translate(" + n.x + "," + n.y + ")");
      if(n.type === "changed"){
        g.appendChild(cel("circle", { "class":"fl-ring",  "r":27 }));
        g.appendChild(cel("circle", { "class":"fl-shape", "r":17 }));
        g.appendChild(cel("circle", { "class":"fl-core",  "r":6, "cx":-4, "cy":-4 }));
      } else if(n.type === "untested"){
        g.appendChild(cel("rect", { "class":"fl-shape", "x":-15, "y":-15, "width":30, "height":30, "rx":4, "transform":"rotate(45)" }));
      } else {
        g.appendChild(cel("rect", { "class":"fl-shape", "x":-15, "y":-15, "width":30, "height":30, "rx":4 }));
      }
      var label = cel("text", { "class":"fl-label", "y":42, "text-anchor":"middle" });
      label.textContent = n.label;
      g.appendChild(label);
      gNodes.appendChild(g);
      nodeEls[n.id] = g;
      attachNodeEvents(g, n);
    });
    redrawEdges();

    /* ----- view transform (zoom / pan) ----- */
    var view = { s:1, tx:0, ty:0 };
    function applyView(){ vp.setAttribute("transform", "translate(" + view.tx + "," + view.ty + ") scale(" + view.s + ")"); }
    applyView();

    var ptSvg = svg.createSVGPoint();
    function clientToView(cx, cy){
      ptSvg.x = cx; ptSvg.y = cy;
      var ctm = svg.getScreenCTM();
      if(!ctm){ return { x:cx, y:cy }; }
      var p = ptSvg.matrixTransform(ctm.inverse());
      return { x:p.x, y:p.y };
    }
    function zoomAround(cx, cy, factor){
      var ns = clamp(view.s * factor, 0.45, 3.2);
      var f = ns / view.s;
      view.tx = cx - f * (cx - view.tx);
      view.ty = cy - f * (cy - view.ty);
      view.s = ns;
      applyView();
    }

    svg.addEventListener("wheel", function(ev){
      ev.preventDefault();
      var c = clientToView(ev.clientX, ev.clientY);
      zoomAround(c.x, c.y, ev.deltaY < 0 ? 1.12 : 1/1.12);
    }, { passive:false });

    document.getElementById("fl-zin").addEventListener("click", function(){ zoomAround(480, 320, 1.2); });
    document.getElementById("fl-zout").addEventListener("click", function(){ zoomAround(480, 320, 1/1.2); });
    document.getElementById("fl-zreset").addEventListener("click", function(){ view.s=1; view.tx=0; view.ty=0; applyView(); });

    /* ----- drag: pan background + move node ----- */
    var dragNode = null, panning = null, dragging = false;

    bg.addEventListener("mousedown", function(ev){
      ev.preventDefault();
      var c = clientToView(ev.clientX, ev.clientY);
      panning = { cx:c.x, cy:c.y, tx0:view.tx, ty0:view.ty };
      svg.classList.add("grabbing");
      clearHighlight(); hideTip();
    });

    function attachNodeEvents(g, n){
      g.addEventListener("mouseenter", function(ev){ if(dragging){ return; } highlight(n.id); showTip(n); moveTip(ev); });
      g.addEventListener("mousemove", function(ev){ if(!dragging){ moveTip(ev); } });
      g.addEventListener("mouseleave", function(){ if(dragging){ return; } clearHighlight(); hideTip(); });
      g.addEventListener("mousedown", function(ev){
        ev.preventDefault(); ev.stopPropagation();
        var c = clientToView(ev.clientX, ev.clientY);
        var gx = (c.x - view.tx) / view.s, gy = (c.y - view.ty) / view.s;
        dragNode = { n:n, g:g, offx:gx - n.x, offy:gy - n.y };
        dragging = true;
        svg.classList.add("grabbing");
        clearHighlight(); hideTip();
      });
    }

    window.addEventListener("mousemove", function(ev){
      if(dragNode){
        var c = clientToView(ev.clientX, ev.clientY);
        var gx = (c.x - view.tx) / view.s, gy = (c.y - view.ty) / view.s;
        dragNode.n.x = gx - dragNode.offx;
        dragNode.n.y = gy - dragNode.offy;
        dragNode.g.setAttribute("transform", "translate(" + dragNode.n.x + "," + dragNode.n.y + ")");
        redrawEdges();
      } else if(panning){
        var p = clientToView(ev.clientX, ev.clientY);
        view.tx = panning.tx0 + (p.x - panning.cx);
        view.ty = panning.ty0 + (p.y - panning.cy);
        applyView();
      }
    });
    window.addEventListener("mouseup", function(){
      dragNode = null; panning = null; dragging = false;
      svg.classList.remove("grabbing");
    });

    /* ----- hover highlight (DOM class toggles only) ----- */
    function highlight(id){
      svg.classList.add("is-hover");
      var self = nodeEls[id];
      if(self){ self.classList.add("lit"); }
      var nb = nbr[id];
      if(nb){
        Object.keys(nb.nodes).forEach(function(k){ if(nodeEls[k]){ nodeEls[k].classList.add("lit"); } });
        nb.edges.forEach(function(i){ edgeEls[i].classList.add("lit"); edgeEls[i].setAttribute("marker-end","url(#fl-arrow-lit)"); });
      }
    }
    function clearHighlight(){
      svg.classList.remove("is-hover");
      data.nodes.forEach(function(n){ var el = nodeEls[n.id]; if(el){ el.classList.remove("lit"); } });
      edgeEls.forEach(function(e){ e.classList.remove("lit"); e.setAttribute("marker-end","url(#fl-arrow)"); });
    }

    /* ----- tooltip ----- */
    var tipName = document.getElementById("fl-tip-name");
    var tipMeta = document.getElementById("fl-tip-meta");
    var tipType = document.getElementById("fl-tip-type");
    function typeLabel(t){ return t === "changed" ? "Changed seed" : (t === "untested" ? "Untested" : "Impacted · tested"); }
    function showTip(n){
      tipName.textContent = n.label;
      var parts = [n.file || "—"];
      if(n.role){ parts.push(n.role); }
      parts.push("hop " + n.hops);
      tipMeta.textContent = parts.join("  ·  ");
      tipType.textContent = typeLabel(n.type);
      tipType.className = "fl-tip-type t-" + n.type;
      tip.classList.add("on");
    }
    function moveTip(ev){
      var pad = 16;
      var w = tip.offsetWidth, h = tip.offsetHeight;
      var x = ev.clientX + pad, y = ev.clientY + pad;
      if(x + w > window.innerWidth - 8){ x = ev.clientX - pad - w; }
      if(y + h > window.innerHeight - 8){ y = ev.clientY - pad - h; }
      tip.style.left = x + "px";
      tip.style.top = y + "px";
    }
    function hideTip(){ tip.classList.remove("on"); }

    /* ----- header verdict + footer stats from data.meta (facts only) ----- */
    var meta = data.meta || {};
    var nUn = (typeof meta.untested === "number") ? meta.untested
              : data.nodes.filter(function(n){ return n.type === "untested"; }).length;
    var vEl = document.getElementById("fl-verdict-text");
    var vWrap = document.querySelector(".fl-verdict");
    if(nUn > 0){
      vEl.textContent = nUn + " untested in blast radius";
    } else {
      vEl.textContent = "no untested impact";
      if(vWrap){ vWrap.classList.add("clear"); }
    }
    var total = (typeof meta.total === "number") ? meta.total : data.nodes.length;
    var depth = (typeof meta.maxDepth === "number") ? meta.maxDepth : 0;
    document.getElementById("fl-foot-stats").textContent = total + " nodes · max depth " + depth + " · scroll to zoom · drag to explore";
  })();
  </script>
</body>
</html>
`
