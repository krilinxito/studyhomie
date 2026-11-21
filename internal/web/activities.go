package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/krilinxito/studyhomie/internal/i18n"
	"github.com/krilinxito/studyhomie/internal/stats"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// generateActivities asks the AI for 3-5 graduated practical activities for a guide
// and saves them as a Markdown checklist in the guide's "## Actividades" section.
func (s *Server) generateActivities(w http.ResponseWriter, r *http.Request) {
	slug := vault.Slugify(r.PathValue("slug"))
	if slug == "" {
		fail(w, 400, "nota inválida")
		return
	}
	drain(r)
	ctx, cancel := context.WithTimeout(r.Context(), answerTimeout)
	defer cancel()
	items, err := s.shortSession().GenerateActivities(ctx, slug)
	if err != nil {
		fail(w, shortStatus(err), s.errText(err))
		return
	}
	writeJSON(w, 200, map[string]any{"activities": items})
}

// updateActivity toggles one practical activity's completion, optionally saves a learner
// submission and requests brief AI feedback, and awards XP once per completed activity.
func (s *Server) updateActivity(w http.ResponseWriter, r *http.Request) {
	slug := vault.Slugify(r.PathValue("slug"))
	if slug == "" {
		fail(w, 400, "nota inválida")
		return
	}
	idx, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || idx < 0 {
		fail(w, 400, "actividad inválida")
		return
	}
	var body struct {
		Done         *bool  `json:"done"`
		Submission   string `json:"submission"`
		WantFeedback bool   `json:"wantFeedback"`
	}
	if !readJSON(w, r, &body) {
		return
	}

	v := s.vault()
	note, err := v.Load(slug)
	if err != nil {
		fail(w, 404, "no existe esa nota")
		return
	}
	items := vault.ParseActivities(note.Body)
	if idx >= len(items) {
		fail(w, 404, "actividad inválida")
		return
	}
	wasDone := items[idx].Done

	var updated vault.Activity
	var all []vault.Activity
	if body.WantFeedback && strings.TrimSpace(body.Submission) != "" {
		ctx, cancel := context.WithTimeout(r.Context(), answerTimeout)
		defer cancel()
		updated, all, err = s.shortSession().ReviewActivity(ctx, slug, idx, body.Submission)
		if err != nil {
			fail(w, shortStatus(err), s.errText(err))
			return
		}
	} else {
		if body.Done != nil {
			items[idx].Done = *body.Done
		} else {
			items[idx].Done = !items[idx].Done
		}
		if sub := strings.TrimSpace(body.Submission); sub != "" {
			items[idx].Submission = sub
		}
		note.Body = vault.ReplaceSection(note.Body, i18n.Heading(s.lang(), "Actividades"), vault.FormatActivities(items))
		if err := v.Save(note); err != nil {
			fail(w, 500, err.Error())
			return
		}
		all = vault.ParseActivities(note.Body)
		updated = all[idx]
	}

	xpGained := 0
	if !wasDone && updated.Done {
		xpGained = s.recordActivityXP(note, updated)
	}
	writeJSON(w, 200, map[string]any{
		"activity":   updated,
		"activities": all,
		"xpGained":   xpGained,
	})
}

// recordActivityXP awards XP for a completed activity once per (topic, title) so toggling a
// checkbox off and on does not farm duplicate XP.
func (s *Server) recordActivityXP(note vault.Note, act vault.Activity) int {
	title := strings.TrimSpace(act.Title)
	if title == "" {
		title = note.Meta.Title
	}
	eventTitle := note.Meta.Title
	if title != "" && title != note.Meta.Title {
		eventTitle = note.Meta.Title + " · " + title
	}
	data, _ := stats.Load(s.vault().Root)
	for _, ev := range data.Events {
		if ev.Kind == stats.KindActivity && ev.Topic == note.Slug && ev.Title == eventTitle {
			return 0
		}
	}
	area := note.Meta.Area
	if area == "" && note.Meta.Field != "" {
		area = vault.NormalizeArea(note.Meta.Field)
	}
	xp := act.XP
	if xp <= 0 {
		xp = vault.ActivityXP(act.Level)
	}
	_, _ = stats.Record(s.vault().Root, stats.Event{
		At:      s.Now(),
		Kind:    stats.KindActivity,
		Topic:   note.Slug,
		Title:   eventTitle,
		Area:    area,
		XP:      xp,
		Seconds: stats.DefaultSeconds(stats.KindActivity),
	})
	return xp
}
