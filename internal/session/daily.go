package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/vault"
)

const maxRerolls = 5

// AllFields as the Daily field draws from every built-in field plus the configured
// interests, however the config was customised.
const AllFields = "*"

// allFields is the built-in fields followed by any configured interest not already in it.
func allFields(interests []string) []string {
	fields := config.DefaultInterests()
	seen := map[string]bool{}
	for _, f := range fields {
		seen[strings.ToLower(f)] = true
	}
	for _, f := range interests {
		if !seen[strings.ToLower(f)] {
			seen[strings.ToLower(f)] = true
			fields = append(fields, f)
		}
	}
	return fields
}

// Daily proposes today's challenge topic and studies it. field restricts the draw
// to one knowledge field (any text, not only the configured ones) or is AllFields;
// empty draws from the configured interests. Running it again the same day resumes
// today's topic instead of drawing a new one.
func (s *Session) Daily(ctx context.Context, field string) error {
	date := s.now().Format("2006-01-02")
	if n, err := s.V.LoadFrom("daily", date); err == nil {
		return s.resumeDaily(ctx, n)
	}

	past, err := s.V.ListIn("daily")
	if err != nil {
		return err
	}
	var seen []string
	for _, n := range past {
		seen = append(seen, n.Meta.Title)
	}
	if len(seen) > 60 {
		seen = seen[len(seen)-60:]
	}

	fields := s.Cfg.Interests
	switch field = strings.TrimSpace(field); field {
	case "":
	case AllFields:
		fields = allFields(fields)
	default:
		fields = []string{field}
	}
	if len(fields) == 0 {
		return fmt.Errorf("no hay campos de interés: agrégalos en %s o pasa uno como argumento", "studyhomie.toml")
	}

	for attempt := 0; attempt < maxRerolls; attempt++ {
		f := fields[s.pick(len(fields))]
		prompt := fmt.Sprintf("Field: %s\nDate: %s\nPrevious topics (do not repeat): %s", f, date, strings.Join(seen, "; "))
		resp, err := s.convo(prompts.Get("daily", s.Cfg.Language)).Say(ctx, prompt, nil)
		if err != nil {
			return err
		}
		var p struct{ Field, Topic, Hook string }
		if err := decodeJSON(resp.Text, &p); err != nil {
			return err
		}
		if strings.TrimSpace(p.Topic) == "" {
			return fmt.Errorf("el modelo no propuso un tema")
		}
		if p.Field == "" {
			p.Field = f
		}
		if !s.card(Card{Kind: CardDaily, Field: p.Field, Topic: p.Topic, Hook: p.Hook}) {
			s.printf("\nReto de hoy [%s]: %s\n%s\n", p.Field, p.Topic, p.Hook)
		}
		if ok, _ := s.confirm("¿Lo aceptas?"); !ok {
			seen = append(seen, p.Topic)
			continue
		}

		slug := vault.Slugify(p.Topic)
		if slug == "" {
			return fmt.Errorf("tema inválido %q", p.Topic)
		}
		if !s.V.Exists(slug) {
			stub := vault.Note{Slug: slug, Meta: vault.Meta{Title: p.Topic, Status: "new", Source: "daily", Field: p.Field}}
			if err := s.V.Save(stub); err != nil {
				return err
			}
		}
		day := vault.Note{
			Slug: date,
			Meta: vault.Meta{Title: p.Topic, Source: "daily", Field: p.Field},
			Body: fmt.Sprintf("[[%s|%s]]\n\n> %s\n", slug, p.Topic, p.Hook),
		}
		if err := s.V.SaveIn("daily", day); err != nil {
			return err
		}
		return s.Study(ctx, p.Topic, "daily")
	}
	s.printf("\nHoy no te convenció ningún tema. Prueba `studyhomie daily <campo>` o vuelve más tarde.\n")
	return nil
}

func (s *Session) resumeDaily(ctx context.Context, day vault.Note) error {
	topic := day.Meta.Title
	slug := vault.Slugify(topic)
	s.printf("Tu reto de hoy: %s\n", topic)
	n, err := s.V.Load(slug)
	if err != nil {
		return s.Study(ctx, topic, "daily")
	}
	switch n.Meta.Status {
	case "explained":
		s.printf("Ya lo completaste hoy (dominio %d/100). Vuelve mañana por un tema nuevo.\n", n.Meta.Mastery)
		return nil
	case "prepared", "tested":
		return s.afterPrepare(ctx, slug)
	}
	return s.Study(ctx, topic, "daily")
}
