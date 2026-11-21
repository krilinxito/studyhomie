package web

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/krilinxito/studyhomie/internal/i18n"
	"github.com/krilinxito/studyhomie/internal/vault"
	"github.com/krilinxito/studyhomie/internal/viz"
)

var (
	headOpenRe      = regexp.MustCompile(`(?i)<head\b[^>]*>`)
	htmlOpenRe      = regexp.MustCompile(`(?i)<html\b[^>]*>`)
	scriptCloseRe   = regexp.MustCompile(`(?i)</script`)
	styleCloseRe    = regexp.MustCompile(`(?i)</style`)
	katexFontFaceRe = regexp.MustCompile(`(?s)@font-face\s*\{[^}]*\}`)
)

// interactive serves a generated simulation locked down in an opaque sandboxed origin.
// Bundled libraries requested via <meta name="studyhomie-libs"> are injected inline from
// staticFS so they work offline without weakening the sandbox (never allow-same-origin).
// Approved CDN libraries widen only script-src/style-src (and map tiles for Leaflet).
func (s *Server) interactive(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	slug := strings.TrimSuffix(file, ".html")
	if !strings.HasSuffix(file, ".html") || vault.Slugify(slug) != slug || slug == "" {
		http.NotFound(w, r)
		return
	}
	raw, err := s.vault().LoadInteractive(slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	theme := r.URL.Query().Get("theme")
	if theme != "dark" && theme != "light" {
		theme = ""
	}
	_, cfg, _ := s.backend()
	lang := i18n.UI(cfg.Language)

	cdn, tiles := viz.UsesCDN(raw)
	rendered := renderInteractiveHTML(raw, theme, lang)

	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", interactiveCSP(cdn, tiles))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-cache")
	io.WriteString(w, rendered)
}

func interactiveCSP(cdn, tiles bool) string {
	scriptSrc := "'unsafe-inline'"
	styleSrc := "'unsafe-inline'"
	imgSrc := "data: blob:"
	fontSrc := "data:"
	if cdn {
		origins := " https://cdnjs.cloudflare.com https://cdn.jsdelivr.net"
		scriptSrc += origins
		styleSrc += origins
		imgSrc += origins
		fontSrc += origins
	}
	if tiles {
		imgSrc += " " + strings.Join(viz.AllowedTileHosts, " ")
	}
	return "sandbox allow-scripts; default-src 'none'; script-src " + scriptSrc +
		"; style-src " + styleSrc +
		"; img-src " + imgSrc +
		"; font-src " + fontSrc +
		"; worker-src blob:; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"
}

func renderInteractiveHTML(raw, theme, lang string) string {
	libs, _ := viz.ParseLibs(raw)
	prelude := buildInteractivePrelude(libs, theme, lang)
	if loc := headOpenRe.FindStringIndex(raw); loc != nil {
		return raw[:loc[1]] + "\n" + prelude + raw[loc[1]:]
	}
	if loc := htmlOpenRe.FindStringIndex(raw); loc != nil {
		return raw[:loc[1]] + "\n<head>\n" + prelude + "</head>\n" + raw[loc[1]:]
	}
	return prelude + raw
}

func buildInteractivePrelude(libs []string, theme, lang string) string {
	var b strings.Builder
	b.WriteString(`<style id="sh-base">` + shBaseCSS + "</style>\n")
	b.WriteString(`<script id="sh-bridge">` + shBridgeJS(theme, lang) + "</script>\n")

	for _, name := range libs {
		lib, ok := viz.Libraries[name]
		if !ok {
			continue
		}
		if lib.Bundled {
			for _, cssPath := range lib.Styles {
				if data, err := staticFS.ReadFile("static/" + cssPath); err == nil {
					css := string(data)
					if name == "katex" {
						// Relative font URLs cannot load in an opaque sandbox origin; drop @font-face to avoid 404s.
						css = katexFontFaceRe.ReplaceAllString(css, "")
					}
					css = styleCloseRe.ReplaceAllString(css, `<\/style`)
					b.WriteString(`<style data-sh-lib="` + name + `">` + css + "</style>\n")
				}
			}
			for _, jsPath := range lib.Scripts {
				if data, err := staticFS.ReadFile("static/" + jsPath); err == nil {
					js := scriptCloseRe.ReplaceAllString(string(data), `<\/script`)
					b.WriteString(`<script data-sh-lib="` + name + `">` + js + "</script>\n")
				}
			}
		} else {
			for _, cssURL := range lib.Styles {
				b.WriteString(`<link rel="stylesheet" href="` + cssURL + `">` + "\n")
			}
			for _, jsURL := range lib.Scripts {
				b.WriteString(`<script src="` + jsURL + `" onerror="window.__shOffline('` + name + `')"></script>` + "\n")
			}
		}
	}
	return b.String()
}

const shBaseCSS = `
:root {
  --sh-bg: #f6f5f9;
  --sh-stage: #fbfafd;
  --sh-surface: #ffffff;
  --sh-surface-2: #f1eff6;
  --sh-ink: #1a1722;
  --sh-muted: #686277;
  --sh-line: rgba(34, 24, 62, .12);
  --sh-grid: rgba(46, 32, 72, .075);
  --sh-grid-major: rgba(46, 32, 72, .14);
  --sh-accent: #6d52c7;
  --sh-accent-2: #8c72df;
  --sh-accent-soft: rgba(109, 82, 199, .12);
  --sh-blue: #2563eb;
  --sh-teal: #0d9488;
  --sh-amber: #d97706;
  --sh-ok: #0d9488;
  --sh-warn: #d97706;
  --sh-danger: #dc2626;
  --sh-font: "Inter var", Inter, system-ui, -apple-system, "Segoe UI", Roboto, "PingFang SC", "Microsoft YaHei", sans-serif;
  --sh-mono: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  color-scheme: light;
}
:root[data-theme="dark"] {
  --sh-bg: #111217;
  --sh-stage: #15171e;
  --sh-surface: #1b1e27;
  --sh-surface-2: #242834;
  --sh-ink: #edf0f7;
  --sh-muted: #939bb0;
  --sh-line: rgba(255, 255, 255, .10);
  --sh-grid: rgba(255, 255, 255, .045);
  --sh-grid-major: rgba(255, 255, 255, .09);
  --sh-accent: #a792f2;
  --sh-accent-2: #c5b5fa;
  --sh-accent-soft: rgba(167, 146, 242, .15);
  --sh-blue: #60a5fa;
  --sh-teal: #2dd4bf;
  --sh-amber: #fbbf24;
  --sh-ok: #2dd4bf;
  --sh-warn: #fbbf24;
  --sh-danger: #f87171;
  color-scheme: dark;
}
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
    --sh-bg: #111217;
    --sh-stage: #15171e;
    --sh-surface: #1b1e27;
    --sh-surface-2: #242834;
    --sh-ink: #edf0f7;
    --sh-muted: #939bb0;
    --sh-line: rgba(255, 255, 255, .10);
    --sh-grid: rgba(255, 255, 255, .045);
    --sh-grid-major: rgba(255, 255, 255, .09);
    --sh-accent: #a792f2;
    --sh-accent-2: #c5b5fa;
    --sh-accent-soft: rgba(167, 146, 242, .15);
    --sh-blue: #60a5fa;
    --sh-teal: #2dd4bf;
    --sh-amber: #fbbf24;
    --sh-ok: #2dd4bf;
    --sh-warn: #fbbf24;
    --sh-danger: #f87171;
    color-scheme: dark;
  }
}
*, *::before, *::after { box-sizing: border-box; }
html, body { margin: 0; background: var(--sh-bg); color: var(--sh-ink); font: 13.5px/1.45 var(--sh-font); -webkit-font-smoothing: antialiased; font-variant-numeric: tabular-nums; }

/* Studio Instrument primitives: native controls look crisp and hardware-inspired by default */
button, select {
  font: 550 12.5px/1 var(--sh-font); color: var(--sh-ink);
  background: var(--sh-surface-2); border: 1px solid var(--sh-line);
  border-radius: 7px; padding: 7px 11px; cursor: pointer;
  transition: background-color .14s, border-color .14s, color .14s, transform .1s;
}
button:hover:not(:disabled), select:hover:not(:disabled) { border-color: color-mix(in srgb, var(--sh-ink) 28%, transparent); }
button:active:not(:disabled) { transform: translateY(1px); }
button:disabled { opacity: .45; cursor: default; }

input[type="range"] {
  -webkit-appearance: none; appearance: none; background: transparent; cursor: pointer; height: 22px; min-width: 90px;
}
input[type="range"]::-webkit-slider-runnable-track {
  height: 4px; border-radius: 99px; background: var(--sh-line);
}
input[type="range"]::-webkit-slider-thumb {
  -webkit-appearance: none; width: 14px; height: 14px; margin-top: -5px; border-radius: 50%;
  background: var(--sh-ink); border: 2px solid var(--sh-surface);
  box-shadow: 0 1px 4px rgba(0, 0, 0, .35); transition: transform .12s, background-color .12s;
}
input[type="range"]:hover::-webkit-slider-thumb { background: var(--sh-accent); transform: scale(1.12); }
input[type="range"]::-moz-range-track { height: 4px; border-radius: 99px; background: var(--sh-line); }
input[type="range"]::-moz-range-thumb {
  width: 14px; height: 14px; border-radius: 50%; background: var(--sh-ink); border: 2px solid var(--sh-surface);
}

/* Studio layout blocks used across labs */
.lab { display: flex; flex-direction: column; width: 100%; background: var(--sh-bg); color: var(--sh-ink); }
.lab-bar {
  display: flex; align-items: center; justify-content: space-between; gap: 10px; flex-wrap: wrap;
  padding: 9px 14px; background: var(--sh-surface); border-bottom: 1px solid var(--sh-line); min-height: 44px;
}
.lab-goal { font-size: 13px; color: var(--sh-muted); font-weight: 500; }
.lab-goal strong { color: var(--sh-ink); font-weight: 620; }
.lab-group { display: inline-flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.lab-btn { display: inline-flex; align-items: center; gap: 6px; padding: 6px 11px; border-radius: 7px; font-weight: 600; }
.lab-btn.primary { background: var(--sh-teal); border-color: transparent; color: #081c1a; }
.lab-btn.primary:hover { filter: brightness(1.06); border-color: transparent; }
.lab-btn.active { background: var(--sh-accent-soft); border-color: var(--sh-accent); color: var(--sh-accent); }
.lab-seg { display: inline-flex; padding: 2px; border-radius: 8px; background: var(--sh-bg); border: 1px solid var(--sh-line); gap: 2px; }
.lab-seg button { border: 0; background: transparent; padding: 5px 10px; border-radius: 6px; color: var(--sh-muted); }
.lab-seg button[aria-pressed="true"], .lab-seg button.active { background: var(--sh-surface-2); color: var(--sh-ink); }
.lab-pill { display: inline-flex; align-items: center; gap: 6px; padding: 4px 9px; border-radius: 7px; background: var(--sh-bg); border: 1px solid var(--sh-line); font: 600 12px var(--sh-mono); }

.lab-stage {
  position: relative; width: 100%; min-height: 320px; background-color: var(--sh-stage);
  touch-action: none; user-select: none; overflow: hidden;
}
.lab-grid {
  background-image:
    linear-gradient(var(--sh-grid-major) 1px, transparent 1px),
    linear-gradient(90deg, var(--sh-grid-major) 1px, transparent 1px),
    linear-gradient(var(--sh-grid) 1px, transparent 1px),
    linear-gradient(90deg, var(--sh-grid) 1px, transparent 1px);
  background-size: 96px 96px, 96px 96px, 24px 24px, 24px 24px;
}
.lab-dots {
  background-image: radial-gradient(var(--sh-grid-major) 1.1px, transparent 1.2px);
  background-size: 20px 20px;
}

.lab-hud {
  display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  background: var(--sh-surface); border-top: 1px solid var(--sh-line);
}
.lab-metric {
  padding: 11px 14px; border-right: 1px solid var(--sh-line); border-bottom: 1px solid var(--sh-line);
  display: flex; flex-direction: column; gap: 3px; min-width: 0;
}
.lab-metric:last-child { border-right: 0; }
.lab-metric.is-ok { background: color-mix(in srgb, var(--sh-ok) 10%, var(--sh-surface)); box-shadow: inset 2.5px 0 0 var(--sh-ok); }
.lab-metric.is-warn { background: color-mix(in srgb, var(--sh-warn) 10%, var(--sh-surface)); box-shadow: inset 2.5px 0 0 var(--sh-warn); }
.lab-metric.is-accent { background: color-mix(in srgb, var(--sh-accent) 10%, var(--sh-surface)); box-shadow: inset 2.5px 0 0 var(--sh-accent); }
.lab-k { font: 650 10px/1.2 var(--sh-mono); letter-spacing: .09em; text-transform: uppercase; color: var(--sh-muted); }
.lab-v { font: 650 14.5px/1.3 var(--sh-mono); color: var(--sh-ink); }
.lab-s { font: 500 11.5px/1.35 var(--sh-font); color: var(--sh-muted); }
.lab-dock {
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
  padding: 9px 14px; background: var(--sh-bg); border-top: 1px solid var(--sh-line);
}

.sh-offline-banner {
  margin: 16px; padding: 14px 16px; border-radius: 10px;
  background: var(--sh-surface); border: 1px solid var(--sh-warn);
  color: var(--sh-ink); font: 13.5px/1.5 var(--sh-font);
}
.sh-offline-banner strong { color: var(--sh-warn); display: block; margin-bottom: 4px; }
`

func shBridgeJS(theme, lang string) string {
	offlineTitle, _ := json.Marshal(i18n.T(lang, "Este visual necesita conexión a internet"))
	offlineBody, _ := json.Marshal(i18n.T(lang, "No se pudo cargar la librería externa ({lib}). Cuando tengas conexión, recarga el visual."))
	themeJSON, _ := json.Marshal(theme)
	return `(function(){` +
		`var t=` + string(themeJSON) + `;` +
		`if(t==="dark"||t==="light"){document.documentElement.dataset.theme=t;}` +
		`var offline=false,reported=false;` +
		`window.__shOffline=function(lib){` +
		`  offline=true;` +
		`  try{window.parent.postMessage({type:"sh-viz-offline",lib:lib},"*");}catch(e){}` +
		`  function show(){` +
		`    if(!document.body||document.querySelector(".sh-offline-banner"))return;` +
		`    var d=document.createElement("div");d.className="sh-offline-banner";` +
		`    var s=document.createElement("strong");s.textContent=` + string(offlineTitle) + `;` +
		`    var p=document.createElement("span");p.textContent=(` + string(offlineBody) + `).replace("{lib}",lib||"CDN");` +
		`    d.appendChild(s);d.appendChild(p);document.body.prepend(d);` +
		`  }` +
		`  if(document.readyState==="loading")document.addEventListener("DOMContentLoaded",show);else show();` +
		`};` +
		`function reportErr(msg){` +
		`  if(offline||reported||!msg)return;reported=true;` +
		`  try{window.parent.postMessage({type:"sh-viz-error",message:String(msg).slice(0,400)},"*");}catch(e){}` +
		`}` +
		`window.addEventListener("error",function(e){` +
		`  if(e&&e.target&&e.target.tagName==="SCRIPT")return;` +
		`  var m=(e&&e.message)||"Runtime error";` +
		`  if(e&&e.lineno)m+=" (line "+e.lineno+")";` +
		`  reportErr(m);` +
		`});` +
		`window.addEventListener("unhandledrejection",function(e){` +
		`  var r=e&&e.reason;reportErr((r&&r.message)||String(r||"Unhandled promise rejection"));` +
		`});` +
		`window.addEventListener("message",function(e){` +
		`  if(!e||!e.data||e.data.type!=="sh-theme")return;` +
		`  var next=e.data.theme;` +
		`  if(next==="dark"||next==="light"){` +
		`    document.documentElement.dataset.theme=next;` +
		`    try{window.dispatchEvent(new CustomEvent("sh-theme-change",{detail:next}));}catch(err){}` +
		`  }` +
		`});` +
		`var lastH=0,timer=0;` +
		`function postH(){` +
		`  if(!document.body)return;` +
		`  var h=Math.ceil(Math.max(document.body.scrollHeight,document.body.offsetHeight));` +
		`  if(h>120&&Math.abs(h-lastH)>12){lastH=h;try{window.parent.postMessage({type:"sh-viz-height",height:h},"*");}catch(e){}}` +
		`}` +
		`function scheduleH(){clearTimeout(timer);timer=setTimeout(postH,80);}` +
		`window.addEventListener("load",function(){` +
		`  postH();` +
		`  if(typeof ResizeObserver==="function"&&document.body){new ResizeObserver(scheduleH).observe(document.body);}` +
		`});` +
		`})();`
}
