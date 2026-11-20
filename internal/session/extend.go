package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// Sections these short answers write into a note, in reading order: guide, base concepts, doubts.
const (
	baseHeading   = "Conceptos base"
	doubtsHeading = "Dudas"
)

// inputError is a mistake of the request, not a failure: no guide yet, an empty question.
// A caller that talks to a person shows it as it is.
type inputError struct{ msg string }

func (e *inputError) Error() string { return e.msg }

// IsInputError reports whether err is something the learner can fix by changing what they asked.
func IsInputError(err error) bool {
	var e *inputError
	return errors.As(err, &e)
}

func inputErrorf(format string, a ...any) error { return &inputError{fmt.Sprintf(format, a...)} }

// maxQuestion bounds a doubt, so a pasted document is not sent as a question.
const maxQuestion = 1500

// quickConvo is a conversation for short answers. It uses QuickModel when one is configured,
// since these replies are small and the learner is waiting for them.
func (s *Session) quickConvo(system string) *Convo {
	c := s.convo(system)
	if s.Cfg.QuickModel != "" {
		c.Model = s.Cfg.QuickModel
	}
	return c
}

// noteContext is what a short answer knows about a note: the summary the guide left
// for the later phases, or else an excerpt of the guide. It is far smaller than the guide.
func (s *Session) noteContext(slug string, n vault.Note) string {
	if h := strings.TrimSpace(s.V.LoadHandoff(slug)); h != "" {
		return h
	}
	return head(vault.Section(n.Body, "Guía"), 5000)
}

func (s *Session) loadGuided(slug string) (vault.Note, string, error) {
	note, err := s.V.Load(slug)
	if err != nil {
		return note, "", inputErrorf("no existe el tema «%s»", slug)
	}
	if vault.Section(note.Body, "Guía") == "" {
		return note, "", inputErrorf("«%s» aún no tiene guía; estúdialo primero", slug)
	}
	title := note.Meta.Title
	if title == "" {
		title = slug
	}
	return note, title, nil
}

// Extend adds the background the guide takes for granted, explained from zero, as the
// "Conceptos base" section of the note, and returns the text it wrote. It is optional:
// no phase depends on it, and running it again replaces the section.
func (s *Session) Extend(ctx context.Context, slug string) (string, error) {
	note, title, err := s.loadGuided(slug)
	if err != nil {
		return "", err
	}
	prompt := "Topic: " + title + "\n\nGUIDE SUMMARY (what the learner already has):\n" + s.noteContext(slug, note)
	resp, err := s.quickConvo(prompts.Get("extend", s.Cfg.Language)).Say(ctx, prompt, nil)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(resp.Text)
	if text == "" {
		return "", fmt.Errorf("la respuesta salió vacía")
	}
	err = s.V.Update(slug, func(n *vault.Note) error {
		n.Body = vault.PlaceSection(n.Body, baseHeading, text, "Guía")
		return nil
	})
	return text, err
}

// Concept is an idea a doubt's answer explained that could be a note of its own.
type Concept struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Slug    string `json:"slug,omitempty"`
	Exists  bool   `json:"exists,omitempty"` // already in the vault
}

// Answer is the reply to one doubt.
type Answer struct {
	Entry    string    `json:"entry"` // what was added to the note's "Dudas" section, in Markdown
	Concepts []Concept `json:"concepts"`
}

// Doubt answers a doubt about a note the way a chat would, adds the question and its answer to
// the note's "Dudas" section and returns what it added. Earlier doubts go along as context, so
// a follow-up question is understood without a conversation held in memory.
func (s *Session) Doubt(ctx context.Context, slug, question string) (Answer, error) {
	question = strings.Join(strings.Fields(question), " ")
	if question == "" {
		return Answer{}, inputErrorf("escribe tu duda")
	}
	if len([]rune(question)) > maxQuestion {
		return Answer{}, inputErrorf("la duda es demasiado larga (máximo %d caracteres)", maxQuestion)
	}
	note, title, err := s.loadGuided(slug)
	if err != nil {
		return Answer{}, err
	}

	prompt := "Topic: " + title + "\n\nGUIDE SUMMARY:\n" + s.noteContext(slug, note)
	if earlier := tail(vault.Section(note.Body, doubtsHeading), 3000); earlier != "" {
		prompt += "\n\nDOUBTS ALREADY ANSWERED:\n" + earlier
	}
	prompt += "\n\nNEW QUESTION:\n" + question
	resp, err := s.quickConvo(prompts.Get("ask", s.Cfg.Language)).Say(ctx, prompt, nil)
	if err != nil {
		return Answer{}, err
	}

	text, payload := splitJSON(resp.Text)
	text = strings.TrimSpace(text)
	if text == "" {
		return Answer{}, fmt.Errorf("la respuesta salió vacía")
	}
	var out struct {
		Concepts []Concept `json:"concepts"`
	}
	if payload != "" {
		_ = decodeJSON(payload, &out) // the answer is still good without its list of concepts
	}

	entry := doubtEntry(question, text)
	err = s.V.Update(slug, func(n *vault.Note) error {
		after := "Guía"
		if vault.Section(n.Body, baseHeading) != "" {
			after = baseHeading
		}
		all := entry
		if prev := vault.Section(n.Body, doubtsHeading); prev != "" {
			all = prev + "\n\n" + entry
		}
		n.Body = vault.PlaceSection(n.Body, doubtsHeading, all, after)
		return nil
	})
	if err != nil {
		return Answer{}, err
	}
	return Answer{Entry: entry, Concepts: s.offers(slug, out.Concepts)}, nil
}

// offers cleans the concepts the model proposed: at most two, no repeats, never the note itself,
// and marked when the vault already has them.
func (s *Session) offers(parent string, in []Concept) []Concept {
	var out []Concept
	seen := map[string]bool{parent: true}
	for _, c := range in {
		c.Title = strings.TrimSpace(c.Title)
		c.Summary = strings.TrimSpace(c.Summary)
		slug := vault.Slugify(c.Title)
		if slug == "" || seen[slug] || len(out) == 2 {
			continue
		}
		seen[slug] = true
		c.Slug = slug
		c.Exists = s.V.Exists(slug)
		out = append(out, c)
	}
	return out
}

// doubtEntry is one question and its answer as Markdown. The question is the heading, and a long
// one is kept whole in a callout below it.
func doubtEntry(question, answer string) string {
	var b strings.Builder
	short := head(question, 90)
	if short != question {
		short += "…"
	}
	b.WriteString("### " + short + "\n\n")
	if short != question {
		b.WriteString("> [!question] Tu duda\n> " + question + "\n\n")
	}
	b.WriteString(strings.TrimSpace(answer) + "\n")
	return b.String()
}

// AddConcept creates a note for a concept a doubt brought up, so it can be studied like any other
// topic. The note starts empty ("new") and points back to where the doubt came from. When the vault
// already has it, nothing is overwritten and created is false.
func (s *Session) AddConcept(parent, title, summary string) (slug string, created bool, err error) {
	title = strings.TrimSpace(title)
	slug = vault.Slugify(title)
	if slug == "" {
		return "", false, inputErrorf("el concepto necesita un nombre")
	}
	if s.V.Exists(slug) {
		return slug, false, nil
	}
	from, err := s.V.Load(parent)
	if err != nil {
		return "", false, inputErrorf("no existe el tema «%s»", parent)
	}
	fromTitle := from.Meta.Title
	if fromTitle == "" {
		fromTitle = parent
	}
	note := vault.Note{
		Slug: slug,
		Meta: vault.Meta{Title: title, Status: "new", Source: "adhoc", Summary: strings.TrimSpace(summary), Field: from.Meta.Field},
		Body: "## Conexiones\n\nSurgió de una duda en [[" + fromTitle + "]].\n",
	}
	if err := s.V.Save(note); err != nil {
		return "", false, err
	}
	return slug, true, nil
}

// head returns the first n runes of s.
func head(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// tail returns the last n runes of s.
func tail(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[len(r)-n:])
	}
	return s
}
