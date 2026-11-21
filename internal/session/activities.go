package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// GenerateActivities asks the AI for 3-5 graduated practical activities for a study guide
// and stores them inside the note's "## Actividades" section as an Obsidian Markdown checklist.
func (s *Session) GenerateActivities(ctx context.Context, slug string) ([]vault.Activity, error) {
	note, title, err := s.loadGuided(slug)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "TOPIC: %s\n", title)
	if area := noteArea(note); area != "" {
		fmt.Fprintf(&b, "AREA: %s\n", area)
	}
	b.WriteString("\nGUIDE CONTENT:\n" + head(s.noteContext(slug, note), 5000) + "\n")

	resp, err := s.quickConvo(prompts.Get("activities", s.aiLang())).Say(ctx, b.String(), nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Activities []struct {
			Level  string `json:"level"`
			Kind   string `json:"kind"`
			Title  string `json:"title"`
			Prompt string `json:"prompt"`
		} `json:"activities"`
	}
	if err := decodeJSON(resp.Text, &out); err != nil {
		return nil, failf("no pude leer las actividades generadas: %w", err)
	}
	var items []vault.Activity
	for _, raw := range out.Activities {
		titleStr := strings.TrimSpace(raw.Title)
		promptStr := strings.TrimSpace(raw.Prompt)
		if titleStr == "" && promptStr == "" {
			continue
		}
		lvl := vault.NormalizeActivityLevel(raw.Level)
		items = append(items, vault.Activity{
			Index:  len(items),
			Done:   false,
			Level:  lvl,
			Kind:   strings.TrimSpace(raw.Kind),
			Title:  titleStr,
			Prompt: promptStr,
			XP:     vault.ActivityXP(lvl),
		})
	}
	if len(items) == 0 {
		return nil, failf("no se generó ninguna actividad práctica")
	}

	// Reload before saving so concurrent edits to other sections are not overwritten.
	if fresh, err := s.V.Load(slug); err == nil {
		note = fresh
	}
	note.Body = vault.ReplaceSection(note.Body, s.h("Actividades"), vault.FormatActivities(items))
	if err := s.V.Save(note); err != nil {
		return nil, err
	}
	return vault.ParseActivities(note.Body), nil
}

// ReviewActivity marks an activity done, asks the AI for brief constructive feedback on the
// learner's pasted result, and saves both under the checklist item in "## Actividades".
func (s *Session) ReviewActivity(ctx context.Context, slug string, index int, submission string) (vault.Activity, []vault.Activity, error) {
	submission = strings.TrimSpace(submission)
	if submission == "" {
		return vault.Activity{}, nil, inputErrorf("escribe tu resultado para pedir feedback")
	}
	note, title, err := s.loadGuided(slug)
	if err != nil {
		return vault.Activity{}, nil, err
	}
	items := vault.ParseActivities(note.Body)
	if index < 0 || index >= len(items) {
		return vault.Activity{}, nil, inputErrorf("actividad inválida")
	}
	target := items[index]

	var b strings.Builder
	fmt.Fprintf(&b, "TOPIC: %s\n", title)
	if area := noteArea(note); area != "" {
		fmt.Fprintf(&b, "AREA: %s\n", area)
	}
	fmt.Fprintf(&b, "\nACTIVITY (%s): %s\n%s\n", target.Level, target.Title, target.Prompt)
	fmt.Fprintf(&b, "\nLEARNER RESULT:\n%s\n", submission)
	b.WriteString("\nGUIDE CONTEXT (brief):\n" + head(s.noteContext(slug, note), 2800) + "\n")

	resp, err := s.quickConvo(prompts.Get("activity_feedback", s.aiLang())).Say(ctx, b.String(), nil)
	if err != nil {
		return vault.Activity{}, nil, err
	}
	feedback := strings.TrimSpace(resp.Text)
	if feedback == "" {
		return vault.Activity{}, nil, failf("el feedback salió vacío")
	}

	// Reload and update cleanly.
	if fresh, err := s.V.Load(slug); err == nil {
		note = fresh
		items = vault.ParseActivities(note.Body)
	}
	if index < 0 || index >= len(items) {
		return vault.Activity{}, nil, inputErrorf("actividad inválida")
	}
	items[index].Done = true
	items[index].Submission = submission
	items[index].Feedback = feedback
	note.Body = vault.ReplaceSection(note.Body, s.h("Actividades"), vault.FormatActivities(items))
	if err := s.V.Save(note); err != nil {
		return vault.Activity{}, nil, err
	}
	parsed := vault.ParseActivities(note.Body)
	return parsed[index], parsed, nil
}
