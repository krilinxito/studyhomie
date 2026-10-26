package web

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

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
	ID  int
	Cmd string

	cancel context.CancelFunc
	in     *inputBuf

	mu      sync.Mutex
	events  []Event
	subs    map[chan struct{}]struct{}
	askKind string // what the flow is waiting for; "" when it is not waiting
	done    bool
}

func (r *Run) emit(e Event) {
	r.mu.Lock()
	r.events = append(r.events, e)
	if e.Type == "ask" {
		r.askKind = e.Kind
	}
	if e.Type == "done" {
		r.done = true
	}
	for ch := range r.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	r.mu.Unlock()
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
	Topic      string   `json:"topic"`
	Slug       string   `json:"slug"`
	Subject    string   `json:"subject"`
	Field      string   `json:"field"`
	Roadmap    string   `json:"roadmap"`
	Brief      string   `json:"brief"`
	N          int      `json:"n"`
	Difficulty string   `json:"difficulty"`
	Files      []string `json:"files"`
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
	case "material":
		if len(req.Files) == 0 {
			return nil, errors.New("falta el archivo")
		}
		for _, id := range req.Files {
			if !uploadIDRe.MatchString(id) {
				return nil, fmt.Errorf("archivo no válido: %q", id)
			}
			p := filepath.Join(s.uploadDir(), filepath.FromSlash(id))
			if _, err := os.Stat(p); err != nil {
				return nil, fmt.Errorf("no encuentro el archivo subido %q", id)
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
	s.mu.Lock()
	if s.active != nil && !s.active.isDone() {
		s.mu.Unlock()
		return nil, errBusy
	}
	s.seq++
	ctx, cancel := context.WithCancel(context.Background())
	run := &Run{ID: s.seq, Cmd: req.Cmd, cancel: cancel, in: newInputBuf()}
	s.runs[run.ID] = run
	s.active = run
	s.mu.Unlock()

	sess := &session.Session{
		P: s.P, V: s.V, Cfg: s.Cfg, CheckLinks: s.CheckLinks,
		In:   bufio.NewReader(run.in),
		Out:  outWriter{run},
		Ask:  func(kind, label, def string) { run.emit(Event{Type: "ask", Kind: kind, Label: label, Def: def}) },
		Card: func(c session.Card) { run.emit(Event{Type: "card", Card: &c}) },
	}
	go func() {
		defer cancel()
		err := s.dispatch(ctx, sess, req, paths)
		run.in.Close()
		done := Event{Type: "done"}
		if err != nil && !errors.Is(err, context.Canceled) {
			done.Error = err.Error()
		}
		if ctx.Err() != nil {
			done.Error = "cancelado"
		}
		run.emit(done)
	}()
	return run, nil
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
		_, err := sess.Roadmap(ctx, req.Subject)
		return err
	case "material":
		_, err := sess.Material(ctx, paths)
		return err
	case "next":
		return sess.Next(ctx, req.Roadmap)
	case "daily":
		return sess.Daily(ctx, req.Field)
	}
	return fmt.Errorf("comando desconocido %q", req.Cmd)
}
