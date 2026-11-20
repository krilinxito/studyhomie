package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/krilinxito/studyhomie/internal/session"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// chatBody is what the browser sends for a chat message, to start a chat or to continue one.
type chatBody struct {
	Text   string              `json:"text"`
	Label  string              `json:"label"` // what to show for a button press, instead of the text
	Depth  *int                `json:"depth"`
	Guide  string              `json:"guide"`
	Action *session.ChatAction `json:"action"`
	Files  []string            `json:"files"` // upload ids
	// Material: the files are study material to build a study line from, not part of a question
	Material bool `json:"material"`
}

// shown is the learner's message as the transcript shows it.
func (b chatBody) shown() string {
	if t := strings.TrimSpace(b.Label); t != "" {
		return t
	}
	names := make([]string, len(b.Files))
	for i, id := range b.Files {
		names[i] = id[strings.Index(id, "/")+1:]
	}
	t := strings.TrimSpace(b.Text)
	switch {
	case len(names) == 0:
		return t
	case t == "":
		return "Adjunto: " + strings.Join(names, ", ")
	}
	return t + "\n\n*Adjunto: " + strings.Join(names, ", ") + "*"
}

// empty reports whether the body asks for nothing.
func (b chatBody) empty() bool {
	return strings.TrimSpace(b.Text) == "" && b.Action == nil && len(b.Files) == 0
}

// message turns the body into the session's message, resolving uploads to local paths.
func (s *Server) message(b chatBody) (session.ChatMsg, error) {
	m := session.ChatMsg{Text: b.Text, Depth: b.Depth, Guide: vault.Slugify(b.Guide), Action: b.Action, Material: b.Material}
	for _, id := range b.Files {
		p, err := s.uploadPath(id)
		if err != nil {
			return m, err
		}
		m.Files = append(m.Files, p)
	}
	if len([]rune(m.Text)) > 8000 {
		return m, errors.New("el mensaje es demasiado largo (máximo 8000 caracteres)")
	}
	return m, nil
}

// uploadPath resolves an upload id to the file it names.
func (s *Server) uploadPath(id string) (string, error) {
	if !uploadIDRe.MatchString(id) {
		return "", fmt.Errorf("archivo no válido: %q", id)
	}
	p := filepath.Join(s.uploadDir(), filepath.FromSlash(id))
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("no encuentro el archivo subido %q", id)
	}
	return p, nil
}

// waiting returns what the run waits for: line, block, chat, or "" while it works.
func (r *Run) waiting() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return ""
	}
	return r.askKind
}

// sendChat delivers a chat message to a chat waiting for one.
func (r *Run) sendChat(m session.ChatMsg, shown string) error {
	r.mu.Lock()
	if r.askKind != "chat" {
		r.mu.Unlock()
		return errors.New("el chat está ocupado; espera a que termine o detenlo")
	}
	r.askKind = ""
	r.mu.Unlock()
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if shown != "" {
		r.emit(Event{Type: "in", Text: shown})
	}
	_, err = io.WriteString(r.in, string(data)+"\n")
	return err
}

// park ends an idle chat so another can start: its input closes and it finishes without error.
// It can be resumed later from its transcript.
func (r *Run) park() {
	r.mu.Lock()
	r.parked = true
	r.mu.Unlock()
	r.in.Close()
}

// history is the conversation of a stored run as plain text, for the model to pick up from.
func (r *Run) history() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, e := range mergeOut(r.events) {
		switch e.Type {
		case "in":
			b.WriteString("Learner: " + strings.TrimSpace(e.Text) + "\n\n")
		case "out":
			if t := strings.TrimSpace(e.Text); t != "" {
				b.WriteString("StudyHomie: " + t + "\n\n")
			}
		case "card":
			if e.Card != nil && e.Card.Kind == session.CardGuide {
				b.WriteString("(The app wrote the guide «" + e.Card.Topic + "».)\n\n")
			}
		}
	}
	return b.String()
}

// topics are the notes a run is about: its own, and every guide or concept it wrote or opened.
func (r *Run) topics() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	if r.Slug != "" {
		out = append(out, r.Slug)
	}
	for _, e := range r.events {
		if e.Type == "card" && e.Card != nil && e.Card.Slug != "" {
			out = append(out, e.Card.Slug)
		}
	}
	return out
}

// carryOver gives a new run the events of the chat it continues, without its closing event,
// and removes the old one: the conversation goes on under the new id.
func (s *Server) carryOver(run *Run, oldID int) (string, string) {
	s.mu.Lock()
	old := s.runs[oldID]
	if old == nil || !old.isDone() {
		s.mu.Unlock()
		return "", ""
	}
	delete(s.runs, oldID)
	s.mu.Unlock()
	hist := old.history()
	old.mu.Lock()
	evs := mergeOut(old.events)
	slug := old.Slug
	title := old.Title
	old.mu.Unlock()
	if n := len(evs); n > 0 && evs[n-1].Type == "done" {
		evs = evs[:n-1]
	}
	run.mu.Lock()
	run.events = append(evs, run.events...)
	run.Title = title
	if run.Slug == "" {
		run.Slug = slug
	}
	run.mu.Unlock()
	if s.store != nil {
		s.store.remove(oldID)
	}
	return hist, slug
}
