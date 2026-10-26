package session

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/vault"
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
	c := s.convo(prompts.Get("interactive", s.Cfg.Language))
	s.printf("Generando la simulación interactiva (puede tardar)...\n")

	var html string
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := c.Say(ctx, prompt, nil)
		if err != nil {
			return err
		}
		html = extractHTML(resp.Text)
		if err = validateInteractive(html); err == nil {
			break
		}
		if attempt == 1 {
			return fmt.Errorf("no logré una simulación válida: %w", err)
		}
		prompt = "That page was rejected: " + err.Error() + ". Fix it and output the complete document again."
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
	s.printf("Simulación guardada en %s/%s\n", s.V.Root, rel)
	return nil
}
