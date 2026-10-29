package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// saveDelay batches the writes of a running session: it grows by one event per streamed chunk.
const saveDelay = time.Second

// Session states as stored on disk and sent to the browser.
const (
	statusRunning     = "running"
	statusDone        = "done"
	statusError       = "error"
	statusCancelled   = "cancelled"
	statusInterrupted = "interrupted" // the app closed while it was running
)

const interruptedMsg = "La sesión se interrumpió al cerrar la aplicación."

// sessionFile is one session as stored in vault/.studyhomie/sessions/<id>.json.
type sessionFile struct {
	ID      int       `json:"id"`
	Cmd     string    `json:"cmd"`
	Title   string    `json:"title"`
	Slug    string    `json:"slug,omitempty"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitzero"`
	Status  string    `json:"status"`
	Error   string    `json:"error,omitempty"`
	Events  []Event   `json:"events"`
}

// store keeps sessions on disk so they survive a restart.
type store struct {
	dir string
	mu  sync.Mutex // one writer at a time, so the newest snapshot is always the last one written
}

func newStore(dir string) (*store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &store{dir: dir}, nil
}

func (st *store) path(id int) string { return filepath.Join(st.dir, strconv.Itoa(id)+".json") }

// save writes the run's current state. Failing to save never interrupts the session.
func (st *store) save(r *Run) {
	st.mu.Lock()
	defer st.mu.Unlock()
	sf := r.snapshot()
	data, err := json.Marshal(sf)
	if err != nil {
		return
	}
	tmp := st.path(sf.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, st.path(sf.ID)); err != nil {
		os.Remove(tmp)
	}
}

func (st *store) remove(id int) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := os.Remove(st.path(id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// load reads every stored session, skipping files that cannot be read.
func (st *store) load() []sessionFile {
	entries, err := os.ReadDir(st.dir)
	if err != nil {
		return nil
	}
	var out []sessionFile
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id, err := strconv.Atoi(strings.TrimSuffix(name, ".json"))
		if err != nil || id < 1 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(st.dir, name))
		if err != nil {
			continue
		}
		var sf sessionFile
		if json.Unmarshal(data, &sf) != nil || sf.ID != id {
			continue
		}
		out = append(out, sf)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// mergeOut joins consecutive streamed chunks into one event, so a stored session stays small.
func mergeOut(evs []Event) []Event {
	out := make([]Event, 0, len(evs))
	for _, e := range evs {
		if n := len(out); e.Type == "out" && n > 0 && out[n-1].Type == "out" {
			out[n-1].Text += e.Text
			continue
		}
		out = append(out, e)
	}
	return out
}

// restore turns a stored session back into a finished Run.
func restore(sf sessionFile) *Run {
	r := &Run{
		ID: sf.ID, Cmd: sf.Cmd, Title: sf.Title, Slug: sf.Slug, Started: sf.Started, Ended: sf.Ended,
		status: sf.Status, errMsg: sf.Error, events: sf.Events,
		cancel: func() {}, in: newInputBuf(), done: true,
	}
	r.in.Close()
	return r
}

// summary is one row of the session history.
type summary struct {
	ID      int       `json:"id"`
	Cmd     string    `json:"cmd"`
	Title   string    `json:"title"`
	Slug    string    `json:"slug,omitempty"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitzero"`
	Status  string    `json:"status"`
	Error   string    `json:"error,omitempty"`
}

func (r *Run) summary() summary {
	r.mu.Lock()
	defer r.mu.Unlock()
	return summary{ID: r.ID, Cmd: r.Cmd, Title: r.Title, Slug: r.Slug, Started: r.Started, Ended: r.Ended, Status: r.status, Error: r.errMsg}
}

func (r *Run) snapshot() sessionFile {
	r.mu.Lock()
	defer r.mu.Unlock()
	return sessionFile{
		ID: r.ID, Cmd: r.Cmd, Title: r.Title, Slug: r.Slug, Started: r.Started, Ended: r.Ended,
		Status: r.status, Error: r.errMsg, Events: mergeOut(r.events),
	}
}

// scheduleSave persists the run: right away when it has finished, otherwise at most once per saveDelay.
func (r *Run) scheduleSave(final bool) {
	if r.store == nil {
		return
	}
	r.mu.Lock()
	if final {
		if r.saveTimer != nil {
			r.saveTimer.Stop()
			r.saveTimer = nil
		}
		r.mu.Unlock()
		r.store.save(r)
		return
	}
	if r.saveTimer == nil {
		r.saveTimer = time.AfterFunc(saveDelay, func() {
			r.mu.Lock()
			r.saveTimer = nil
			r.mu.Unlock()
			r.store.save(r)
		})
	}
	r.mu.Unlock()
}
