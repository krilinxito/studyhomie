// Package web serves the local browser UI: the knowledge graph, notes, and the
// study flows running in the background.
package web

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/roadmap"
	"github.com/krilinxito/studyhomie/internal/session"
	"github.com/krilinxito/studyhomie/internal/vault"
)

//go:embed static
var staticFS embed.FS

const (
	maxJSONBody = 1 << 20
	maxUpload   = 64 << 20
)

type Server struct {
	Cfg config.Config
	V   *vault.Vault
	P   provider.Provider
	// Now is overridable in tests.
	Now func() time.Time
	// Warning, if set, is shown in the UI: the AI backend is not ready.
	Warning string
	// CheckLinks verifies cited URLs; nil uses the real check. Tests replace it.
	CheckLinks func(ctx context.Context, urls []string) map[string]bool

	mu     sync.Mutex
	runs   map[int]*Run
	active *Run
	seq    int
	store  *store // nil when the sessions folder cannot be created: they then live in memory only
}

func New(cfg config.Config, v *vault.Vault, p provider.Provider) *Server {
	s := &Server{Cfg: cfg, V: v, P: p, Now: time.Now, runs: map[int]*Run{}}
	if st, err := newStore(s.sessionsDir()); err == nil {
		s.store = st
		s.loadSessions()
	}
	return s
}

func (s *Server) uploadDir() string   { return filepath.Join(s.V.Root, ".studyhomie", "uploads") }
func (s *Server) sessionsDir() string { return filepath.Join(s.V.Root, ".studyhomie", "sessions") }

// loadSessions brings back the stored history. One that was still running when the app closed
// is marked interrupted and given a closing event, so the browser can replay it to the end.
func (s *Server) loadSessions() {
	for _, sf := range s.store.load() {
		run := restore(sf)
		run.store = s.store
		if sf.Status == statusRunning || len(sf.Events) == 0 || sf.Events[len(sf.Events)-1].Type != "done" {
			run.status = statusInterrupted
			run.errMsg = interruptedMsg
			run.events = append(run.events, Event{Type: "done", Error: interruptedMsg})
			if run.Ended.IsZero() {
				run.Ended = run.Started
			}
			s.store.save(run)
		}
		s.runs[run.ID] = run
		if run.ID > s.seq {
			s.seq = run.ID
		}
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /", pageHeaders(http.FileServerFS(static)))

	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("GET /api/graph", s.graph)
	mux.HandleFunc("GET /api/notes/{slug}", s.note)
	mux.HandleFunc("POST /api/notes/{slug}/extend", s.extend)
	mux.HandleFunc("POST /api/notes/{slug}/doubt", s.doubt)
	mux.HandleFunc("POST /api/notes/{slug}/concept", s.concept)
	mux.HandleFunc("GET /api/roadmaps", s.roadmaps)
	mux.HandleFunc("GET /api/daily", s.daily)
	mux.HandleFunc("GET /api/sessions", s.sessions)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.deleteSession)
	mux.HandleFunc("POST /api/upload", s.upload)
	mux.HandleFunc("POST /api/runs", s.startRun)
	mux.HandleFunc("GET /api/runs/{id}/events", s.events)
	mux.HandleFunc("POST /api/runs/{id}/input", s.input)
	mux.HandleFunc("POST /api/runs/{id}/cancel", s.cancel)
	mux.HandleFunc("GET /interactive/{file}", s.interactive)
	return guard(mux)
}

// guard protects a server that can spend the user's AI quota and read their files:
// only loopback host names are accepted (blocks DNS rebinding) and every request
// that changes something must come from this very origin (blocks cross-site forms).
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "[::1]" && host != "::1" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			u, err := url.Parse(r.Header.Get("Origin"))
			if err != nil || u.Host == "" || u.Host != r.Host {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func pageHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; frame-src 'self' https://www.youtube-nocookie.com; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "JSON inválido: "+err.Error())
		return false
	}
	return true
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	active := 0
	s.mu.Lock()
	if s.active != nil && !s.active.isDone() {
		active = s.active.ID
	}
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{
		"provider":  s.P.Name(),
		"vault":     s.V.Root,
		"interests": s.Cfg.Interests,
		"activeRun": active,
		"warning":   s.Warning,
	})
}

type gNode struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Mastery int    `json:"mastery"`
	Roadmap string `json:"roadmap,omitempty"`
	Order   int    `json:"order,omitempty"`
	Source  string `json:"source,omitempty"`
	Field   string `json:"field,omitempty"`
}

type gEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"` // requires: from must be understood before to | related
}

func (s *Server) graph(w http.ResponseWriter, r *http.Request) {
	notes, err := s.V.List()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	exists := map[string]bool{}
	for _, n := range notes {
		exists[n.Slug] = true
	}
	nodes := []gNode{}
	edges := []gEdge{}
	seen := map[string]bool{}
	for _, n := range notes {
		st := n.Meta.Status
		if st == "" {
			st = "new"
		}
		title := n.Meta.Title
		if title == "" {
			title = n.Slug
		}
		nodes = append(nodes, gNode{ID: n.Slug, Label: title, Status: st, Mastery: n.Meta.Mastery,
			Roadmap: n.Meta.Roadmap, Order: n.Meta.Order, Source: n.Meta.Source, Field: n.Meta.Field})
		for _, req := range n.Meta.Requires {
			if exists[req] {
				seen[req+">"+n.Slug] = true
				edges = append(edges, gEdge{From: req, To: n.Slug, Kind: "requires"})
			}
		}
	}
	for _, n := range notes {
		for _, to := range n.Links() {
			if !exists[to] || to == n.Slug || seen[n.Slug+">"+to] || seen[to+">"+n.Slug] {
				continue
			}
			seen[n.Slug+">"+to] = true
			edges = append(edges, gEdge{From: n.Slug, To: to, Kind: "related"})
		}
	}
	writeJSON(w, 200, map[string]any{"nodes": nodes, "edges": edges})
}

type linkRef struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

func (s *Server) note(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if vault.Slugify(slug) != slug || slug == "" {
		fail(w, 400, "tema inválido")
		return
	}
	n, err := s.V.Load(slug)
	if err != nil {
		fail(w, 404, "no existe ese tema")
		return
	}
	title := func(sl string) string {
		if o, err := s.V.Load(sl); err == nil && o.Meta.Title != "" {
			return o.Meta.Title
		}
		return sl
	}
	var back []linkRef
	if bl, err := s.V.Backlinks(slug); err == nil {
		for _, b := range bl {
			back = append(back, linkRef{b, title(b)})
		}
	}
	var req []linkRef
	for _, q := range n.Meta.Requires {
		req = append(req, linkRef{q, title(q)})
	}
	_, simErr := s.V.LoadInteractive(slug)
	writeJSON(w, 200, map[string]any{
		"slug": slug, "meta": n.Meta, "body": n.Body,
		"backlinks": back, "requires": req, "hasSimulation": simErr == nil,
	})
}

// answerTimeout bounds a short answer (base concepts, a doubt). They are small, so a model
// that has not replied by then is stuck.
const answerTimeout = 3 * time.Minute

// shortSession is a session for actions that do not need a conversation with the learner:
// they take their input from the request and send the result back in the response.
func (s *Server) shortSession() *session.Session {
	return &session.Session{P: s.P, V: s.V, Cfg: s.Cfg, CheckLinks: s.CheckLinks}
}

// drain reads what is left of the request body. Go only notices that the browser closed the connection
// once the body is read to its end, and a short action is cancelled that way: the browser aborts the
// request, the request's context ends, and the model is stopped.
func drain(r *http.Request) { io.Copy(io.Discard, r.Body) }

// noteSlug reads the {slug} of a note route, or answers 400.
func noteSlug(w http.ResponseWriter, r *http.Request) (string, bool) {
	slug := r.PathValue("slug")
	if slug == "" || vault.Slugify(slug) != slug {
		fail(w, 400, "tema inválido")
		return "", false
	}
	return slug, true
}

// extend writes the base concepts of a note and answers with the text, to put in the article.
func (s *Server) extend(w http.ResponseWriter, r *http.Request) {
	slug, ok := noteSlug(w, r)
	if !ok {
		return
	}
	drain(r)
	ctx, cancel := context.WithTimeout(r.Context(), answerTimeout)
	defer cancel()
	text, err := s.shortSession().Extend(ctx, slug)
	if err != nil {
		fail(w, shortStatus(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"text": text})
}

// doubt answers a question about a note and saves it under the note's "Dudas" section.
func (s *Server) doubt(w http.ResponseWriter, r *http.Request) {
	slug, ok := noteSlug(w, r)
	if !ok {
		return
	}
	var body struct {
		Question string `json:"question"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	drain(r)
	ctx, cancel := context.WithTimeout(r.Context(), answerTimeout)
	defer cancel()
	ans, err := s.shortSession().Doubt(ctx, slug, body.Question)
	if err != nil {
		fail(w, shortStatus(err), err.Error())
		return
	}
	writeJSON(w, 200, ans)
}

// concept adds a concept a doubt brought up to the vault as a new note.
func (s *Server) concept(w http.ResponseWriter, r *http.Request) {
	slug, ok := noteSlug(w, r)
	if !ok {
		return
	}
	var body struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	got, created, err := s.shortSession().AddConcept(slug, body.Title, body.Summary)
	if err != nil {
		fail(w, shortStatus(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"slug": got, "created": created})
}

// shortStatus tells what the learner did wrong (no guide yet, empty question) from what failed on our side.
func shortStatus(err error) int {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		return 499 // the browser went away; nobody reads this
	case session.IsInputError(err):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func (s *Server) roadmaps(w http.ResponseWriter, r *http.Request) {
	rms, err := s.V.ListIn("roadmaps")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	topics, err := s.V.List()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	type item struct {
		Slug   string   `json:"slug"`
		Title  string   `json:"title"`
		Total  int      `json:"total"`
		Done   int      `json:"done"`
		Next   *linkRef `json:"next"`
		Source string   `json:"source,omitempty"`
	}
	out := []item{}
	for _, rm := range rms {
		it := item{Slug: rm.Slug, Title: rm.Meta.Title, Source: rm.Meta.Source}
		var in []vault.Note
		for _, t := range topics {
			if t.Meta.Roadmap == rm.Slug {
				in = append(in, t)
				if t.Meta.Status == "explained" {
					it.Done++
				}
			}
		}
		it.Total = len(in)
		if n, ok := roadmap.Next(in); ok {
			it.Next = &linkRef{n.Slug, n.Meta.Title}
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	writeJSON(w, 200, out)
}

func (s *Server) daily(w http.ResponseWriter, r *http.Request) {
	date := s.Now().Format("2006-01-02")
	d, err := s.V.LoadFrom("daily", date)
	if err != nil {
		writeJSON(w, 200, map[string]any{"date": date})
		return
	}
	slug := vault.Slugify(d.Meta.Title)
	res := map[string]any{"date": date, "topic": d.Meta.Title, "slug": slug, "field": d.Meta.Field}
	if n, err := s.V.Load(slug); err == nil {
		res["status"], res["mastery"] = n.Meta.Status, n.Meta.Mastery
	}
	writeJSON(w, 200, res)
}

var safeName = regexp.MustCompile(`[^\p{L}\p{N}._ -]+`)

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		fail(w, 400, "subida inválida: "+err.Error())
		return
	}
	defer r.MultipartForm.RemoveAll()
	var ids []string
	for _, fh := range r.MultipartForm.File["files"] {
		name := strings.Trim(safeName.ReplaceAllString(filepath.Base(fh.Filename), "_"), ". ")
		if name == "" {
			name = "archivo"
		}
		var b [8]byte
		rand.Read(b[:])
		id := hex.EncodeToString(b[:]) + "/" + name
		dst := filepath.Join(s.uploadDir(), filepath.FromSlash(id))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			fail(w, 500, err.Error())
			return
		}
		src, err := fh.Open()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		out, err := os.Create(dst)
		if err != nil {
			src.Close()
			fail(w, 500, err.Error())
			return
		}
		_, err = io.Copy(out, src)
		src.Close()
		out.Close()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		fail(w, 400, "no llegó ningún archivo")
		return
	}
	writeJSON(w, 200, map[string]any{"files": ids})
}

// sessions lists the history, newest first.
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	runs := make([]*Run, 0, len(s.runs))
	for _, run := range s.runs {
		runs = append(runs, run)
	}
	s.mu.Unlock()
	out := make([]summary, 0, len(runs))
	for _, run := range runs {
		out = append(out, run.summary())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	writeJSON(w, 200, out)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	run := s.run(r)
	if run == nil {
		fail(w, 404, "sesión desconocida")
		return
	}
	if !run.isDone() {
		fail(w, http.StatusConflict, "la sesión sigue en curso; cancélala primero")
		return
	}
	s.mu.Lock()
	delete(s.runs, run.ID)
	if s.active == run {
		s.active = nil
	}
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.remove(run.ID); err != nil {
			fail(w, 500, err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	var req StartReq
	if !readJSON(w, r, &req) {
		return
	}
	run, err := s.StartRun(req)
	switch {
	case errors.Is(err, errBusy):
		fail(w, http.StatusConflict, err.Error())
	case err != nil:
		fail(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, 200, map[string]any{"id": run.ID})
	}
}

func (s *Server) run(r *http.Request) *Run {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[id]
}

// events streams a run's events as Server-Sent Events. ?from=N resumes after a reconnect.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	run := s.run(r)
	if run == nil {
		fail(w, 404, "sesión desconocida")
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "streaming no soportado")
		return
	}
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		// the browser reconnected: continue right after the last event it received
		if n, err := strconv.Atoi(last); err == nil {
			from = n + 1
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	fl.Flush()

	wake := run.subscribe()
	defer run.unsubscribe(wake)
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		evs, done := run.since(from)
		for _, e := range evs {
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", from, data)
			from++
		}
		fl.Flush()
		if done && len(evs) == 0 {
			return
		}
		if len(evs) > 0 {
			continue // check again before sleeping: more may have arrived
		}
		select {
		case <-wake:
		case <-keepalive.C:
			io.WriteString(w, ": keepalive\n\n")
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) input(w http.ResponseWriter, r *http.Request) {
	run := s.run(r)
	if run == nil {
		fail(w, 404, "sesión desconocida")
		return
	}
	var body struct {
		Text   string `json:"text"`
		Finish bool   `json:"finish"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := run.send(body.Text, body.Finish); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	run := s.run(r)
	if run == nil {
		fail(w, 404, "sesión desconocida")
		return
	}
	run.cancel()
	run.in.Close()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// interactive serves a generated simulation locked down: it runs in an opaque
// origin, can use inline script and style, and cannot reach the network or the app.
func (s *Server) interactive(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	slug := strings.TrimSuffix(file, ".html")
	if !strings.HasSuffix(file, ".html") || vault.Slugify(slug) != slug || slug == "" {
		http.NotFound(w, r)
		return
	}
	html, err := s.V.LoadInteractive(slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	io.WriteString(w, html)
}

// Listen starts the server on the loopback interface.
func (s *Server) Listen(port int) (net.Listener, error) {
	return net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}
