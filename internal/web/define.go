package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/krilinxito/studyhomie/internal/session"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// maxLookups is how many recent lookups the search box remembers.
const maxLookups = 50

// lookup is one thing the learner asked to have explained.
type lookup struct {
	Text    string    `json:"text"`
	Title   string    `json:"title"`
	Guide   string    `json:"guide,omitempty"`
	Concept string    `json:"concept,omitempty"` // the note it already had, if that is what it showed
	At      time.Time `json:"at"`
}

var lookupsMu sync.Mutex

func (s *Server) lookupsPath() string {
	return filepath.Join(s.vault().Root, ".studyhomie", "lookups.json")
}

func (s *Server) readLookups() []lookup {
	data, err := os.ReadFile(s.lookupsPath())
	if err != nil {
		return []lookup{}
	}
	var out []lookup
	if json.Unmarshal(data, &out) != nil || out == nil {
		return []lookup{}
	}
	return out
}

// remember puts a lookup first in the list, replacing an earlier one of the same text.
func (s *Server) remember(l lookup) {
	lookupsMu.Lock()
	defer lookupsMu.Unlock()
	list := []lookup{l}
	key := strings.ToLower(strings.TrimSpace(l.Text))
	for _, x := range s.readLookups() {
		if strings.ToLower(strings.TrimSpace(x.Text)) != key && len(list) < maxLookups {
			list = append(list, x)
		}
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	p := s.lookupsPath()
	if os.MkdirAll(filepath.Dir(p), 0o755) == nil {
		_ = os.WriteFile(p, data, 0o644) // the list is a convenience: failing to save it changes nothing else
	}
}

func (s *Server) lookups(w http.ResponseWriter, r *http.Request) {
	lookupsMu.Lock()
	defer lookupsMu.Unlock()
	writeJSON(w, 200, s.readLookups())
}

// existingConcept is a concept note the vault already has for what was asked.
type existingConcept struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
}

// define explains a word, a group of words or a sentence in its context. A term the vault already
// has a note for answers with that note instead, unless force asks for a fresh explanation.
func (s *Server) define(w http.ResponseWriter, r *http.Request) {
	var body struct {
		session.DefineReq
		Force bool `json:"force"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	drain(r)
	sess := s.shortSession()
	first := strings.TrimSpace(body.Question) == "" && !body.Deeper
	if first && !body.Force {
		if id, ok := sess.FindConcept(body.Text); ok {
			c, err := s.vault().LoadConcept(id)
			if err == nil {
				title := c.Meta.Title
				if title == "" {
					title = id
				}
				s.remember(lookup{Text: strings.TrimSpace(body.Text), Title: title, Guide: vault.Slugify(body.Guide), Concept: id, At: s.Now()})
				writeJSON(w, 200, map[string]any{"existing": existingConcept{Slug: id, Title: title, Summary: c.Meta.Summary}})
				return
			}
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), answerTimeout)
	defer cancel()
	d, err := sess.Define(ctx, body.DefineReq)
	if err != nil {
		fail(w, shortStatus(err), s.errText(err))
		return
	}
	if first {
		s.remember(lookup{Text: strings.Join(strings.Fields(body.Text), " "), Title: d.Title, Guide: vault.Slugify(body.Guide), At: s.Now()})
	}
	writeJSON(w, 200, map[string]any{"definition": d})
}

// newConcept keeps an explanation as a concept note, linked to the guide it came from (if any).
func (s *Server) newConcept(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
		Text    string `json:"text"`
		Guide   string `json:"guide"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	id, created, err := s.shortSession().SaveDefinition(body.Title, body.Summary, body.Text, body.Guide)
	if err != nil {
		fail(w, shortStatus(err), s.errText(err))
		return
	}
	writeJSON(w, 200, map[string]any{"slug": id, "created": created})
}

// apuntesList lists the notes a lookup can be added to: the learner's existing ones.
func (s *Server) apuntesList(w http.ResponseWriter, r *http.Request) {
	items, err := s.vault().Items(vault.CatApuntes)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	type entry struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	}
	out := []entry{}
	for _, it := range items {
		title := s.apunteTitle(it.ID)
		if title == "" {
			continue // notes of a guide that is gone
		}
		out = append(out, entry{Slug: it.ID, Title: title})
	}
	writeJSON(w, 200, out)
}

// appendApunte adds a block at the end of the learner's note on a guide or concept, creating it.
func (s *Server) appendApunte(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	var body struct {
		Text string `json:"text"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		fail(w, 400, "no hay nada que guardar")
		return
	}
	title := s.apunteTitle(slug)
	if title == "" {
		fail(w, 404, "no existe esa guía ni ese concepto")
		return
	}
	v := s.vault()
	lookupsMu.Lock() // two saves at once must not lose one another
	defer lookupsMu.Unlock()
	a, err := v.LoadApunte(slug)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	full := text
	if old := strings.TrimRight(a.Body, " \n"); old != "" {
		full = old + "\n\n" + text
	}
	if _, err := v.SaveApunte(slug, title, full); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"slug": slug, "title": title})
}
