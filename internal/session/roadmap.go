package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/roadmap"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// Roadmap asks about the learner's scope and traces a connected plan for a subject.
// It returns the roadmap's slug.
func (s *Session) Roadmap(ctx context.Context, subject string) (string, error) {
	s.printf("Voy a hacerte unas preguntas para ajustar el plan de «%s».\n", subject)
	resp, err := s.convo(prompts.Get("scope", s.Cfg.Language)).Say(ctx, "Subject: "+subject, nil)
	if err != nil {
		return "", err
	}
	var qs struct {
		Questions []string `json:"questions"`
	}
	if err := decodeJSON(resp.Text, &qs); err != nil {
		return "", err
	}
	var scope strings.Builder
	for i, q := range qs.Questions {
		s.printf("\n%d/%d. %s\n", i+1, len(qs.Questions), q)
		a, err := s.readLine("Respuesta", "")
		if err != nil {
			return "", err
		}
		if a == "" {
			a = "(sin respuesta)"
		}
		fmt.Fprintf(&scope, "Q: %s\nA: %s\n\n", q, a)
	}
	return s.buildRoadmap(ctx, subject, scope.String(), "", "", "roadmap")
}

// buildRoadmap generates the plan, validates it and writes the roadmap note and
// one stub note per topic, so the connections show up in the vault graph.
func (s *Session) buildRoadmap(ctx context.Context, subject, scope, outline, materialSlug, source string) (string, error) {
	prompt := "Subject: " + subject + "\n\nLearner's answers about scope:\n" + scope
	if outline != "" {
		prompt += "\nSOURCE MATERIAL OUTLINE (build the roadmap from this):\n" + outline
	}
	s.printf("\nTrazando el roadmap...\n")
	resp, err := s.convo(prompts.Get("roadmap", s.Cfg.Language)).Say(ctx, prompt, nil)
	if err != nil {
		return "", err
	}
	var plan roadmap.Plan
	if err := decodeJSON(resp.Text, &plan); err != nil {
		return "", err
	}
	plan, warnings, err := plan.Normalize()
	if err != nil {
		return "", fmt.Errorf("el roadmap generado no es válido: %w", err)
	}
	for _, w := range warnings {
		s.printf("Aviso: %s\n", w)
	}
	rslug := vault.Slugify(subject)
	if rslug == "" {
		return "", fmt.Errorf("invalid subject %q", subject)
	}

	titles := map[string]string{}
	for _, n := range plan.Nodes {
		titles[n.Slug()] = n.Title
	}
	link := func(slug string) string { return fmt.Sprintf("[[%s|%s]]", slug, titles[slug]) }
	linkAll := func(slugs []string) string {
		var l []string
		for _, sl := range slugs {
			l = append(l, link(sl))
		}
		return strings.Join(l, ", ")
	}

	var route strings.Builder
	for i, n := range plan.Nodes {
		slug := n.Slug()
		conn := ""
		if len(n.Requires) > 0 {
			conn += "- Requiere: " + linkAll(n.Requires) + "\n"
		}
		if len(n.Related) > 0 {
			conn += "- Relacionado: " + linkAll(n.Related) + "\n"
		}
		conn += "- Ruta: [[" + rslug + "]]\n"

		note := vault.Note{Slug: slug, Meta: vault.Meta{Title: n.Title, Status: "new", Source: source}}
		if s.V.Exists(slug) {
			if note, err = s.V.Load(slug); err != nil {
				return "", err
			}
		} else {
			note.Body = "> " + n.Summary + "\n"
		}
		note.Meta.Roadmap, note.Meta.Order, note.Meta.Requires = rslug, i+1, n.Requires
		note.Meta.Summary = n.Summary
		if materialSlug != "" {
			note.Meta.Material = materialSlug
		}
		note.Body = vault.ReplaceSection(note.Body, "Conexiones", conn)
		if err := s.V.Save(note); err != nil {
			return "", err
		}

		fmt.Fprintf(&route, "%d. %s — %s", i+1, link(slug), n.Summary)
		if len(n.Requires) > 0 {
			fmt.Fprintf(&route, " (requiere: %s)", linkAll(n.Requires))
		}
		route.WriteString("\n")
	}

	body := "## Alcance\n\n" + strings.TrimSpace(scope) + "\n\n## Ruta\n\n" + route.String()
	rn := vault.Note{Slug: rslug, Meta: vault.Meta{Title: plan.Subject, Source: source, Material: materialSlug}, Body: body}
	if rn.Meta.Title == "" {
		rn.Meta.Title = subject
	}
	if err := s.V.SaveIn("roadmaps", rn); err != nil {
		return "", err
	}
	s.printf("\nRoadmap «%s» (%d temas):\n\n%s\nGuardado en %s/roadmaps/%s.md\n", rn.Meta.Title, len(plan.Nodes), route.String(), s.V.Root, rslug)
	return rslug, nil
}

// Next suggests the next topic of a roadmap whose prerequisites are done and studies it.
func (s *Session) Next(ctx context.Context, rslug string) error {
	all, err := s.V.List()
	if err != nil {
		return err
	}
	var inRoadmap []vault.Note
	for _, n := range all {
		if n.Meta.Roadmap == rslug {
			inRoadmap = append(inRoadmap, n)
		}
	}
	if len(inRoadmap) == 0 {
		return fmt.Errorf("no encuentro el roadmap «%s» (usa el nombre con el que lo creaste)", rslug)
	}
	n, ok := roadmap.Next(inRoadmap)
	if !ok {
		s.printf("¡Completaste todo el roadmap «%s»!\n", rslug)
		return nil
	}
	s.printf("Siguiente tema: %s (%d/%d)\n", n.Meta.Title, n.Meta.Order, len(inRoadmap))
	if n.Meta.Summary != "" {
		s.printf("Objetivo: %s\n", n.Meta.Summary)
	}
	if ok, _ := s.confirm("¿Empezamos?"); !ok {
		return nil
	}
	if n.Meta.Status == "prepared" || n.Meta.Status == "tested" {
		return s.afterPrepare(ctx, n.Slug)
	}
	return s.Study(ctx, n.Meta.Title, "roadmap")
}
