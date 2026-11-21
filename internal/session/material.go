package session

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/krilinxito/studyhomie/internal/material"
	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/roadmap"
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
// Unless o says otherwise, the roadmap stays within what the material covers.
func (s *Session) Material(ctx context.Context, paths []string, o RoadmapOpts) (string, error) {
	if len(paths) == 0 {
		return "", inputErrorf("falta el archivo")
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
			s.status("Leyendo la imagen %s", filepath.Base(abs))
			c := s.convo(prompts.Get("extract", s.aiLang()))
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
			return "", inputErrorf("%s no tiene texto utilizable", filepath.Base(abs))
		}
		if len(paths) > 1 {
			fmt.Fprintf(&text, "# %s\n\n", filepath.Base(abs))
		}
		text.WriteString(part + "\n\n")
	}

	title := strings.TrimSuffix(filepath.Base(paths[0]), filepath.Ext(paths[0]))
	mslug := vault.Slugify(title)
	if mslug == "" {
		return "", inputErrorf("no puedo derivar un nombre de %q", paths[0])
	}
	if err := s.V.SaveMaterial(mslug, text.String()); err != nil {
		return "", err
	}
	// Copy the original files into vault/material/<mslug>/ so they are visible in Obsidian and the Vault panel.
	var copied []string
	for _, p := range paths {
		if abs, err := filepath.Abs(p); err == nil {
			if rel, err := s.V.CopyMaterial(mslug, abs); err == nil {
				copied = append(copied, rel)
			}
		}
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
	if o.Reach == 0 {
		o.Reach = roadmap.ReachFocused
	}
	rslug, err := s.buildRoadmap(ctx, roadmapReq{subject: title, scope: scope + "\n", outline: material.Outline(chunks, outlineBudget),
		materialSlug: mslug, source: "material", opts: o})
	if err == nil && len(copied) > 0 {
		if rm, err2 := s.V.LoadFrom("roadmaps", rslug); err2 == nil {
			rm.Meta.Materials = copied
			rm.Body = vault.ReplaceSection(rm.Body, s.h("Material"), materialLines(copied))
			_ = s.V.SaveIn("roadmaps", rm)
		}
	}
	return rslug, err
}

// materialLines renders vault/material/<topic>/<file> entries as Obsidian wikilinks.
func materialLines(rels []string) string {
	var b strings.Builder
	for _, rel := range rels {
		ext := strings.ToLower(filepath.Ext(rel))
		switch ext {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf":
			fmt.Fprintf(&b, "- ![[%s]]\n", rel)
		default:
			fmt.Fprintf(&b, "- [[%s]]\n", rel)
		}
	}
	return b.String()
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
