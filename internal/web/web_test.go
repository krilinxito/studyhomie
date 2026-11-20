package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/session"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// scripted replies in order; blocks on "hold" when set so tests can cancel mid-call.
type scripted struct {
	mu      sync.Mutex
	replies []string
	hold    chan struct{}
}

func (f *scripted) Name() string { return "fake" }
func (f *scripted) Complete(ctx context.Context, req provider.Request, _ func(string)) (provider.Response, error) {
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
			return provider.Response{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.replies[0]
	f.replies = f.replies[1:]
	return provider.Response{Text: r, SessionID: "s"}, nil
}

const prep = "La intuición es X.\n\n```json\n{\"resources\":[],\"handoff\":\"resumen\"}\n```"

func newTestServer(t *testing.T, p provider.Provider) (*Server, *httptest.Server) {
	t.Helper()
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Depth, cfg.Iceberg = 0, false // guides without concept notes, so the scripted replies stay in order
	s := New(cfg, v, p)
	// runs save themselves in the background: stop that before the temporary vault is removed
	// (cleanups run last-registered first, so this one runs before TempDir's)
	t.Cleanup(func() {
		if s.store != nil {
			s.store.close()
		}
	})
	s.Now = func() time.Time { return time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC) }
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func do(t *testing.T, ts *httptest.Server, method, path string, body any, mod func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, ts.URL+path, rd)
	if method != "GET" {
		req.Header.Set("Origin", ts.URL)
		req.Header.Set("Content-Type", "application/json")
	}
	if mod != nil {
		mod(req)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res, data
}

func TestGuardRejectsForeignHostAndOrigin(t *testing.T) {
	_, ts := newTestServer(t, &scripted{})

	if res, _ := do(t, ts, "GET", "/api/state", nil, func(r *http.Request) { r.Host = "evil.example.com" }); res.StatusCode != 403 {
		t.Errorf("foreign Host (DNS rebinding) must be 403, got %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "GET", "/api/state", nil, nil); res.StatusCode != 200 {
		t.Errorf("loopback GET = %d", res.StatusCode)
	}
	post := map[string]string{"cmd": "daily"}
	cases := map[string]func(*http.Request){
		"no origin":      func(r *http.Request) { r.Header.Del("Origin") },
		"foreign origin": func(r *http.Request) { r.Header.Set("Origin", "https://evil.example.com") },
		"null origin":    func(r *http.Request) { r.Header.Set("Origin", "null") },
	}
	for name, mod := range cases {
		if res, _ := do(t, ts, "POST", "/api/runs", post, mod); res.StatusCode != 403 {
			t.Errorf("%s: POST must be 403, got %d", name, res.StatusCode)
		}
	}
}

func TestStateReportsProviderWarning(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	_, body := do(t, ts, "GET", "/api/state", nil, nil)
	if !strings.Contains(string(body), `"warning":""`) {
		t.Errorf("no warning expected: %s", body)
	}
	s.Warning = "falta la clave"
	_, body = do(t, ts, "GET", "/api/state", nil, nil)
	if !strings.Contains(string(body), `"warning":"falta la clave"`) {
		t.Errorf("warning must reach the UI: %s", body)
	}
}

func TestPageHasStrictHeaders(t *testing.T) {
	_, ts := newTestServer(t, &scripted{})
	res, body := do(t, ts, "GET", "/", nil, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), "StudyHomie") {
		t.Fatalf("index: %d", res.StatusCode)
	}
	csp := res.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %q: %s", want, csp)
		}
	}
	if !strings.Contains(csp, "frame-src 'self' https://www.youtube-nocookie.com;") || strings.Contains(csp, "youtube.com") {
		t.Errorf("only the no-cookie YouTube player may be framed: %s", csp)
	}
	for _, f := range []string{"/app.js", "/style.css", "/vendor/mermaid.min.js", "/vendor/marked.min.js", "/vendor/purify.min.js", "/vendor/cytoscape.min.js", "/vendor/katex/katex.min.js", "/vendor/katex/fonts/KaTeX_Main-Regular.woff2"} {
		if res, _ := do(t, ts, "GET", f, nil, nil); res.StatusCode != 200 {
			t.Errorf("%s = %d", f, res.StatusCode)
		}
	}
}

func seed(t *testing.T, v *vault.Vault) {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(v.Save(vault.Note{Meta: vault.Meta{Title: "Probabilidad", Status: "explained", Mastery: 80, Roadmap: "mate", Order: 1},
		Body: "Base de todo.\n\n## Conexiones\n\n- Ruta: [[mate]]\n"}))
	must(v.Save(vault.Note{Meta: vault.Meta{Title: "Bayes", Status: "prepared", Roadmap: "mate", Order: 2, Requires: []string{"probabilidad"},
		Resources: []vault.Resource{{Type: "video", Title: "3B1B", URL: "https://example.com/v"}}},
		Body: "Usa [[Probabilidad]] y [[Inferencia|inferir]] y [[fantasma]].\n\n## Recursos\n\n- x\n"}))
	must(v.Save(vault.Note{Meta: vault.Meta{Title: "Inferencia", Roadmap: "mate", Order: 3, Requires: []string{"bayes"}}, Body: "Ver [[Probabilidad]]."}))
	must(v.SaveIn("roadmaps", vault.Note{Slug: "mate", Meta: vault.Meta{Title: "Matemática"}, Body: "ruta"}))
	must(v.SaveInteractive("bayes", "<html><script>1</script></html>"))
}

func TestDataEndpoints(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	seed(t, s.V)

	_, body := do(t, ts, "GET", "/api/graph", nil, nil)
	var g struct {
		Nodes []gNode `json:"nodes"`
		Edges []gEdge `json:"edges"`
	}
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 3 {
		t.Fatalf("nodes = %+v", g.Nodes)
	}
	kinds := map[string]string{}
	for _, e := range g.Edges {
		kinds[e.From+">"+e.To] = e.Kind
	}
	if kinds["probabilidad>bayes"] != "requires" || kinds["bayes>inferencia"] != "requires" {
		t.Errorf("requires edges missing or reversed: %v", kinds)
	}
	for k := range kinds {
		if strings.Contains(k, "fantasma") || strings.Contains(k, "mate") {
			t.Errorf("edge to a note that is not a topic must be dropped: %s", k)
		}
	}
	// "Bayes links Probabilidad" duplicates the requires edge, so it is not repeated as related
	if _, dup := kinds["bayes>probabilidad"]; dup {
		t.Errorf("duplicate edge in the opposite direction: %v", kinds)
	}
	if kinds["inferencia>probabilidad"] != "related" {
		t.Errorf("plain wikilink must be a related edge: %v", kinds)
	}

	_, body = do(t, ts, "GET", "/api/notes/bayes", nil, nil)
	var n struct {
		Meta          vault.Meta `json:"meta"`
		Body          string     `json:"body"`
		Backlinks     []linkRef  `json:"backlinks"`
		Requires      []linkRef  `json:"requires"`
		HasSimulation bool       `json:"hasSimulation"`
	}
	json.Unmarshal(body, &n)
	if n.Meta.Title != "Bayes" || !n.HasSimulation || len(n.Requires) != 1 || n.Requires[0].Title != "Probabilidad" {
		t.Errorf("note = %+v", n)
	}
	if len(n.Backlinks) != 0 {
		t.Errorf("nobody links to bayes with a wikilink: %+v", n.Backlinks)
	}
	_, body = do(t, ts, "GET", "/api/notes/probabilidad", nil, nil)
	json.Unmarshal(body, &n)
	got := map[string]string{}
	for _, b := range n.Backlinks {
		got[b.Slug] = b.Title
	}
	if len(got) != 2 || got["bayes"] != "Bayes" || got["inferencia"] != "Inferencia" {
		t.Errorf("backlinks of probabilidad = %+v", n.Backlinks)
	}
	// the browser reads these exact key names; decoding into Go types would hide a mismatch
	var raw struct {
		Meta map[string]any `json:"meta"`
	}
	json.Unmarshal(body, &raw)
	_, body = do(t, ts, "GET", "/api/notes/bayes", nil, nil)
	json.Unmarshal(body, &raw)
	if raw.Meta["title"] != "Bayes" || raw.Meta["status"] != "prepared" || raw.Meta["roadmap"] != "mate" {
		t.Errorf("meta keys must be lowercase for the UI: %v", raw.Meta)
	}
	if res, _ := raw.Meta["resources"].([]any); len(res) != 1 || res[0].(map[string]any)["url"] != "https://example.com/v" {
		t.Errorf("resources keys: %v", raw.Meta["resources"])
	}
	if res, _ := do(t, ts, "GET", "/api/notes/no-existe", nil, nil); res.StatusCode != 404 {
		t.Errorf("missing note = %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "GET", "/api/notes/..%2F..%2Fsecret", nil, nil); res.StatusCode == 200 {
		t.Error("path traversal must not work")
	}

	_, body = do(t, ts, "GET", "/api/roadmaps", nil, nil)
	var rms []struct {
		Slug        string
		Total, Done int
		Next        *linkRef
		Pass        int
		Steps       []session.RoadmapStep
	}
	json.Unmarshal(body, &rms)
	if len(rms) != 1 || rms[0].Total != 3 || rms[0].Done != 1 || rms[0].Next == nil || rms[0].Next.Slug != "bayes" || rms[0].Pass != 70 {
		t.Errorf("roadmaps = %+v", rms)
	}
	var states []string
	for _, st := range rms[0].Steps {
		states = append(states, fmt.Sprintf("%d.%s:%s:%v", st.N, st.Slug, st.State, st.Ready))
	}
	// Bayes has a guide (seeded as text) only if its body has a "Guía" section: it does not
	if want := "1.probabilidad:passed:false,2.bayes:current:false,3.inferencia:locked:false"; strings.Join(states, ",") != want {
		t.Errorf("steps = %v, want %s", states, want)
	}

	_, body = do(t, ts, "GET", "/api/daily", nil, nil)
	if !strings.Contains(string(body), `"date":"2026-10-27"`) || strings.Contains(string(body), "topic") {
		t.Errorf("no daily yet: %s", body)
	}
	s.V.SaveIn("daily", vault.Note{Slug: "2026-10-27", Meta: vault.Meta{Title: "Bayes", Field: "Matemática"}})
	_, body = do(t, ts, "GET", "/api/daily", nil, nil)
	if !strings.Contains(string(body), `"slug":"bayes"`) || !strings.Contains(string(body), `"status":"prepared"`) {
		t.Errorf("daily = %s", body)
	}
}

func TestInteractiveIsSandboxed(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	seed(t, s.V)
	res, body := do(t, ts, "GET", "/interactive/bayes.html", nil, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), "<script>") {
		t.Fatalf("status %d", res.StatusCode)
	}
	csp := res.Header.Get("Content-Security-Policy")
	for _, want := range []string{"sandbox allow-scripts", "default-src 'none'", "connect-src 'none'", "frame-ancestors 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("simulation CSP lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "allow-same-origin") {
		t.Error("the simulation must never share the app's origin")
	}
	for _, p := range []string{"/interactive/nope.html", "/interactive/bayes.txt", "/interactive/..%2Fstudyhomie.toml", "/interactive/.html"} {
		if res, _ := do(t, ts, "GET", p, nil, nil); res.StatusCode == 200 {
			t.Errorf("%s must not be served", p)
		}
	}
}

// readEvents collects a run's events until "done" or the timeout.
func readEvents(t *testing.T, ts *httptest.Server, id int, from int, until func([]Event) bool) []Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/runs/"+itoa(id)+"/events?from="+itoa(from), nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var evs []Event
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var e Event
		json.Unmarshal([]byte(line[6:]), &e)
		evs = append(evs, e)
		if until(evs) {
			return evs
		}
	}
	return evs
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func has(evs []Event, typ string) *Event {
	for i := range evs {
		if evs[i].Type == typ {
			return &evs[i]
		}
	}
	return nil
}

func startRunHTTP(t *testing.T, ts *httptest.Server, body any) (int, int) {
	t.Helper()
	res, data := do(t, ts, "POST", "/api/runs", body, nil)
	var out struct {
		ID int `json:"id"`
	}
	json.Unmarshal(data, &out)
	return res.StatusCode, out.ID
}

func TestStudyRunEmitsCards(t *testing.T) {
	guide := "La intuición es X.\n\n```json\n{\"resources\":[{\"type\":\"video\",\"title\":\"Charla\",\"url\":\"https://www.youtube.com/watch?v=dQw4w9WgXcQ\",\"why\":\"Visual\"},{\"type\":\"book\",\"title\":\"Un libro\",\"why\":\"Profundiza\"}],\"handoff\":\"resumen\"}\n```"
	s, ts := newTestServer(t, &scripted{replies: []string{guide}})
	s.CheckLinks = func(_ context.Context, urls []string) map[string]bool {
		ok := map[string]bool{}
		for _, u := range urls {
			ok[u] = true
		}
		return ok
	}
	_, id := startRunHTTP(t, ts, map[string]string{"cmd": "study", "topic": "Bayes"})
	evs := readEvents(t, ts, id, 0, func(e []Event) bool { return has(e, "ask") != nil })

	var kinds []string
	var res *session.Card
	for i := range evs {
		if evs[i].Type == "card" {
			kinds = append(kinds, evs[i].Card.Kind+":"+evs[i].Card.Phase)
			if evs[i].Card.Kind == session.CardResources {
				res = evs[i].Card
			}
		}
	}
	if len(kinds) < 2 || kinds[0] != "phase:prepare" {
		t.Fatalf("expected the prepare phase card first, got %v", kinds)
	}
	if res == nil || len(res.Resources) != 2 || res.Resources[0].Type != "video" {
		t.Fatalf("expected a resources card with both items, got %+v", res)
	}
	ask := has(evs, "ask")
	if ask == nil || strings.Contains(outText(evs), "(s/n)") {
		t.Errorf("the prompt belongs in the ask event, not in the transcript text: %q", outText(evs))
	}
}

func outText(evs []Event) string {
	var b strings.Builder
	for _, e := range evs {
		if e.Type == "out" {
			b.WriteString(e.Text)
		}
	}
	return b.String()
}

func TestStudyRunOverSSE(t *testing.T) {
	s, ts := newTestServer(t, &scripted{replies: []string{prep}})
	code, id := startRunHTTP(t, ts, map[string]string{"cmd": "study", "topic": "Bayes"})
	if code != 200 || id == 0 {
		t.Fatalf("start: %d %d", code, id)
	}
	// wait for the question that comes after the guide
	evs := readEvents(t, ts, id, 0, func(e []Event) bool { return has(e, "ask") != nil })
	ask := has(evs, "ask")
	if ask == nil || ask.Kind != "line" || !strings.Contains(ask.Label, "Respondemos") {
		t.Fatalf("expected the quiz question, got %+v", evs)
	}
	// the browser shows the guide in its reading panel: the run announces it with a card
	ready := false
	for _, e := range evs {
		if e.Type == "card" && e.Card.Kind == "guide" && e.Card.Slug == "bayes" {
			ready = true
		}
	}
	if !ready {
		t.Errorf("no guide card in %+v", evs)
	}
	if n, err := s.V.Load("bayes"); err != nil || !strings.Contains(n.Body, "La intuición es X.") {
		t.Errorf("guide not saved: %v", err)
	}

	// a second run is refused while this one waits
	if code, _ := startRunHTTP(t, ts, map[string]string{"cmd": "daily"}); code != http.StatusConflict {
		t.Errorf("a second run must be 409, got %d", code)
	}
	if res, _ := do(t, ts, "GET", "/api/state", nil, nil); res.StatusCode != 200 {
		t.Fatal("state")
	}

	if res, _ := do(t, ts, "POST", "/api/runs/"+itoa(id)+"/input", map[string]any{"text": "n"}, nil); res.StatusCode != 200 {
		t.Fatalf("input = %d", res.StatusCode)
	}
	all := readEvents(t, ts, id, 0, func(e []Event) bool { return has(e, "done") != nil })
	if in := has(all, "in"); in == nil || in.Text != "n" {
		t.Errorf("the user's answer must be part of the history: %+v", all)
	}
	if d := has(all, "done"); d == nil || d.Error != "" {
		t.Errorf("done = %+v", d)
	}
	if n, err := s.V.Load("bayes"); err != nil || n.Meta.Status != "prepared" {
		t.Errorf("the guide must be saved: %+v %v", n.Meta, err)
	}
	// answering when nothing is pending is rejected
	if res, _ := do(t, ts, "POST", "/api/runs/"+itoa(id)+"/input", map[string]any{"text": "x"}, nil); res.StatusCode != http.StatusConflict {
		t.Errorf("late input = %d", res.StatusCode)
	}
	// a replay from the middle only returns what was missed
	tail := readEvents(t, ts, id, len(all)-1, func(e []Event) bool { return has(e, "done") != nil })
	if len(tail) != 1 || tail[0].Type != "done" {
		t.Errorf("replay from %d = %+v", len(all)-1, tail)
	}
	// and a new run can start now
	s.P.(*scripted).replies = []string{"x"}
	if code, _ := startRunHTTP(t, ts, map[string]string{"cmd": "next", "roadmap": "nada"}); code != 200 {
		t.Errorf("a new run after the previous finished = %d", code)
	}
}

func TestCancelStopsARun(t *testing.T) {
	p := &scripted{replies: []string{prep}, hold: make(chan struct{})}
	_, ts := newTestServer(t, p)
	_, id := startRunHTTP(t, ts, map[string]string{"cmd": "prepare", "topic": "Bayes"})
	time.Sleep(50 * time.Millisecond) // the model call is blocked on hold
	if res, _ := do(t, ts, "POST", "/api/runs/"+itoa(id)+"/cancel", map[string]any{}, nil); res.StatusCode != 200 {
		t.Fatalf("cancel = %d", res.StatusCode)
	}
	evs := readEvents(t, ts, id, 0, func(e []Event) bool { return has(e, "done") != nil })
	if d := has(evs, "done"); d == nil || d.Error != "cancelado" {
		t.Errorf("done = %+v", d)
	}
}

func TestStartValidation(t *testing.T) {
	_, ts := newTestServer(t, &scripted{})
	bad := []map[string]any{
		{"cmd": "nope"},
		{"cmd": "study"},
		{"cmd": "quiz"},
		{"cmd": "extend"},
		{"cmd": "roadmap", "subject": "  "},
		{"cmd": "material"},
		{"cmd": "material", "files": []string{"../../etc/passwd"}},
		{"cmd": "material", "files": []string{"0123456789abcdef/missing.pdf"}},
	}
	for _, b := range bad {
		if code, _ := startRunHTTP(t, ts, b); code != 400 {
			t.Errorf("%v = %d, want 400", b, code)
		}
	}
	if res, _ := do(t, ts, "POST", "/api/runs/999/input", map[string]any{"text": "x"}, nil); res.StatusCode != 404 {
		t.Errorf("unknown run = %d", res.StatusCode)
	}
}

func TestUploadThenMaterialRun(t *testing.T) {
	plan := "```json\n" + `{"subject":"Apuntes","nodes":[{"title":"Tema uno","summary":"Entender uno"}]}` + "\n```"
	_, ts := newTestServer(t, &scripted{replies: []string{plan, prep}})

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("files", "../../mis apuntes.md")
	io.WriteString(fw, "# Tema uno\n\nUna explicación suficientemente larga del tema uno para trocear.")
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", ts.URL)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var up struct{ Files []string }
	json.NewDecoder(res.Body).Decode(&up)
	res.Body.Close()
	if res.StatusCode != 200 || len(up.Files) != 1 || strings.Contains(up.Files[0], "..") || !strings.HasSuffix(up.Files[0], "/mis apuntes.md") {
		t.Fatalf("upload = %d %+v", res.StatusCode, up)
	}

	code, id := startRunHTTP(t, ts, map[string]any{"cmd": "material", "files": up.Files})
	if code != 200 {
		t.Fatalf("material start = %d", code)
	}
	evs := readEvents(t, ts, id, 0, func(e []Event) bool { return has(e, "ask") != nil })
	if a := has(evs, "ask"); a == nil || !strings.Contains(a.Label, "foco") {
		t.Fatalf("expected the focus question: %+v", evs)
	}
	do(t, ts, "POST", "/api/runs/"+itoa(id)+"/input", map[string]any{"text": ""}, nil)
	evs = readEvents(t, ts, id, 0, func(e []Event) bool { return has(e, "done") != nil && len(e) > 3 })
	var roadmapCard, guideCard *session.Card
	for _, e := range evs {
		if e.Card != nil && e.Card.Kind == session.CardRoadmap {
			roadmapCard = e.Card
		}
		if e.Card != nil && e.Card.Kind == session.CardGuide {
			guideCard = e.Card
		}
	}
	if roadmapCard == nil || roadmapCard.Topic != "Apuntes" || len(roadmapCard.Steps) != 1 {
		t.Errorf("roadmap not built from the upload: %+v", evs)
	}
	if guideCard == nil || guideCard.Slug != "tema-uno" {
		t.Errorf("the first topic's guide must be written: %+v", guideCard)
	}
}

func TestBlockInputCannotEndEarly(t *testing.T) {
	run := &Run{in: newInputBuf()}
	run.askKind = "block"
	if err := run.send("primera\n.\n/fin\nultima", false); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, _ := run.in.Read(buf)
	got := string(buf[:n])
	if strings.Count(got, "\n.\n") != 0 && !strings.HasSuffix(got, "\n.\n") {
		t.Errorf("a lone dot inside the text must not terminate the block: %q", got)
	}
	if strings.Contains(got, "\n/fin\n") {
		t.Errorf("a lone /fin inside the text must be neutralised: %q", got)
	}
	if !strings.HasSuffix(got, "ultima\n.\n") {
		t.Errorf("block must end with the terminator: %q", got)
	}

	run.askKind = "block"
	run.send("listo", true)
	n, _ = run.in.Read(buf)
	if !strings.HasSuffix(string(buf[:n]), "listo\n/fin\n") {
		t.Errorf("finish must send /fin: %q", buf[:n])
	}
}

func guidedNote(t *testing.T, s *Server) {
	t.Helper()
	if err := s.V.Save(vault.Note{Slug: "cumpleanos", Meta: vault.Meta{Title: "Paradoja del cumpleaños", Status: "prepared"}, Body: "## Guía\n\nEn un grupo de 23 personas..."}); err != nil {
		t.Fatal(err)
	}
}

func TestExtendEndpointAnswersWithTheTextAndSavesIt(t *testing.T) {
	s, ts := newTestServer(t, &scripted{replies: []string{"### Probabilidad\n\nContar casos."}})
	guidedNote(t, s)
	res, data := do(t, ts, "POST", "/api/notes/cumpleanos/extend", map[string]string{}, nil)
	var out struct{ Text string }
	json.Unmarshal(data, &out)
	if res.StatusCode != 200 || !strings.Contains(out.Text, "Contar casos.") {
		t.Fatalf("extend = %d %s", res.StatusCode, data)
	}
	n, _ := s.V.Load("cumpleanos")
	if !strings.Contains(n.Body, "## Conceptos base") || n.Meta.Status != "prepared" {
		t.Errorf("section not saved, or progress changed:\n%s\n%+v", n.Body, n.Meta)
	}
	if rows := s.runs; len(rows) != 0 {
		t.Errorf("an instant action must not appear in the session history: %v", rows)
	}
}

func TestDoubtEndpointSavesTheExchangeAndOffersConcepts(t *testing.T) {
	reply := "Se multiplican porque piden las dos cosas, ver [[Probabilidad condicional]].\n\n```json\n" + `{"concepts":[{"title":"Probabilidad condicional","summary":"Cómo cambia una probabilidad al saber algo más."}]}` + "\n```"
	s, ts := newTestServer(t, &scripted{replies: []string{reply}})
	guidedNote(t, s)

	res, data := do(t, ts, "POST", "/api/notes/cumpleanos/doubt", map[string]string{"question": "¿Por qué se multiplican?"}, nil)
	var ans struct {
		Entry    string
		Concepts []struct{ Title, Slug string }
	}
	json.Unmarshal(data, &ans)
	if res.StatusCode != 200 || !strings.HasPrefix(ans.Entry, "### ¿Por qué se multiplican?") || len(ans.Concepts) != 1 || ans.Concepts[0].Slug != "probabilidad-condicional" {
		t.Fatalf("doubt = %d %s", res.StatusCode, data)
	}
	n, _ := s.V.Load("cumpleanos")
	if !strings.Contains(n.Body, "## Dudas") || !strings.Contains(n.Body, "piden las dos cosas") {
		t.Errorf("the exchange must be saved in the note:\n%s", n.Body)
	}

	// adding the concept creates an empty note, once
	for i, want := range []bool{true, false} {
		res, data = do(t, ts, "POST", "/api/notes/cumpleanos/concept", map[string]string{"title": "Probabilidad condicional", "summary": "x"}, nil)
		var c struct {
			Slug    string
			Created bool
		}
		json.Unmarshal(data, &c)
		if res.StatusCode != 200 || c.Slug != "probabilidad-condicional" || c.Created != want {
			t.Fatalf("concept #%d = %d %s", i, res.StatusCode, data)
		}
	}
	if c, err := s.V.Load("probabilidad-condicional"); err != nil || c.Meta.Status != "new" {
		t.Errorf("the concept note was not created: %v %+v", err, c.Meta)
	}
}

func TestShortActionsRejectBadRequests(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	guidedNote(t, s)
	s.V.Save(vault.Note{Slug: "sin-guia", Meta: vault.Meta{Title: "Sin guía"}, Body: ""})
	cases := []struct {
		path string
		body map[string]string
		want int
	}{
		{"/api/notes/cumpleanos/doubt", map[string]string{"question": "  "}, 400},
		{"/api/notes/sin-guia/doubt", map[string]string{"question": "¿y esto?"}, 400},
		{"/api/notes/sin-guia/extend", map[string]string{}, 400},
		{"/api/notes/no-existe/extend", map[string]string{}, 400},
		{"/api/notes/Mal Slug/extend", map[string]string{}, 400},
		{"/api/notes/cumpleanos/concept", map[string]string{"title": " "}, 400},
		{"/api/notes/no-existe/concept", map[string]string{"title": "Algo"}, 400},
	}
	for _, c := range cases {
		if res, data := do(t, ts, "POST", strings.ReplaceAll(c.path, " ", "%20"), c.body, nil); res.StatusCode != c.want {
			t.Errorf("%s %v = %d, want %d (%s)", c.path, c.body, res.StatusCode, c.want, data)
		}
	}
	// nothing that changes a note may be done from another origin
	res, _ := do(t, ts, "POST", "/api/notes/cumpleanos/doubt", map[string]string{"question": "x"}, func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") })
	if res.StatusCode != 403 {
		t.Errorf("foreign origin = %d, want 403", res.StatusCode)
	}
}

func TestCancellingAShortActionStopsTheModelAndSavesNothing(t *testing.T) {
	hold := make(chan struct{}) // the model never answers
	s, ts := newTestServer(t, &scripted{hold: hold, replies: []string{"nunca se usa"}})
	guidedNote(t, s)
	for _, path := range []string{"/api/notes/cumpleanos/extend", "/api/notes/cumpleanos/doubt"} {
		ctx, cancel := context.WithCancel(context.Background())
		body := strings.NewReader(`{"question":"¿por qué?"}`)
		req, _ := http.NewRequestWithContext(ctx, "POST", ts.URL+path, body)
		req.Header.Set("Origin", ts.URL)
		req.Header.Set("Content-Type", "application/json")
		done := make(chan error, 1)
		go func() {
			res, err := http.DefaultClient.Do(req)
			if res != nil {
				res.Body.Close()
			}
			done <- err
		}()
		time.Sleep(150 * time.Millisecond) // the request is now waiting for the model
		cancel()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%s: the request should have been cancelled", path)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: cancelling did not release the request", path)
		}
	}
	time.Sleep(200 * time.Millisecond) // let the handlers notice and finish
	n, _ := s.V.Load("cumpleanos")
	if strings.Contains(n.Body, "## Dudas") || strings.Contains(n.Body, "## Conceptos base") {
		t.Errorf("a cancelled action must not change the note:\n%s", n.Body)
	}
}

func lastAsk(evs []Event) *Event {
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type == "ask" {
			return &evs[i]
		}
		if evs[i].Type == "in" {
			return nil
		}
	}
	return nil
}

func TestChatRunTalksParksAndResumes(t *testing.T) {
	p := &scripted{replies: []string{"Hola, ¿qué estudiamos?", "La media es el promedio.", "Otro chat.", "Seguimos con lo de antes."}}
	s, ts := newTestServer(t, p)
	code, id := startRunHTTP(t, ts, map[string]any{"cmd": "chat", "chat": map[string]any{"text": "hola", "depth": 2}})
	if code != 200 {
		t.Fatalf("start chat: %d", code)
	}
	waitChat := func(id, n int) []Event {
		return readEvents(t, ts, id, 0, func(e []Event) bool {
			a := lastAsk(e)
			c := 0
			for _, x := range e {
				if x.Type == "ask" && x.Kind == "chat" {
					c++
				}
			}
			return a != nil && a.Kind == "chat" && c >= n
		})
	}
	evs := waitChat(id, 1)
	if evs[0].Type != "in" || evs[0].Text != "hola" || !strings.Contains(outText(evs), "qué estudiamos") {
		t.Fatalf("opening exchange: %+v", evs)
	}
	if res, body := do(t, ts, "POST", "/api/runs/"+itoa(id)+"/input", map[string]any{"text": "¿qué es la media?"}, nil); res.StatusCode != 200 {
		t.Fatalf("chat input: %d %s", res.StatusCode, body)
	}
	evs = waitChat(id, 2)
	if !strings.Contains(outText(evs), "promedio") {
		t.Errorf("second answer missing: %s", outText(evs))
	}

	// a new chat parks the idle one, which ends without error
	code, id2 := startRunHTTP(t, ts, map[string]any{"cmd": "chat", "chat": map[string]any{"text": "otra cosa"}})
	if code != 200 {
		t.Fatalf("second chat: %d", code)
	}
	old := readEvents(t, ts, id, 0, func(e []Event) bool { return has(e, "done") != nil })
	if d := has(old, "done"); d.Error != "" {
		t.Errorf("a parked chat must end cleanly: %q", d.Error)
	}
	waitChat(id2, 1)

	// continuing the first chat brings its transcript along, under a new id
	code, id3 := startRunHTTP(t, ts, map[string]any{"cmd": "chat", "resume": id, "chat": map[string]any{"text": "sigamos"}})
	if code != 200 {
		t.Fatalf("resume: %d", code)
	}
	evs = waitChat(id3, 3)
	if evs[0].Text != "hola" || !strings.Contains(outText(evs), "Seguimos con lo de antes.") {
		t.Errorf("resumed transcript: %+v", evs)
	}
	s.mu.Lock()
	_, stillThere := s.runs[id]
	s.mu.Unlock()
	if stillThere {
		t.Error("the continued chat should be replaced by the new one")
	}
}

func TestNoteEndpointServesConcepts(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	if err := s.V.Save(vault.Note{Slug: "bayes", Meta: vault.Meta{Title: "Bayes", Concepts: []string{"probabilidad"}, Base: []string{"probabilidad"}}, Body: "## Guía\n\nVer [[probabilidad]].\n"}); err != nil {
		t.Fatal(err)
	}
	if err := s.V.SaveConcept(vault.Concept{Slug: "probabilidad", Meta: vault.ConceptMeta{Title: "Probabilidad", Guides: []string{"bayes"}}, Body: "# Probabilidad\n"}); err != nil {
		t.Fatal(err)
	}
	res, body := do(t, ts, "GET", "/api/notes/probabilidad", nil, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"kind":"concept"`) || !strings.Contains(string(body), `"guides":[{"slug":"bayes","title":"Bayes"}]`) {
		t.Errorf("concept note: %d %s", res.StatusCode, body)
	}
	_, body = do(t, ts, "GET", "/api/notes/bayes", nil, nil)
	if !strings.Contains(string(body), `"concepts":[{"slug":"probabilidad","title":"Probabilidad","exists":true,"guides":1}]`) {
		t.Errorf("guide note: %s", body)
	}
	_, body = do(t, ts, "GET", "/api/graph", nil, nil)
	if !strings.Contains(string(body), `{"from":"probabilidad","to":"bayes","kind":"requires"}`) || !strings.Contains(string(body), `"kind":"concept"`) {
		t.Errorf("graph: %s", body)
	}
}

func TestExplainEndpointAndPendingInTheGraph(t *testing.T) {
	s, ts := newTestServer(t, &scripted{replies: []string{"## En una frase\n\nAlgo."}})
	if err := s.V.Save(vault.Note{Slug: "bayes", Meta: vault.Meta{Title: "Bayes",
		Pending: []vault.PendingConcept{{ID: "fraccion", Title: "Fracción", Level: 2}}}, Body: "## Guía\n\nVer [[fraccion]].\n"}); err != nil {
		t.Fatal(err)
	}
	_, body := do(t, ts, "GET", "/api/graph", nil, nil)
	if !strings.Contains(string(body), `"id":"fraccion","label":"Fracción","kind":"pending"`) {
		t.Errorf("graph without the pending concept: %s", body)
	}
	res, body := do(t, ts, "POST", "/api/notes/bayes/explain", map[string]string{"ref": "fraccion"}, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"slug":"fraccion"`) {
		t.Fatalf("explain: %d %s", res.StatusCode, body)
	}
	if !s.V.ConceptExists("fraccion") {
		t.Error("the concept note was not written")
	}
	_, body = do(t, ts, "GET", "/api/graph", nil, nil)
	if strings.Contains(string(body), `"kind":"pending"`) || !strings.Contains(string(body), `"id":"fraccion","label":"Fracción","kind":"concept"`) {
		t.Errorf("explained concept should be a concept now: %s", body)
	}
}

func TestSettingsSwitchTheBackend(t *testing.T) {
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			fmt.Fprint(w, `{"data":[{"id":"qwen3:14b"}]}`)
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"listo\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer ai.Close()
	s, ts := newTestServer(t, &scripted{})
	s.ConfigPath = filepath.Join(t.TempDir(), "studyhomie.toml")

	_, body := do(t, ts, "GET", "/api/settings", nil, nil)
	if !strings.Contains(string(body), `"id":"ollama"`) || !strings.Contains(string(body), `"id":"codex"`) {
		t.Fatalf("settings: %s", body)
	}
	choice := map[string]string{"provider": "ollama", "model": "qwen3:14b", "baseURL": ai.URL + "/v1"}
	res, body := do(t, ts, "POST", "/api/settings/models", choice, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), "qwen3:14b") {
		t.Errorf("models: %d %s", res.StatusCode, body)
	}
	res, body = do(t, ts, "POST", "/api/settings/test", choice, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"reply":"listo"`) {
		t.Errorf("test: %d %s", res.StatusCode, body)
	}
	res, body = do(t, ts, "POST", "/api/settings", choice, nil)
	if res.StatusCode != 200 {
		t.Fatalf("save: %d %s", res.StatusCode, body)
	}
	_, body = do(t, ts, "GET", "/api/state", nil, nil)
	if !strings.Contains(string(body), `"provider":"ollama"`) {
		t.Errorf("the app must use the new backend at once: %s", body)
	}
	saved, _ := os.ReadFile(s.ConfigPath)
	if !strings.Contains(string(saved), `provider = "ollama"`) || !strings.Contains(string(saved), `model = "qwen3:14b"`) || !strings.Contains(string(saved), "base_url") {
		t.Errorf("config file:\n%s", saved)
	}
	if res, _ := do(t, ts, "POST", "/api/settings", map[string]string{"provider": "nope"}, nil); res.StatusCode != 400 {
		t.Errorf("unknown provider must be refused, got %d", res.StatusCode)
	}
}

func TestApuntesAndAttachments(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	if err := s.V.Save(vault.Note{Slug: "integral", Meta: vault.Meta{Title: "Integral"}, Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if res, _ := do(t, ts, "PUT", "/api/apuntes/no-existe", map[string]string{"body": "hola"}, nil); res.StatusCode != 404 {
		t.Errorf("notes on a missing note = %d", res.StatusCode)
	}
	if res, body := do(t, ts, "PUT", "/api/apuntes/integral", map[string]string{"body": "Mi resumen ![[foto.png]]"}, nil); res.StatusCode != 200 {
		t.Fatalf("save = %d %s", res.StatusCode, body)
	}
	_, body := do(t, ts, "GET", "/api/apuntes/integral", nil, nil)
	if !strings.Contains(string(body), "Mi resumen") {
		t.Errorf("load = %s", body)
	}
	_, body = do(t, ts, "GET", "/api/notes/integral", nil, nil)
	if !strings.Contains(string(body), `"apunte":true`) {
		t.Errorf("the note must say it has notes: %s", body)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("files", "pizarra.png")
	io.WriteString(fw, "\x89PNG fake")
	fw, _ = mw.CreateFormFile("files", "ejercicios.html")
	io.WriteString(fw, "<script>alert(1)</script>")
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/adjuntos", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", ts.URL)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var up struct {
		Files []struct {
			Name  string
			Image bool
		}
	}
	json.NewDecoder(res.Body).Decode(&up)
	res.Body.Close()
	if len(up.Files) != 2 || up.Files[0].Name != "pizarra.png" || !up.Files[0].Image || up.Files[1].Image {
		t.Fatalf("attach = %+v", up)
	}
	res, _ = do(t, ts, "GET", "/api/adjuntos/pizarra.png", nil, nil)
	if res.Header.Get("Content-Type") != "image/png" {
		t.Errorf("image type = %q", res.Header.Get("Content-Type"))
	}
	res, _ = do(t, ts, "GET", "/api/adjuntos/ejercicios.html", nil, nil)
	if !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") || strings.Contains(res.Header.Get("Content-Type"), "html") {
		t.Errorf("other files must download: %v", res.Header)
	}
	if res, _ := do(t, ts, "GET", "/api/adjuntos/..%2F..%2Fstudyhomie.toml", nil, nil); res.StatusCode == 200 {
		t.Error("path escape served")
	}
}

func TestVaultsSwitchAndManage(t *testing.T) {
	proposal := "```json\n{\"field\":\"Historia\",\"topic\":\"Tema\",\"hook\":\"h\"}\n```"
	s, ts := newTestServer(t, &scripted{replies: []string{proposal, proposal, proposal, proposal, proposal}})
	first := s.V.Root
	s.ConfigPath = filepath.Join(t.TempDir(), "studyhomie.toml")
	s.Cfg.Vault = first
	if err := s.V.Save(vault.Note{Slug: "fourier", Meta: vault.Meta{Title: "Fourier"}, Body: "La fase importa."}); err != nil {
		t.Fatal(err)
	}
	// a chat in the first vault
	code, id := startRunHTTP(t, ts, map[string]any{"cmd": "daily"})
	if code != 200 {
		t.Fatalf("start = %d", code)
	}
	readEvents(t, ts, id, 0, func(e []Event) bool { return has(e, "ask") != nil })

	second := filepath.Join(t.TempDir(), "Cálculo")
	if res, body := do(t, ts, "POST", "/api/vaults", map[string]string{"path": "relativo"}, nil); res.StatusCode != 400 {
		t.Errorf("relative path = %d %s", res.StatusCode, body)
	}
	// busy: the daily run is waiting for a yes/no, not for a chat message
	if res, _ := do(t, ts, "POST", "/api/vaults", map[string]string{"path": second}, nil); res.StatusCode != 409 {
		t.Errorf("switch while a flow waits for an answer = %d", res.StatusCode)
	}
	do(t, ts, "POST", fmt.Sprintf("/api/runs/%d/cancel", id), map[string]any{}, nil)
	readEvents(t, ts, id, 0, func(e []Event) bool { return has(e, "done") != nil })
	if res, body := do(t, ts, "POST", "/api/vaults", map[string]string{"path": second}, nil); res.StatusCode != 200 {
		t.Fatalf("add = %d %s", res.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(second, vault.GuidesDir)); err != nil {
		t.Error("a new vault gets its folders")
	}
	_, body := do(t, ts, "GET", "/api/sessions", nil, nil)
	if string(body) != "[]\n" && string(body) != "[]" {
		t.Errorf("the new vault has its own chats: %s", body)
	}
	_, body = do(t, ts, "GET", "/api/graph", nil, nil)
	if strings.Contains(string(body), "fourier") {
		t.Errorf("the new vault must not show the old notes: %s", body)
	}
	saved, _ := os.ReadFile(s.ConfigPath)
	if !strings.Contains(string(saved), "Cálculo") || !strings.Contains(string(saved), "vaults") {
		t.Errorf("config: %s", saved)
	}
	var vl struct {
		Vaults []struct {
			Path    string
			Current bool
		}
	}
	_, body = do(t, ts, "GET", "/api/vaults", nil, nil)
	json.Unmarshal(body, &vl)
	if len(vl.Vaults) != 2 || !vl.Vaults[0].Current {
		t.Fatalf("vault list: %s", body)
	}
	if res, _ := do(t, ts, "POST", "/api/vaults/forget", map[string]string{"path": second}, nil); res.StatusCode != 400 {
		t.Error("the vault in use cannot be forgotten")
	}
	// back to the first one: its notes and its chat come back
	if res, body := do(t, ts, "POST", "/api/vaults/use", map[string]string{"path": first}, nil); res.StatusCode != 200 {
		t.Fatalf("use = %d %s", res.StatusCode, body)
	}
	_, body = do(t, ts, "GET", "/api/sessions", nil, nil)
	if !strings.Contains(string(body), "daily") {
		t.Errorf("the first vault's chats must come back: %s", body)
	}
	if res, _ := do(t, ts, "POST", "/api/vaults/use", map[string]string{"path": "C:/nunca/agregado"}, nil); res.StatusCode != 404 {
		t.Error("unknown vault accepted")
	}
	if res, _ := do(t, ts, "POST", "/api/vaults/forget", map[string]string{"path": second}, nil); res.StatusCode != 200 {
		t.Error("forget")
	}
	if _, err := os.Stat(second); err != nil {
		t.Error("forgetting a vault must never delete it")
	}

	// browse, search, edit
	_, body = do(t, ts, "GET", "/api/vault/files", nil, nil)
	if !strings.Contains(string(body), `"slug":"fourier"`) {
		t.Errorf("files: %s", body)
	}
	_, body = do(t, ts, "GET", "/api/vault/search?q=FASE", nil, nil)
	if !strings.Contains(string(body), "La fase importa") {
		t.Errorf("search: %s", body)
	}
	var raw struct {
		Text    string
		Updated time.Time
	}
	_, body = do(t, ts, "GET", "/api/vault/raw/guias/fourier", nil, nil)
	json.Unmarshal(body, &raw)
	if !strings.Contains(raw.Text, "title: Fourier") {
		t.Fatalf("raw: %s", body)
	}
	edited := strings.Replace(raw.Text, "La fase importa.", "La fase importa mucho.", 1)
	if res, body := do(t, ts, "PUT", "/api/vault/raw/guias/fourier", map[string]any{"text": edited, "since": raw.Updated}, nil); res.StatusCode != 200 {
		t.Fatalf("write = %d %s", res.StatusCode, body)
	}
	if n, _ := s.vault().Load("fourier"); !strings.Contains(n.Body, "mucho") {
		t.Errorf("edit not saved: %q", n.Body)
	}
	if res, _ := do(t, ts, "PUT", "/api/vault/raw/guias/fourier", map[string]any{"text": edited, "since": raw.Updated.Add(-time.Hour)}, nil); res.StatusCode != 409 {
		t.Errorf("a stale edit must be refused, got %d", res.StatusCode)
	}

	// the desktop: the folder picker and opening outside the app
	s.PickFolder = func(context.Context) (string, error) { return `D:\Uni`, nil }
	_, body = do(t, ts, "POST", "/api/vaults/pick", map[string]any{}, nil)
	var picked struct{ Path string }
	if json.Unmarshal(body, &picked); picked.Path != `D:\Uni` {
		t.Errorf("pick: %s", body)
	}
	var opened []string
	s.Open = func(kind, target string) error { opened = append(opened, kind+" "+target); return nil }
	do(t, ts, "POST", "/api/vault/open", map[string]string{"app": "obsidian", "dir": "guias", "slug": "fourier"}, nil)
	do(t, ts, "POST", "/api/vault/open", map[string]string{"app": "folder"}, nil)
	if res, _ := do(t, ts, "POST", "/api/vault/open", map[string]string{"app": "folder", "dir": "..", "slug": "x"}, nil); res.StatusCode != 404 {
		t.Error("open outside the vault accepted")
	}
	if len(opened) != 2 || !strings.HasPrefix(opened[0], "url obsidian://open?path=") || !strings.Contains(opened[0], "fourier.md") || !strings.HasPrefix(opened[1], "folder ") {
		t.Errorf("opened: %q", opened)
	}
}

func TestStudySettings(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	if res, _ := do(t, ts, "POST", "/api/settings/study", map[string]any{"passScore": 101}, nil); res.StatusCode != 400 {
		t.Errorf("101 must be rejected, got %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "POST", "/api/settings/study", map[string]any{}, nil); res.StatusCode != 400 {
		t.Errorf("a missing score must be rejected, got %d", res.StatusCode)
	}
	if res, body := do(t, ts, "POST", "/api/settings/study", map[string]any{"passScore": 0}, nil); res.StatusCode != 200 || !strings.Contains(string(body), `"passScore":0`) {
		t.Fatalf("save = %d %s", res.StatusCode, body)
	}
	if _, cfg, _ := s.backend(); cfg.PassScore != 0 {
		t.Errorf("pass score = %d", cfg.PassScore)
	}
	// the time limit of an AI call goes alone, and leaves the score as it was
	if res, _ := do(t, ts, "POST", "/api/settings/study", map[string]any{"timeout": 61}, nil); res.StatusCode != 400 {
		t.Errorf("61 minutes must be rejected, got %d", res.StatusCode)
	}
	if res, body := do(t, ts, "POST", "/api/settings/study", map[string]any{"timeout": 12}, nil); res.StatusCode != 200 || !strings.Contains(string(body), `"timeout":12`) {
		t.Fatalf("save timeout = %d %s", res.StatusCode, body)
	}
	if _, cfg, _ := s.backend(); cfg.Timeout != 12 || cfg.PassScore != 0 {
		t.Errorf("after saving the timeout: %d, pass score %d", cfg.Timeout, cfg.PassScore)
	}
}

func TestLanguageScriptAndSetting(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	s.Cfg.Language = ""
	_, body := do(t, ts, "GET", "/i18n.js", nil, nil)
	js := string(body)
	if !strings.HasPrefix(js, "window.I18N = ") || !strings.Contains(js, `"chosen":false`) || !strings.Contains(js, `"lang":"es"`) || !strings.Contains(js, "function t(") {
		t.Fatalf("i18n.js = %.300s", js)
	}
	if res, _ := do(t, ts, "POST", "/api/settings/language", map[string]any{"language": "klingon"}, nil); res.StatusCode != 400 {
		t.Errorf("an unknown language must be rejected: %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "POST", "/api/settings/language", map[string]any{"language": "fr"}, nil); res.StatusCode != 200 {
		t.Fatalf("set language = %d", res.StatusCode)
	}
	_, body = do(t, ts, "GET", "/i18n.js", nil, nil)
	js = string(body)
	if !strings.Contains(js, `"lang":"fr"`) || !strings.Contains(js, `"chosen":true`) || !strings.Contains(js, `"Nueva conversación":"Nouvelle conversation"`) {
		t.Errorf("after choosing French: %.300s", js)
	}
	// a run's questions come translated, with the Spanish one alongside to tell which it is
	if _, cfg, _ := s.backend(); cfg.Language != "fr" {
		t.Errorf("config language = %q", cfg.Language)
	}
}

func TestDigEndpointAndSharedConceptsInTheGraph(t *testing.T) {
	dig := "```json\n{\"concepts\":[{\"id\":\"fraccion\",\"title\":\"Fracción\",\"for\":[\"probabilidad\"]}]}\n```"
	s, ts := newTestServer(t, &scripted{replies: []string{dig}})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.V.Save(vault.Note{Slug: "bayes", Meta: vault.Meta{Title: "Bayes", Concepts: []string{"probabilidad"}, Base: []string{"probabilidad"}}, Body: "## Guía\n\nx\n"}))
	must(s.V.Save(vault.Note{Slug: "markov", Meta: vault.Meta{Title: "Markov", Concepts: []string{"probabilidad"}, Base: []string{"probabilidad", "matriz"},
		Pending: []vault.PendingConcept{{ID: "matriz", Title: "Matriz", Level: 1}}}, Body: "## Guía\n\nx\n"}))
	must(s.V.Save(vault.Note{Slug: "pca", Meta: vault.Meta{Title: "PCA", Base: []string{"matriz"},
		Pending: []vault.PendingConcept{{ID: "matriz", Title: "Matriz", Level: 1, Requires: []string{"vector"}}, {ID: "vector", Title: "Vector", Level: 2}}}, Body: "## Guía\n\nx\n"}))
	must(s.V.SaveConcept(vault.Concept{Slug: "probabilidad", Meta: vault.ConceptMeta{Title: "Probabilidad", Guides: []string{"bayes", "markov"}}, Body: "# Probabilidad\n"}))

	res, body := do(t, ts, "POST", "/api/notes/bayes/dig", map[string]any{}, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"id":"fraccion"`) {
		t.Fatalf("dig: %d %s", res.StatusCode, body)
	}

	var g struct {
		Nodes []gNode `json:"nodes"`
		Edges []gEdge `json:"edges"`
	}
	_, body = do(t, ts, "GET", "/api/graph", nil, nil)
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatal(err)
	}
	nodes := map[string]gNode{}
	count := map[string]int{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
		count[n.ID]++
	}
	edges := map[string]bool{}
	for _, e := range g.Edges {
		edges[e.From+">"+e.To] = true
	}
	// one node per concept, with an edge to each guide that stands on it
	if count["probabilidad"] != 1 || nodes["probabilidad"].Shared != 2 || !edges["probabilidad>bayes"] || !edges["probabilidad>markov"] {
		t.Errorf("shared concept: %+v, edges %v", nodes["probabilidad"], edges)
	}
	if count["matriz"] != 1 || nodes["matriz"].Shared != 2 || !edges["matriz>markov"] || !edges["matriz>pca"] {
		t.Errorf("shared pending concept: %+v, edges %v", nodes["matriz"], edges)
	}
	// what the second guide says a shared pending concept stands on is kept too
	if !edges["vector>matriz"] {
		t.Errorf("requires of a pending concept mapped by two guides: %v", edges)
	}
	if nodes["fraccion"].Kind != "pending" || !edges["fraccion>probabilidad"] {
		t.Errorf("dug concept: %+v, edges %v", nodes["fraccion"], edges)
	}

	// a chat that wrote the Markov guide: probabilidad came from the Bayes guide, another chat
	s.mu.Lock()
	s.runs[42] = &Run{ID: 42, Slug: "markov"}
	s.mu.Unlock()
	_, body = do(t, ts, "GET", "/api/graph?chat=42", nil, nil)
	g.Nodes = nil
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatal(err)
	}
	foreign := map[string]bool{}
	for _, n := range g.Nodes {
		foreign[n.ID] = n.Foreign
	}
	if !foreign["probabilidad"] || foreign["matriz"] || foreign["markov"] {
		t.Errorf("foreign marks in the chat map: %v", foreign)
	}
}
