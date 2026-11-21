package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/vault"
)

func TestInteractiveInjectsBundledLibrariesOffline(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	raw := `<!doctype html><html><head><meta name="studyhomie-libs" content="three, tone, jsxgraph, katex"></head><body><script>new THREE.Scene();</script></body></html>`
	if err := s.V.SaveInteractive("lab", raw); err != nil {
		t.Fatal(err)
	}
	res, body := do(t, ts, "GET", "/interactive/lab.html?theme=dark", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	html := string(body)
	for _, want := range []string{
		`id="sh-base"`,
		`id="sh-bridge"`,
		`var t="dark"`,
		`data-sh-lib="three"`,
		`data-sh-lib="tone"`,
		`data-sh-lib="jsxgraph"`,
		`data-sh-lib="katex"`,
		"OrbitControls",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered widget lacks %q", want)
		}
	}
	csp := res.Header.Get("Content-Security-Policy")
	// Bundled libraries keep the strictest offline CSP (no CDN origins needed).
	if strings.Contains(csp, "cdnjs.cloudflare.com") || strings.Contains(csp, "allow-same-origin") {
		t.Errorf("bundled-only widget CSP should stay offline and opaque: %s", csp)
	}
	if !strings.Contains(csp, "sandbox allow-scripts") || !strings.Contains(csp, "connect-src 'none'") || !strings.Contains(csp, "worker-src blob:") {
		t.Errorf("missing sandbox/connect-src/worker-src in CSP: %s", csp)
	}
}

func TestInteractiveCDNCspAndOfflineHandler(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	raw := `<!doctype html><html><head><meta name="studyhomie-libs" content="leaflet, matter"></head><body><div id="m"></div><script>if(typeof L!=='undefined')L.map('m');</script></body></html>`
	if err := s.V.SaveInteractive("mapa", raw); err != nil {
		t.Fatal(err)
	}
	res, body := do(t, ts, "GET", "/interactive/mapa.html?theme=light", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	html := string(body)
	for _, want := range []string{
		`cdnjs.cloudflare.com/ajax/libs/leaflet/1.9.4/leaflet.min.js`,
		`cdnjs.cloudflare.com/ajax/libs/matter-js/0.20.0/matter.min.js`,
		`window.__shOffline('leaflet')`,
		`window.__shOffline('matter')`,
		`var t="light"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("CDN widget lacks %q", want)
		}
	}
	csp := res.Header.Get("Content-Security-Policy")
	for _, want := range []string{
		"sandbox allow-scripts",
		"https://cdnjs.cloudflare.com",
		"https://cdn.jsdelivr.net",
		"https://*.tile.openstreetmap.org",
		"connect-src 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CDN widget CSP lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "allow-same-origin") {
		t.Errorf("must never include allow-same-origin: %s", csp)
	}
}

func TestVizEndpointRegeneratesAndRepairsErrors(t *testing.T) {
	broken := "```html\n<!doctype html><html><head><meta name=\"studyhomie-libs\" content=\"tone\"></head><body><script>brokenCall();</script></body></html>\n```"
	fixed := "```html\n<!doctype html><html><head><meta name=\"studyhomie-libs\" content=\"tone\"></head><body><script>const ok = true;</script></body></html>\n```"
	p := &scripted{replies: []string{broken, fixed}}
	s, ts := newTestServer(t, p)
	note := vault.Note{
		Slug: "escalas",
		Meta: vault.Meta{Title: "Escalas menores", Status: "prepared", Area: "musica"},
		Body: "# Escalas menores\n\n## Guía\n\n```viz\n{\"type\":\"html\",\"brief\":\"piano interactivo en Si menor\"}\n```\n",
	}
	if err := s.V.Save(note); err != nil {
		t.Fatal(err)
	}

	// First build
	res, body := do(t, ts, "POST", "/api/notes/escalas/viz", map[string]any{"brief": "piano interactivo en Si menor"}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("build status %d: %s", res.StatusCode, body)
	}
	var out struct {
		Src string `json:"src"`
	}
	json.Unmarshal(body, &out)
	if !strings.HasPrefix(out.Src, "interactive/escalas-") {
		t.Fatalf("unexpected src: %q", out.Src)
	}
	if len(p.reqs) != 1 || !strings.Contains(p.reqs[0].Prompt, "AREA: musica") {
		t.Errorf("first prompt must carry AREA: musica, got %+v", p.reqs)
	}

	// Runtime error auto-repair
	res, body = do(t, ts, "POST", "/api/notes/escalas/viz", map[string]any{
		"brief":    "piano interactivo en Si menor",
		"fixError": "ReferenceError: brokenCall is not defined (line 1)",
	}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("fix status %d: %s", res.StatusCode, body)
	}
	if len(p.reqs) != 2 || !strings.Contains(p.reqs[1].Prompt, "brokenCall is not defined") || !strings.Contains(p.reqs[1].Prompt, "brokenCall();") {
		t.Errorf("fix prompt must include error and previous HTML: %+v", p.reqs[1].Prompt)
	}
	// Serving the fixed page returns the updated script.
	_, served := do(t, ts, "GET", "/"+out.Src, nil, nil)
	if !strings.Contains(string(served), "const ok = true;") {
		t.Errorf("repaired HTML not served: %s", served)
	}
	_ = context.Background()
}
