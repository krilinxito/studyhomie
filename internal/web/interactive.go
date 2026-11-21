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
  --sh-bg: #f7f6f9;
  --sh-surface: #ffffff;
  --sh-surface-2: #f4f2f7;
  --sh-ink: #1d1a24;
  --sh-muted: #6e687b;
  --sh-line: #e4dfec;
  --sh-grid: rgba(46, 32, 72, .08);
  --sh-accent: #7c5fd3;
  --sh-accent-2: #9d84e3;
  --sh-accent-soft: #f1ecfc;
  --sh-ok: #16a34a;
  --sh-warn: #d97706;
  --sh-danger: #dc2626;
  --sh-font: system-ui, -apple-system, "Segoe UI", Roboto, "PingFang SC", "Microsoft YaHei", sans-serif;
  --sh-mono: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  color-scheme: light;
}
:root[data-theme="dark"] {
  --sh-bg: #100e14;
  --sh-surface: #18151d;
  --sh-surface-2: #211d28;
  --sh-ink: #ece8f2;
  --sh-muted: #a09aab;
  --sh-line: #2e2936;
  --sh-grid: rgba(236, 225, 255, .07);
  --sh-accent: #b9a2f2;
  --sh-accent-2: #cfbff7;
  --sh-accent-soft: rgba(185, 162, 242, .14);
  --sh-ok: #34d399;
  --sh-warn: #fbbf24;
  --sh-danger: #f87171;
  color-scheme: dark;
}
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
    --sh-bg: #100e14;
    --sh-surface: #18151d;
    --sh-surface-2: #211d28;
    --sh-ink: #ece8f2;
    --sh-muted: #a09aab;
    --sh-line: #2e2936;
    --sh-grid: rgba(236, 225, 255, .07);
    --sh-accent: #b9a2f2;
    --sh-accent-2: #cfbff7;
    --sh-accent-soft: rgba(185, 162, 242, .14);
    --sh-ok: #34d399;
    --sh-warn: #fbbf24;
    --sh-danger: #f87171;
    color-scheme: dark;
  }
}
html, body { margin: 0; background: var(--sh-bg); color: var(--sh-ink); font-family: var(--sh-font); }
.sh-offline-banner {
  margin: 16px; padding: 14px 16px; border-radius: 12px;
  background: var(--sh-surface); border: 1px solid var(--sh-warn);
  color: var(--sh-ink); font: 14px/1.5 var(--sh-font);
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
