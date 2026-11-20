package web

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/krilinxito/studyhomie/internal/session"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// Event is one message from a running flow to the browser.
type Event struct {
	Type  string `json:"type"` // out | in | ask | card | done
	Text  string `json:"text,omitempty"`
	Kind  string `json:"kind,omitempty"` // ask: line | block
	Label string `json:"label,omitempty"`
	Def   string `json:"def,omitempty"`
	Error string `json:"error,omitempty"` // done
	// Card is a structured element the browser draws instead of plain text.
	Card *session.Card `json:"card,omitempty"` // card
}

// inputBuf feeds the session's reader with what the browser sends. Writes never block.
type inputBuf struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
}

func newInputBuf() *inputBuf {
	b := &inputBuf{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *inputBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, io.ErrClosedPipe
	}
	b.buf = append(b.buf, p...)
	b.cond.Broadcast()
	return len(p), nil
}

func (b *inputBuf) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(b.buf) == 0 && !b.closed {
		b.cond.Wait()
	}
	if len(b.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.buf)
	b.buf = b.buf[n:]
	return n, nil
}

func (b *inputBuf) Close() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	b.cond.Broadcast()
}

// Run is one flow (study, roadmap...) executing in the background.
type Run struct {
	ID      int
	Cmd     string
	Title   string
	Slug    string // the topic or roadmap it works on, when known
	Started time.Time
	Ended   time.Time

	cancel context.CancelFunc
	in     *inputBuf
	store  *store // nil when sessions are not persisted

	mu        sync.Mutex
	events    []Event
	subs      map[chan struct{}]struct{}
	askKind   string // what the flow is waiting for; "" when it is not waiting
	parked    bool   // an idle chat closed to make room for another
	done      bool
	status    string
	errMsg    string
	saveTimer *time.Timer
}

func (r *Run) emit(e Event) {
	r.mu.Lock()
	r.events = append(r.events, e)
	if e.Type == "ask" {
		r.askKind = e.Kind
	}
	if e.Type == "done" {
		r.done = true
		r.Ended = time.Now()
		r.errMsg = e.Error
		switch e.Error {
		case "":
			r.status = statusDone
		case "cancelado":
			r.status = statusCancelled
		default:
			r.status = statusError
		}
	}
	for ch := range r.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	r.mu.Unlock()
	r.scheduleSave(e.Type == "done")
}

// since returns the events from index n on, and whether the run has finished.
func (r *Run) since(n int) ([]Event, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > len(r.events) {
		n = len(r.events)
	}
	return append([]Event(nil), r.events[n:]...), r.done
}

func (r *Run) subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	r.mu.Lock()
	if r.subs == nil {
		r.subs = map[chan struct{}]struct{}{}
	}
	r.subs[ch] = struct{}{}
	r.mu.Unlock()
	return ch
}

func (r *Run) unsubscribe(ch chan struct{}) {
	r.mu.Lock()
	delete(r.subs, ch)
	r.mu.Unlock()
}

// send delivers the user's answer in the form the waiting flow expects.
func (r *Run) send(text string, finish bool) error {
	r.mu.Lock()
	kind := r.askKind
	r.askKind = ""
	r.mu.Unlock()
	shown := text
	switch kind {
	case "line":
		text = strings.ReplaceAll(strings.TrimSpace(text), "\n", " ") + "\n"
		shown = strings.TrimSpace(text)
	case "block":
		// a lone "." or "/fin" line would end the block early, so neutralise them inside the text
		var lines []string
		for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			if t := strings.TrimSpace(l); t == "." || t == "/fin" {
				l = " " + t
			}
			lines = append(lines, l)
		}
		text = strings.Join(lines, "\n") + "\n"
		if finish {
			text += "/fin\n"
		} else {
			text += ".\n"
		}
	default:
		return errors.New("the session is not waiting for input")
	}
	if finish && kind == "block" {
		shown = strings.TrimSpace(shown) + "\n\n*(sesión terminada: pido la evaluación final)*"
	}
	r.emit(Event{Type: "in", Text: shown})
	_, err := io.WriteString(r.in, text)
	return err
}

type outWriter struct{ r *Run }

func (w outWriter) Write(p []byte) (int, error) {
	w.r.emit(Event{Type: "out", Text: string(p)})
	return len(p), nil
}

// StartReq is the body of POST /api/runs.
type StartReq struct {
	Cmd        string   `json:"cmd"`
	Title      string   `json:"title"` // optional label for the session history
	Topic      string   `json:"topic"`
	Slug       string   `json:"slug"`
	Subject    string   `json:"subject"`
	Field      string   `json:"field"`
	Roadmap    string   `json:"roadmap"`
	Brief      string   `json:"brief"`
	N          int      `json:"n"`
	Difficulty string   `json:"difficulty"`
	Files      []string `json:"files"`
	Reach      int      `json:"reach"` // roadmap: how much of the field it covers (1-3); 0 asks
	Depth      *int     `json:"depth"` // roadmap, material: levels of concept notes each guide gets

	// chat: the opening message, the guide open next to it, and the chat it continues
	Chat   *chatBody `json:"chat"`
	Guide  string    `json:"guide"`
	Resume int       `json:"resume"`
}

var uploadIDRe = regexp.MustCompile(`^[0-9a-f]{16}/[^/\\]+$`)

// validate checks the request has what its command needs and resolves uploads to paths.
func (s *Server) validate(req *StartReq) (paths []string, err error) {
	need := func(name, v string) error {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("falta %s", name)
		}
		return nil
	}
	slug := func() error {
		req.Slug = vault.Slugify(req.Slug)
		return need("el tema", req.Slug)
	}
	switch req.Cmd {
	case "study", "prepare":
		return nil, need("el tema", req.Topic)
	case "quiz":
		if err := slug(); err != nil {
			return nil, err
		}
		if req.N < 1 || req.N > 30 {
			req.N = 5
		}
		if req.Difficulty == "" {
			req.Difficulty = "media"
		}
	case "feynman":
		return nil, slug()
	case "interactive":
		return nil, slug()
	case "roadmap":
		return nil, need("la materia", req.Subject)
	case "next":
		req.Roadmap = vault.Slugify(req.Roadmap)
		return nil, need("el roadmap", req.Roadmap)
	case "daily":
		return nil, nil
	case "chat":
		return nil, nil
	case "material":
		if len(req.Files) == 0 {
			return nil, errors.New("falta el archivo")
		}
		for _, id := range req.Files {
			p, err := s.uploadPath(id)
			if err != nil {
				return nil, err
			}
			paths = append(paths, p)
		}
		return paths, nil
	default:
		return nil, fmt.Errorf("comando desconocido %q", req.Cmd)
	}
	return nil, nil
}

// StartRun launches a flow in the background. Only one runs at a time.
func (s *Server) StartRun(req StartReq) (*Run, error) {
	paths, err := s.validate(&req)
	if err != nil {
		return nil, err
	}
	var first *session.ChatMsg
	shown := ""
	if req.Cmd == "chat" && req.Chat != nil && !req.Chat.empty() {
		m, err := s.message(*req.Chat)
		if err != nil {
			return nil, err
		}
		first, shown = &m, req.Chat.shown()
	}
	s.mu.Lock()
	if s.active != nil && !s.active.isDone() {
		// a chat that is only waiting for a message steps aside; anything working keeps the floor
		if req.Cmd != "chat" || s.active.waiting() != "chat" {
			s.mu.Unlock()
			return nil, errBusy
		}
		s.active.park()
	}
	s.seq++
	ctx, cancel := context.WithCancel(context.Background())
	run := &Run{
		ID: s.seq, Cmd: req.Cmd, Title: s.runTitle(req), Slug: runSlug(req), Started: s.Now(),
		cancel: cancel, in: newInputBuf(), store: s.store, status: statusRunning,
	}
	s.runs[run.ID] = run
	s.active = run
	s.mu.Unlock()
	opts := session.ChatOpts{First: first, Guide: req.Guide}
	if req.Cmd == "chat" {
		if req.Resume > 0 {
			hist, slug := s.carryOver(run, req.Resume)
			opts.History = hist
			if opts.Guide == "" {
				opts.Guide = slug
			}
		}
		if shown != "" {
			run.emit(Event{Type: "in", Text: shown})
		}
	}
	run.scheduleSave(false)

	prov, cfg, _ := s.backend()
	sess := &session.Session{
		P: prov, V: s.vault(), Cfg: cfg, CheckLinks: s.CheckLinks,
		In:  bufio.NewReader(run.in),
		Out: outWriter{run},
		Ask: func(kind, label, def string) { run.emit(Event{Type: "ask", Kind: kind, Label: label, Def: def}) },
		Card: func(c session.Card) {
			if c.Kind == session.CardGuide {
				run.mu.Lock()
				run.Slug = c.Slug // the history offers to reopen the guide the chat ended on
				run.mu.Unlock()
			}
			run.emit(Event{Type: "card", Card: &c})
		},
	}
	go func() {
		defer cancel()
		var err error
		if req.Cmd == "chat" {
			err = sess.Chat(ctx, opts)
		} else {
			err = s.dispatch(ctx, sess, req, paths)
		}
		run.in.Close()
		done := Event{Type: "done"}
		if err != nil && !errors.Is(err, context.Canceled) {
			done.Error = err.Error()
		}
		if ctx.Err() != nil {
			done.Error = "cancelado"
		}
		run.mu.Lock()
		if run.parked {
			done.Error = ""
		}
		run.mu.Unlock()
		run.emit(done)
	}()
	return run, nil
}

// runSlug is the note or roadmap a request works on, or "" when it creates one.
func runSlug(req StartReq) string {
	switch req.Cmd {
	case "study", "prepare":
		return vault.Slugify(req.Topic)
	case "chat":
		return vault.Slugify(req.Guide)
	case "quiz", "feynman", "interactive":
		return req.Slug
	case "next":
		return req.Roadmap
	}
	return ""
}

// runTitle labels a session in the history: the caller's title, else a name built from the request.
func (s *Server) runTitle(req StartReq) string {
	if t := strings.TrimSpace(req.Title); t != "" {
		return truncate(t, 160)
	}
	switch req.Cmd {
	case "chat":
		if req.Chat != nil {
			if t := req.Chat.shown(); t != "" {
				return truncate(strings.Join(strings.Fields(t), " "), 80)
			}
		}
		return "Chat"
	case "roadmap":
		return truncate(req.Subject, 160)
	case "daily":
		if f := strings.TrimSpace(req.Field); f != "" && f != "*" {
			return "Reto de hoy: " + truncate(f, 60)
		}
		return "Reto de hoy"
	case "material":
		return "Material"
	case "next":
		return req.Roadmap
	case "quiz", "feynman", "interactive":
		if n, err := s.vault().Load(req.Slug); err == nil && n.Meta.Title != "" {
			return n.Meta.Title
		}
		return req.Slug
	}
	return truncate(req.Topic, 160)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

var errBusy = errors.New("ya hay una sesión en curso; termínala o cancélala primero")

func (r *Run) isDone() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.done
}

func (s *Server) dispatch(ctx context.Context, sess *session.Session, req StartReq, paths []string) error {
	switch req.Cmd {
	case "study":
		return sess.Study(ctx, req.Topic, "adhoc")
	case "prepare":
		_, err := sess.Prepare(ctx, req.Topic, "adhoc")
		return err
	case "quiz":
		return sess.Quiz(ctx, req.Slug, req.N, req.Difficulty)
	case "feynman":
		return sess.Feynman(ctx, req.Slug)
	case "interactive":
		return sess.Interactive(ctx, req.Slug, req.Brief)
	case "roadmap":
		_, err := sess.Roadmap(ctx, req.Subject, session.RoadmapOpts{Reach: req.Reach, GuideDepth: req.Depth})
		return err
	case "material":
		_, err := sess.Material(ctx, paths, session.RoadmapOpts{Reach: req.Reach, GuideDepth: req.Depth})
		return err
	case "next":
		return sess.Next(ctx, req.Roadmap)
	case "daily":
		return sess.Daily(ctx, req.Field)
	}
	return fmt.Errorf("comando desconocido %q", req.Cmd)
}
