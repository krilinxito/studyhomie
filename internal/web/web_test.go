package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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
	s := New(config.Default(), v, p)
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
	}
	json.Unmarshal(body, &rms)
	if len(rms) != 1 || rms[0].Total != 3 || rms[0].Done != 1 || rms[0].Next == nil || rms[0].Next.Slug != "bayes" {
		t.Errorf("roadmaps = %+v", rms)
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
	if ask == nil || ask.Kind != "line" || !strings.Contains(ask.Label, "prueba") {
		t.Fatalf("expected the quiz question, got %+v", evs)
	}
	var out strings.Builder
	for _, e := range evs {
		if e.Type == "out" {
			out.WriteString(e.Text)
		}
	}
	if !strings.Contains(out.String(), "La intuición es X.") {
		t.Errorf("guide not streamed: %s", out.String())
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
	_, ts := newTestServer(t, &scripted{replies: []string{plan}})

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
	var out strings.Builder
	for _, e := range evs {
		out.WriteString(e.Text)
	}
	if !strings.Contains(out.String(), "Roadmap «Apuntes»") {
		t.Errorf("roadmap not built from the upload: %s", out.String())
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
