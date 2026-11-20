package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/vault"
	"github.com/krilinxito/studyhomie/internal/viz"
)

const maxInteractiveBytes = 120 << 10

var (
	htmlFenceRe = regexp.MustCompile("(?s)```html\\s*\n(.*?)```")
	// anything that would reach outside the page. The sandbox blocks these too;
	// rejecting them here lets the model fix its output instead of shipping a broken page.
	externalRe = regexp.MustCompile(`(?i)(\b(src|href|action|data)\s*=\s*["']?\s*(https?:|//)|@import|url\(\s*["']?(https?:|//)|\bfetch\s*\(|XMLHttpRequest|WebSocket|importScripts|window\.open|localStorage|sessionStorage|document\.cookie)`)
)

// validateInteractive checks a generated page is self-contained and not absurdly large.
func validateInteractive(html string) error {
	switch {
	case len(html) > maxInteractiveBytes:
		return fmt.Errorf("the page is too large (%d KB, limit %d KB)", len(html)>>10, maxInteractiveBytes>>10)
	case !strings.Contains(strings.ToLower(html), "<script"):
		return fmt.Errorf("the page has no JavaScript, so nothing is interactive")
	case !strings.Contains(strings.ToLower(html), "</html>"):
		return fmt.Errorf("the HTML document is incomplete")
	}
	if m := externalRe.FindString(html); m != "" {
		return fmt.Errorf("the page uses something that is not allowed in the sandbox: %q", m)
	}
	return nil
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

// Interactive generates a self-contained simulation for the topic and links it from the note.
// brief says what the learner should be able to manipulate; empty lets the model decide.
func (s *Session) Interactive(ctx context.Context, slug, brief string) error {
	note, err := s.V.Load(slug)
	if err != nil {
		return fmt.Errorf("no hay nota para «%s»: %w", slug, err)
	}
	prompt := "Topic: " + note.Meta.Title + "\nWhat the learner studied: " + s.V.LoadHandoff(slug)
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
		why = "Simulación generada para explorar el tema"
	}
	note.Meta.Resources = append(kept, vault.Resource{Type: "interactive", Title: "Simulación: " + note.Meta.Title, URL: rel, Why: why})
	note.Body = vault.ReplaceSection(note.Body, "Recursos", resourceLines(note.Meta.Resources))
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
	c := s.convo(prompts.Get("interactive", s.Cfg.Language))
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
			return "", fmt.Errorf("no logré una simulación válida: %w", err)
		}
		prompt = "That page was rejected: " + err.Error() + ". Fix it and output the complete document again."
	}
}

// VizPage builds the page an html widget of a guide or concept describes, saves it and records
// it in the widget, so it is built once. It returns the page's vault-relative path.
func (s *Session) VizPage(ctx context.Context, slug, brief string) (string, error) {
	brief = strings.TrimSpace(brief)
	if brief == "" {
		return "", inputErrorf("falta la descripción del visual")
	}
	var title, body, context string
	isGuide := s.V.Exists(slug)
	if isGuide {
		n, err := s.V.Load(slug)
		if err != nil {
			return "", err
		}
		title, body, context = n.Meta.Title, n.Body, s.noteContext(slug, n)
	} else {
		c, err := s.V.LoadConcept(slug)
		if err != nil {
			return "", inputErrorf("no existe la nota «%s»", slug)
		}
		title, body, context = c.Meta.Title, c.Body, head(c.Body, 3000)
	}
	if _, found := viz.SetSrc(body, brief, ""); !found {
		return "", inputErrorf("la nota no tiene ese visual")
	}
	prompt := "Topic: " + title + "\nWhat the learner studied: " + context + "\nBuild this: " + brief
	html, err := s.buildHTML(ctx, prompt)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(brief))
	name := slug + "-" + hex.EncodeToString(sum[:4])
	if err := s.V.SaveInteractive(name, html); err != nil {
		return "", err
	}
	src := "interactive/" + name + ".html"
	set := func(b string) string { out, _ := viz.SetSrc(b, brief, src); return out }
	if isGuide {
		err = s.V.Update(slug, func(n *vault.Note) error { n.Body = set(n.Body); return nil })
	} else {
		err = s.V.UpdateConcept(slug, func(c *vault.Concept) error { c.Body = set(c.Body); return nil })
	}
	return src, err
}
