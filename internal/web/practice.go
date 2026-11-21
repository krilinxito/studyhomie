package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/krilinxito/studyhomie/internal/i18n"
	"github.com/krilinxito/studyhomie/internal/session"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// Practice states, as stored and sent to the browser.
const (
	practiceRunning   = "running"   // a run shows it right now
	practicePaused    = "paused"    // put aside: it can be resumed, finished now or discarded
	practiceDone      = "done"      // finished and graded
	practicePartial   = "partial"   // closed early: graded on what was answered
	practiceDiscarded = "discarded" // closed without anything to grade
)

// practiceRec is one quiz or Feynman session as kept in .studyhomie/practice/<id>.json.
type practiceRec struct {
	ID      string           `json:"id"`
	Kind    string           `json:"kind"`
	Slug    string           `json:"slug"`
	Title   string           `json:"title"`
	Status  string           `json:"status"`
	Run     int              `json:"run,omitempty"` // the session that shows it
	Created time.Time        `json:"created"`
	Updated time.Time        `json:"updated"`
	Error   string           `json:"error,omitempty"` // why it stopped, when the learner did not stop it
	State   session.Practice `json:"state"`
}

// practiceInfo is a practice as the browser lists it: no questions, no reference answers.
type practiceInfo struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Slug       string    `json:"slug"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	Run        int       `json:"run,omitempty"`
	Created    time.Time `json:"created"`
	Updated    time.Time `json:"updated"`
	Error      string    `json:"error,omitempty"`
	Answered   int       `json:"answered"`
	Total      int       `json:"total"`
	Difficulty string    `json:"difficulty,omitempty"`
	Score      int       `json:"score,omitempty"`
	Waiting    bool      `json:"waiting,omitempty"` // live and waiting for the learner
}

func (s *Server) practiceDir() string {
	return filepath.Join(s.vault().Root, filepath.FromSlash(vault.PracticeDir))
}

// validPracticeID accepts the ids newPracticeID makes: a date, a time and random hex.
func validPracticeID(id string) bool {
	if len(id) < 8 || len(id) > 40 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c == '-') {
			return false
		}
	}
	return true
}

func newPracticeID(now time.Time) string {
	b := make([]byte, 3)
	rand.Read(b)
	return now.Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// writePractice saves a record; s.prMu must be held.
func (s *Server) writePractice(rec *practiceRec) error {
	dir := s.practiceDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(dir, rec.ID+".json")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Server) readPractice(id string) (*practiceRec, error) {
	if !validPracticeID(id) {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(filepath.Join(s.practiceDir(), id+".json"))
	if err != nil {
		return nil, err
	}
	var rec practiceRec
	if err := json.Unmarshal(data, &rec); err != nil || rec.ID != id {
		return nil, os.ErrNotExist
	}
	return &rec, nil
}

// practice finds a practice: the live record when a run has it, else the file.
func (s *Server) practice(id string) (*practiceRec, error) {
	s.mu.Lock()
	rec := s.practices[id]
	s.mu.Unlock()
	if rec != nil {
		return rec, nil
	}
	return s.readPractice(id)
}

// pauseStalePractices marks the practices a closed app left running as paused. Called at start
// and when another vault is put in use, before any run exists.
func (s *Server) pauseStalePractices() {
	entries, err := os.ReadDir(s.practiceDir())
	if err != nil {
		return
	}
	s.prMu.Lock()
	defer s.prMu.Unlock()
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		if rec, err := s.readPractice(id); err == nil && rec.Status == practiceRunning {
			rec.Status = practicePaused
			s.writePractice(rec)
		}
	}
}

func (s *Server) info(rec *practiceRec) practiceInfo {
	s.prMu.Lock()
	pi := practiceInfo{
		ID: rec.ID, Kind: rec.Kind, Slug: rec.Slug, Title: rec.Title, Status: rec.Status, Run: rec.Run,
		Created: rec.Created, Updated: rec.Updated, Error: rec.Error,
		Answered: rec.State.Answered(), Total: rec.State.Total(), Difficulty: rec.State.Difficulty, Score: rec.State.Score,
	}
	s.prMu.Unlock()
	if pi.Status == practiceRunning {
		if r := s.runByID(pi.Run); r != nil {
			w := r.waiting()
			pi.Waiting = w == "line" || w == "block"
		}
	}
	return pi
}

func (s *Server) runByID(id int) *Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[id]
}

// startPractice opens a new quiz or Feynman on a guide and runs it next to whatever else runs.
func (s *Server) startPractice(p session.Practice) (*practiceRec, *Run, error) {
	p.Slug = vault.Slugify(p.Slug)
	if p.Kind != session.PracticeQuiz && p.Kind != session.PracticeFeynman {
		return nil, nil, errors.New(i18n.N("esa práctica no existe"))
	}
	note, err := s.vault().Load(p.Slug)
	if err != nil {
		return nil, nil, errors.New(i18n.N("primero abre o genera una guía"))
	}
	if p.Kind == session.PracticeQuiz {
		if p.N < 1 || p.N > 30 {
			p.N = 5
		}
		if p.Difficulty == "" {
			p.Difficulty = "media"
		}
	}
	title := note.Meta.Title
	if title == "" {
		title = p.Slug
	}
	now := s.Now()
	rec := &practiceRec{ID: newPracticeID(now), Kind: p.Kind, Slug: p.Slug, Title: title, Status: practiceRunning, Created: now, Updated: now, State: p}
	rec.State.ID = rec.ID
	run, err := s.runPractice(rec, false, 0)
	if err != nil {
		return nil, nil, err
	}
	return rec, run, nil
}

// runPractice starts a run for a practice: a new one, or one resumed from where it was saved, in
// which case the paused session's transcript carries over to the new run. finishNow grades what
// was answered right away.
func (s *Server) runPractice(rec *practiceRec, finishNow bool, carry int) (*Run, error) {
	s.mu.Lock()
	if old := s.practices[rec.ID]; old != nil {
		s.mu.Unlock()
		return nil, errPracticeLive
	}
	run, ctx := s.newRunLocked(rec.Kind, rec.Title, rec.Slug)
	run.Practice = rec.ID
	s.practices[rec.ID] = rec
	s.mu.Unlock()
	if carry > 0 {
		s.carryOver(run, carry)
		run.dropUnanswered()
	}

	s.prMu.Lock()
	rec.Run, rec.Status, rec.Error, rec.Updated = run.ID, practiceRunning, "", s.Now()
	s.writePractice(rec)
	p := rec.State
	s.prMu.Unlock()
	run.scheduleSave(false)

	sess, cfg := s.runSession(run)
	sess.Checkpoint = func(st session.Practice) {
		s.prMu.Lock()
		defer s.prMu.Unlock()
		rec.State, rec.Updated = st, s.Now()
		s.writePractice(rec)
	}
	p.ID, p.FinishNow = rec.ID, finishNow
	go func() {
		defer run.cancel()
		var err error
		if p.Kind == session.PracticeFeynman {
			err = sess.FeynmanFrom(ctx, &p)
		} else {
			err = sess.QuizFrom(ctx, &p)
		}
		s.prMu.Lock()
		rec.State, rec.Updated = p, s.Now()
		rec.State.FinishNow = false
		switch {
		case p.Outcome == session.OutcomeDone:
			rec.Status = practiceDone
		case p.Outcome == session.OutcomePartial:
			rec.Status = practicePartial
		case p.Outcome == session.OutcomeEmpty:
			rec.Status = practiceDiscarded
		default:
			// cut short (paused, stopped, closed, or the AI failed): it can be resumed
			rec.Status = practicePaused
			if err != nil && ctx.Err() == nil {
				rec.Error = s.errText(err)
			}
		}
		s.writePractice(rec)
		s.prMu.Unlock()
		if rec.Status == practicePaused && rec.Error == "" {
			run.mu.Lock()
			run.paused = true
			run.mu.Unlock()
		}
		s.mu.Lock()
		if s.practices[rec.ID] == rec {
			delete(s.practices, rec.ID)
		}
		s.mu.Unlock()
		run.finish(ctx, err, cfg.Language)
	}()
	return run, nil
}

var (
	errPracticeLive = errors.New(i18n.N("esa práctica ya está abierta"))
	errPracticeOver = errors.New(i18n.N("esa práctica ya terminó"))
	errPracticeBusy = errors.New(i18n.N("espera a que termine lo que está haciendo y vuelve a intentarlo"))
	errPracticeGone = errors.New(i18n.N("esa práctica ya no existe"))
)

// dropUnanswered removes the tail of a resumed practice that nobody answered: the question
// it was waiting on comes again, so it would show twice.
func (r *Run) dropUnanswered() {
	r.mu.Lock()
	defer r.mu.Unlock()
	cut := len(r.events)
	for i := len(r.events) - 1; i >= 0; i-- {
		e := r.events[i]
		if e.Type == "in" || e.Type == "out" {
			break
		}
		if e.Type == "ask" || e.Type == "card" && e.Card != nil && (e.Card.Kind == session.CardQuestion || e.Card.Kind == session.CardStatus || e.Card.Kind == session.CardPhase) {
			cut = i
			continue
		}
		break
	}
	r.events = r.events[:cut]
}

// pause puts a live practice aside: its run ends quietly and it waits to be resumed.
func (r *Run) pause() {
	r.mu.Lock()
	r.paused = true
	r.mu.Unlock()
	r.cancel()
	r.in.Close()
}

// waitDone waits a little for a run to end (a pause cancels the AI call it was making).
func (r *Run) waitDone(d time.Duration) {
	deadline := time.Now().Add(d)
	for !r.isDone() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
}

// finishNow asks a practice waiting for an answer to close now.
func (r *Run) finishNow() error {
	r.mu.Lock()
	kind := r.askKind
	if kind != "line" && kind != "block" {
		r.mu.Unlock()
		return errPracticeBusy
	}
	r.askKind = ""
	r.mu.Unlock()
	r.emit(Event{Type: "in", Text: i18n.T(r.lang, "Terminar ahora")})
	_, err := io.WriteString(r.in, session.FinishNow+"\n")
	return err
}

/* ---------- API ---------- */

// listPractices: the open ones (live or paused) and the latest that ended, newest first.
func (s *Server) listPractices(w http.ResponseWriter, r *http.Request) {
	entries, _ := os.ReadDir(s.practiceDir())
	var open, ended []practiceInfo
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		rec, err := s.practice(id)
		if err != nil {
			continue
		}
		pi := s.info(rec)
		if pi.Status == practiceRunning || pi.Status == practicePaused {
			open = append(open, pi)
		} else {
			ended = append(ended, pi)
		}
	}
	newest := func(l []practiceInfo) {
		sort.Slice(l, func(i, j int) bool { return l[i].Updated.After(l[j].Updated) })
	}
	newest(open)
	newest(ended)
	if len(ended) > 20 {
		ended = ended[:20]
	}
	if open == nil {
		open = []practiceInfo{}
	}
	if ended == nil {
		ended = []practiceInfo{}
	}
	writeJSON(w, 200, map[string]any{"open": open, "recent": ended})
}

func (s *Server) newPractice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind       string `json:"kind"`
		Slug       string `json:"slug"`
		N          int    `json:"n"`
		Difficulty string `json:"difficulty"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	rec, run, err := s.startPractice(session.Practice{Kind: body.Kind, Slug: body.Slug, N: body.N, Difficulty: body.Difficulty})
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"practice": s.info(rec), "run": run.ID})
}

// pathPractice reads the {id} of a practice route, or answers 404.
func (s *Server) pathPractice(w http.ResponseWriter, r *http.Request) *practiceRec {
	rec, err := s.practice(r.PathValue("id"))
	if err != nil {
		fail(w, 404, errPracticeGone.Error())
		return nil
	}
	return rec
}

func (s *Server) liveRun(rec *practiceRec) *Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.practices[rec.ID] != rec {
		return nil
	}
	if r := s.runs[rec.Run]; r != nil && !r.isDone() {
		return r
	}
	return nil
}

func (s *Server) resumePractice(w http.ResponseWriter, r *http.Request) {
	rec := s.pathPractice(w, r)
	if rec == nil {
		return
	}
	if run := s.liveRun(rec); run != nil {
		writeJSON(w, 200, map[string]any{"practice": s.info(rec), "run": run.ID})
		return
	}
	if rec.Status != practicePaused && rec.Status != practiceRunning {
		fail(w, http.StatusConflict, errPracticeOver.Error())
		return
	}
	run, err := s.runPractice(rec, false, rec.Run)
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"practice": s.info(rec), "run": run.ID})
}

func (s *Server) pausePractice(w http.ResponseWriter, r *http.Request) {
	rec := s.pathPractice(w, r)
	if rec == nil {
		return
	}
	if run := s.liveRun(rec); run != nil {
		run.pause()
		run.waitDone(3 * time.Second)
	}
	writeJSON(w, 200, map[string]any{"practice": s.info(rec)})
}

// finishPractice closes a practice now: what was answered is graded, the rest counts as blank, and
// the topic's mastery does not change. A live one that is still working has to wait.
func (s *Server) finishPractice(w http.ResponseWriter, r *http.Request) {
	rec := s.pathPractice(w, r)
	if rec == nil {
		return
	}
	if run := s.liveRun(rec); run != nil {
		if err := run.finishNow(); err != nil {
			fail(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"practice": s.info(rec), "run": run.ID})
		return
	}
	if rec.Status != practicePaused {
		fail(w, http.StatusConflict, errPracticeOver.Error())
		return
	}
	if rec.State.Answered() == 0 {
		// nothing to grade: it closes without a score, and the history keeps the session
		s.prMu.Lock()
		rec.Status, rec.Updated = practiceDiscarded, s.Now()
		s.writePractice(rec)
		s.prMu.Unlock()
		writeJSON(w, 200, map[string]any{"practice": s.info(rec), "empty": true})
		return
	}
	run, err := s.runPractice(rec, true, rec.Run)
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"practice": s.info(rec), "run": run.ID})
}

// discardPractice drops a practice without grading it: its file goes to the trash.
func (s *Server) discardPractice(w http.ResponseWriter, r *http.Request) {
	rec := s.pathPractice(w, r)
	if rec == nil {
		return
	}
	if run := s.liveRun(rec); run != nil {
		run.pause()
		run.waitDone(5 * time.Second)
		if !run.isDone() {
			fail(w, http.StatusConflict, errPracticeBusy.Error())
			return
		}
	}
	kind := i18n.T(s.lang(), "Preguntas")
	if rec.Kind == session.PracticeFeynman {
		kind = i18n.T(s.lang(), "Explicación")
	}
	s.prMu.Lock()
	item, err := s.vault().Trash(vault.TrashItem{
		Kind: vault.CatPractice, Slug: rec.ID, Title: kind + " · " + rec.Title,
		Files: []string{vault.PracticeDir + "/" + rec.ID + ".json"},
	}, nil)
	s.prMu.Unlock()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "trash": item})
}

// pauseAll puts every live practice aside and waits for their runs to end (switching vaults).
func (s *Server) pauseAll() {
	s.mu.Lock()
	var live []*Run
	for _, rec := range s.practices {
		if r := s.runs[rec.Run]; r != nil && !r.isDone() {
			live = append(live, r)
		}
	}
	s.mu.Unlock()
	for _, r := range live {
		r.pause()
	}
	for _, r := range live {
		r.waitDone(5 * time.Second)
	}
}
