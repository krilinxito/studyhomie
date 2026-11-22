package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// Limits of what a lookup sends: a selection can be a word, a phrase or a passage, never a document.
const (
	maxDefineText    = 800
	maxDefineContext = 2500
)

// DefineTurn is one follow-up of a lookup: the learner's question and the answer.
type DefineTurn struct {
	Q string `json:"q"`
	A string `json:"a"`
}

// DefineReq asks to explain something the learner selected or searched for.
type DefineReq struct {
	Text    string `json:"text"`    // a word, a group of words or a sentence
	Context string `json:"context"` // the paragraph it was selected from
	Guide   string `json:"guide"`   // the guide or concept note it came from, if any
	// Deeper asks for more detail than Previous, the explanation already given.
	Deeper   bool         `json:"deeper"`
	Previous string       `json:"previous"`
	Question string       `json:"question"` // a follow-up question about it
	History  []DefineTurn `json:"history"`  // earlier follow-ups
}

// Definition is a short explanation of a selection, in its context.
type Definition struct {
	Title   string `json:"title"`   // a short name for the idea: the term, or what the sentence says
	Summary string `json:"summary"` // its gist, in one sentence
	Kind    string `json:"kind"`    // term | passage
	Text    string `json:"text"`    // the explanation, in Markdown
}

// FindConcept returns the concept note the vault already has for a term, if any (same id, or the
// same idea spelled another way). A passage never matches.
func (s *Session) FindConcept(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || len(strings.Fields(text)) > 6 {
		return "", false
	}
	if id := vault.ConceptID(text); id != "" && s.V.ConceptExists(id) {
		return id, true
	}
	if k, ok := s.conceptIndex().find(text); ok && k.Written {
		return k.ID, true
	}
	return "", false
}

// Define explains a selection as it is used where it was found. It is a short answer: no run, no
// note is written until the learner saves it.
func (s *Session) Define(ctx context.Context, req DefineReq) (Definition, error) {
	text := strings.Join(strings.Fields(req.Text), " ")
	if text == "" {
		return Definition{}, inputErrorf("¿qué te explico?")
	}
	if len([]rune(text)) > maxDefineText {
		return Definition{}, inputErrorf("es demasiado texto para explicarlo de una vez (máximo %d caracteres)", maxDefineText)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "SELECTED:\n%s\n", text)
	if c := strings.TrimSpace(req.Context); c != "" && c != text {
		fmt.Fprintf(&b, "\nPARAGRAPH IT CAME FROM:\n%s\n", head(c, maxDefineContext))
	}
	if g := vault.Slugify(req.Guide); g != "" {
		if n, err := s.V.Load(g); err == nil {
			fmt.Fprintf(&b, "\nTOPIC BEING STUDIED: %s\nGuide summary: %s\n", n.Meta.Title, head(s.noteContext(g, n), 1500))
		} else if c, err := s.V.LoadConcept(g); err == nil {
			fmt.Fprintf(&b, "\nCONCEPT NOTE BEING READ: %s\n%s\n", c.Meta.Title, c.Meta.Summary)
		}
	}
	if p := strings.TrimSpace(req.Previous); p != "" {
		fmt.Fprintf(&b, "\nEXPLANATION ALREADY GIVEN:\n%s\n", head(p, 3000))
	}
	for _, t := range req.History {
		fmt.Fprintf(&b, "\nLearner: %s\nYou: %s\n", head(t.Q, 800), head(t.A, 2000))
	}
	switch q := strings.TrimSpace(req.Question); {
	case q != "":
		if len([]rune(q)) > maxQuestion {
			return Definition{}, inputErrorf("la duda es demasiado larga (máximo %d caracteres)", maxQuestion)
		}
		fmt.Fprintf(&b, "\nFOLLOW-UP QUESTION:\n%s\n", q)
	case req.Deeper:
		b.WriteString("\nDEEPER: explain it in more depth.\n")
	}
	resp, err := s.quickConvo(prompts.Get("define", s.aiLang())).Say(ctx, b.String(), nil)
	if err != nil {
		return Definition{}, err
	}
	body, payload := splitJSON(resp.Text)
	d := Definition{Text: strings.TrimSpace(body)}
	if d.Text == "" {
		return Definition{}, failf("la respuesta salió vacía")
	}
	if payload != "" {
		_ = decodeJSON(payload, &d) // the explanation stands without its title
		d.Text = strings.TrimSpace(body)
	}
	d.Title = strings.TrimSpace(d.Title)
	if d.Title == "" || len([]rune(d.Title)) > 80 {
		d.Title = head(text, 60)
	}
	if d.Kind != "term" && d.Kind != "passage" {
		d.Kind = "passage"
		if len(strings.Fields(text)) <= 4 {
			d.Kind = "term"
		}
	}
	return d, nil
}

// SaveDefinition keeps an explanation as a concept note, written from the explanation itself (no
// new AI call). From a guide, the concept joins its iceberg. A concept the vault already has is
// linked, not overwritten. It returns the concept id and whether it was created.
func (s *Session) SaveDefinition(title, summary, text, guide string) (string, bool, error) {
	title = strings.TrimSpace(title)
	id := vault.ConceptID(title)
	if id == "" {
		return "", false, inputErrorf("el concepto necesita un nombre")
	}
	if strings.TrimSpace(text) == "" {
		return "", false, inputErrorf("no hay explicación que guardar")
	}
	guide = vault.Slugify(guide)
	var g *vault.Note
	if guide != "" {
		if n, err := s.V.Load(guide); err == nil {
			g = &n
		}
	}
	if !s.V.ConceptExists(id) {
		if k, ok := s.conceptIndex().find(title); ok && k.Written {
			id = k.ID // the vault has it under another spelling
		}
	}
	created := false
	if !s.V.ConceptExists(id) {
		var body strings.Builder
		if sum := strings.TrimSpace(summary); sum != "" {
			fmt.Fprintf(&body, "## %s\n\n%s\n\n", s.h("En una frase"), sum)
		}
		fmt.Fprintf(&body, "## %s\n\n%s\n\n", s.h("Intuición"), strings.TrimSpace(text))
		c := vault.Concept{Slug: id, Meta: vault.ConceptMeta{Title: title, Summary: strings.TrimSpace(summary), Tags: []string{"explicado"}}}
		if g != nil {
			c.Meta.Guides = []string{guide}
			if g.Meta.Area != "" {
				c.Meta.Category = g.Meta.Area
			}
			fmt.Fprintf(&body, "## %s\n\n%s\n", s.h("Aparece en"), appearsLine(guide, g.Meta.Title))
		} else {
			fmt.Fprintf(&body, "## %s\n\n%s\n", s.h("Aparece en"), s.tr("Lo buscaste para entenderlo."))
		}
		c.Body = body.String()
		if err := s.V.SaveConcept(c); err != nil {
			return "", false, err
		}
		created = true
	} else if g != nil {
		if err := s.linkConcept(id, guide, g.Meta.Title); err != nil {
			return "", false, err
		}
	}
	if g != nil {
		// into the guide's iceberg: written now, so out of what is still under water
		err := s.V.Update(guide, func(n *vault.Note) error {
			has := false
			for _, x := range n.Meta.Concepts {
				has = has || x == id
			}
			if !has {
				n.Meta.Concepts = append(n.Meta.Concepts, id)
			}
			var keep []vault.PendingConcept
			for _, p := range n.Meta.Pending {
				if p.ID != id {
					keep = append(keep, p)
				}
			}
			n.Meta.Pending = keep
			return nil
		})
		if err != nil {
			return "", false, err
		}
	}
	return id, created, nil
}
