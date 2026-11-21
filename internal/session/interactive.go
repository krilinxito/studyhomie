package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/vault"
	"github.com/krilinxito/studyhomie/internal/viz"
)

const maxInteractiveBytes = viz.MaxHTMLBytes

var htmlFenceRe = regexp.MustCompile("(?s)```html\\s*\n(.*?)```")

// validateInteractive checks a generated page is self-contained, within size limits,
// and only uses allowed libraries and sandbox-safe APIs.
func validateInteractive(html string) error {
	return viz.ValidateHTML(html)
}

func extractHTML(s string) string {
	if m := htmlFenceRe.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(s)
}

// pendingInteractive returns the description of an interactive resource that the
// guide proposed to build (type interactive without a URL).
func pendingInteractive(n vault.Note) (why string, ok bool) {
	for _, r := range n.Meta.Resources {
		if r.Type == "interactive" && r.URL == "" {
			return r.Why, true
		}
	}
	return "", false
}

// noteArea returns the canonical area of a guide note, falling back to its daily field if set.
func noteArea(n vault.Note) string {
	if n.Meta.Area != "" {
		return n.Meta.Area
	}
	if n.Meta.Field != "" {
		return vault.NormalizeArea(n.Meta.Field)
	}
	return ""
}

// Interactive generates a self-contained simulation for the topic and links it from the note.
// brief says what the learner should be able to manipulate; empty lets the model decide.
func (s *Session) Interactive(ctx context.Context, slug, brief string) error {
	note, err := s.V.Load(slug)
	if err != nil {
		return failf("no hay nota para «%s»: %w", slug, err)
	}
	prompt := "Topic: " + note.Meta.Title
	if area := noteArea(note); area != "" {
		prompt += "\nAREA: " + area
	}
	prompt += "\nWhat the learner studied: " + s.V.LoadHandoff(slug)
	if brief != "" {
		prompt += "\nBuild this: " + brief
	}
	s.status("Generando la simulación interactiva (puede tardar)")
	html, err := s.buildHTML(ctx, prompt)
	if err != nil {
		return err
	}
	if err := s.V.SaveInteractive(slug, html); err != nil {
		return err
	}

	rel := "interactive/" + slug + ".html"
	var kept []vault.Resource
	for _, r := range note.Meta.Resources {
		if r.Type == "interactive" && (r.URL == "" || r.URL == rel) {
			continue // replaced by the generated page
		}
		kept = append(kept, r)
	}
	why := brief
	if why == "" {
		why = s.tr("Simulación generada para explorar el tema")
	}
	note.Meta.Resources = append(kept, vault.Resource{Type: "interactive", Title: s.trf("Simulación: %s", note.Meta.Title), URL: rel, Why: why})
	note.Body = s.V.SetRecursos(note.Body, s.h("Recursos"), note.Meta.Sources, note.Meta.Resources)
	if err := s.V.Save(note); err != nil {
		return err
	}
	if s.rich() {
		s.printf("Simulación lista: ábrela desde los recursos de la guía.\n")
	} else {
		s.printf("Simulación guardada en %s/%s\n", s.V.Root, rel)
	}
	return nil
}

// buildHTML asks for a self-contained page and checks it, giving the model one chance to fix it.
func (s *Session) buildHTML(ctx context.Context, prompt string) (string, error) {
	c := s.convo(prompts.Get("interactive", s.aiLang()))
	for attempt := 0; ; attempt++ {
		resp, err := c.Say(ctx, prompt, nil)
		if err != nil {
			return "", err
		}
		html := extractHTML(resp.Text)
		err = validateInteractive(html)
		if err == nil {
			return html, nil
		}
		if attempt == 1 {
			return "", failf("no logré una simulación válida: %w", err)
		}
		prompt = "That page was rejected: " + err.Error() + ". Fix it and output the complete document again."
	}
}

// VizPageOpts controls regeneration and runtime error recovery for an html widget.
type VizPageOpts struct {
	Force    bool   // rebuild from scratch even if a page was already saved
	FixError string // runtime error reported by the iframe; asks the model to repair the saved page
}

// VizPage builds the page an html widget of a guide or concept describes, saves it and records
// it in the widget, so it is built once. It returns the page's vault-relative path.
func (s *Session) VizPage(ctx context.Context, slug, brief string) (string, error) {
	return s.VizPageWithOpts(ctx, slug, brief, VizPageOpts{})
}

// VizPageWithOpts builds, regenerates, or repairs the page for an html widget.
func (s *Session) VizPageWithOpts(ctx context.Context, slug, brief string, opts VizPageOpts) (string, error) {
	brief = strings.TrimSpace(brief)
	if brief == "" {
		return "", inputErrorf("falta la descripción del visual")
	}
	var title, body, noteCtx, area string
	isGuide := s.V.Exists(slug)
	if isGuide {
		n, err := s.V.Load(slug)
		if err != nil {
			return "", err
		}
		title, body, noteCtx, area = n.Meta.Title, n.Body, s.noteContext(slug, n), noteArea(n)
	} else {
		c, err := s.V.LoadConcept(slug)
		if err != nil {
			return "", inputErrorf("no existe la nota «%s»", slug)
		}
		title, body, noteCtx = c.Meta.Title, c.Body, head(c.Body, 3000)
		if len(c.Meta.Guides) > 0 {
			if g, err := s.V.Load(c.Meta.Guides[0]); err == nil {
				area = noteArea(g)
			}
		}
		if area == "" && c.Meta.Category != "" {
			area = vault.NormalizeArea(c.Meta.Category)
		}
	}
	if _, found := viz.SetSrc(body, brief, ""); !found {
		return "", inputErrorf("la nota no tiene ese visual")
	}

	sum := sha256.Sum256([]byte(brief))
	name := slug + "-" + hex.EncodeToString(sum[:4])
	src := "interactive/" + name + ".html"

	// Return the existing page when not forcing a rebuild or repairing an error.
	if !opts.Force && strings.TrimSpace(opts.FixError) == "" {
		if _, err := s.V.LoadInteractive(name); err == nil {
			return src, nil
		}
	}

	var prompt strings.Builder
	prompt.WriteString("Topic: " + title)
	if area != "" {
		prompt.WriteString("\nAREA: " + area)
	}
	prompt.WriteString("\nWhat the learner studied: " + noteCtx)
	prompt.WriteString("\nBuild this: " + brief)

	if fixErr := strings.TrimSpace(opts.FixError); fixErr != "" {
		if prev, err := s.V.LoadInteractive(name); err == nil && prev != "" {
			prompt.WriteString("\n\nThe previous version threw this runtime error in the browser:\n")
			prompt.WriteString(head(fixErr, 600))
			prompt.WriteString("\n\nFix the error while keeping the visual design and interactivity intact. Previous HTML:\n```html\n")
			prompt.WriteString(prev)
			prompt.WriteString("\n```")
		} else {
			prompt.WriteString("\n\nAvoid this runtime error that occurred previously: " + head(fixErr, 600))
		}
	}

	html, err := s.buildHTML(ctx, prompt.String())
	if err != nil {
		return "", err
	}
	if err := s.V.SaveInteractive(name, html); err != nil {
		return "", err
	}
	set := func(b string) string { out, _ := viz.SetSrc(b, brief, src); return out }
	if isGuide {
		err = s.V.Update(slug, func(n *vault.Note) error { n.Body = set(n.Body); return nil })
	} else {
		err = s.V.UpdateConcept(slug, func(c *vault.Concept) error { c.Body = set(c.Body); return nil })
	}
	return src, err
}
