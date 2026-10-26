package session

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/krilinxito/studyhomie/internal/material"
	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/vault"
)

const (
	chunkSize     = 3000  // characters per chunk
	outlineBudget = 24000 // characters of outline sent when planning
	groundChunks  = 6     // chunks of source material sent when preparing a topic
)

// Material ingests the learner's documents and builds a roadmap from them. Each
// topic is later prepared from the chunks of the document that match it.
// It returns the roadmap's slug.
func (s *Session) Material(ctx context.Context, paths []string) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("falta el archivo")
	}
	var text strings.Builder
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		kind, err := material.Classify(abs)
		if err != nil {
			return "", err
		}
		var part string
		if kind == material.Image {
			s.printf("Leyendo la imagen %s...\n", filepath.Base(abs))
			c := s.convo(prompts.Get("extract", s.Cfg.Language))
			c.Files = []string{abs}
			resp, err := c.Say(ctx, "Transcribe the attached image.", nil)
			if err != nil {
				return "", err
			}
			part = resp.Text
		} else if part, err = material.Extract(abs); err != nil {
			return "", fmt.Errorf("%s: %w", filepath.Base(abs), err)
		}
		if strings.TrimSpace(part) == "" {
			return "", fmt.Errorf("%s no tiene texto utilizable", filepath.Base(abs))
		}
		if len(paths) > 1 {
			fmt.Fprintf(&text, "# %s\n\n", filepath.Base(abs))
		}
		text.WriteString(part + "\n\n")
	}

	title := strings.TrimSuffix(filepath.Base(paths[0]), filepath.Ext(paths[0]))
	mslug := vault.Slugify(title)
	if mslug == "" {
		return "", fmt.Errorf("no puedo derivar un nombre de %q", paths[0])
	}
	if err := s.V.SaveMaterial(mslug, text.String()); err != nil {
		return "", err
	}
	chunks := material.Split(text.String(), chunkSize)
	s.printf("Material «%s»: %d fragmentos.\n\n", title, len(chunks))

	focus, err := s.readLine("¿Algún foco o necesidad? (Enter = todo el material)", "")
	if err != nil {
		return "", err
	}
	scope := "Cover the whole material."
	if focus != "" {
		scope = "Focus: " + focus
	}
	return s.buildRoadmap(ctx, title, scope+"\n", material.Outline(chunks, outlineBudget), mslug, "material")
}

// grounding returns the parts of the topic's source material that best match it,
// so long documents never go into a prompt whole.
func (s *Session) grounding(n vault.Note) string {
	if n.Meta.Material == "" {
		return ""
	}
	text := s.V.LoadMaterial(n.Meta.Material)
	if text == "" {
		return ""
	}
	top := material.Top(material.Split(text, chunkSize), n.Meta.Title+" "+n.Meta.Summary, groundChunks)
	var b strings.Builder
	for _, c := range top {
		if c.Heading != "" {
			fmt.Fprintf(&b, "[%s]\n", c.Heading)
		}
		b.WriteString(c.Text + "\n\n---\n\n")
	}
	return b.String()
}
