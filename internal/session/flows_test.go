package session

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/vault"
)

func jsonReply(s string) provider.Response { return reply("```json\n"+s+"\n```", 10) }

func newReader(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }

func noteFor(slug, title, status string, mastery int) vault.Note {
	return vault.Note{Slug: slug, Meta: vault.Meta{Title: title, Status: status, Mastery: mastery}}
}

func TestMaterialRejectsUnsupportedAndEmpty(t *testing.T) {
	s, _ := newSession(t, &fake{}, "")
	if _, err := s.Material(context.Background(), []string{"virus.exe"}, RoadmapOpts{}); err == nil {
		t.Error("unsupported type must be rejected")
	}
	empty := filepath.Join(t.TempDir(), "vacio.txt")
	os.WriteFile(empty, []byte("  \n"), 0o644)
	if _, err := s.Material(context.Background(), []string{empty}, RoadmapOpts{}); err == nil {
		t.Error("empty document must be rejected")
	}
}

func TestDailyRerollAcceptAndResume(t *testing.T) {
	first := `{"field":"Historia","topic":"La Biblioteca de Alejandría","hook":"¿Qué se perdió realmente?"}`
	second := `{"field":"Física","topic":"Por qué el cielo es azul","hook":"No es el reflejo del mar."}`
	f := &fake{replies: []provider.Response{jsonReply(first), jsonReply(second), reply(prepReply, 10)}}
	// n = reject first proposal, s = accept second, then EOF at "¿Respondemos unas preguntas?"
	s, out := newSession(t, f, "n\ns\n")
	s.Now = func() time.Time { return time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC) }
	s.Cfg.Interests = []string{"Historia", "Física"}
	picks := []int{0, 1}
	s.Pick = func(n int) int { p := picks[0]; picks = picks[1:]; return p }

	if err := s.Daily(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.reqs[0].Prompt, "Field: Historia") || !strings.Contains(f.reqs[1].Prompt, "La Biblioteca de Alejandría") {
		t.Errorf("rerolls must avoid rejected topics: %q / %q", f.reqs[0].Prompt, f.reqs[1].Prompt)
	}
	day, err := s.V.LoadFrom("daily", "2026-10-27")
	if err != nil || day.Meta.Title != "Por qué el cielo es azul" || day.Meta.Field != "Física" {
		t.Fatalf("daily note: %+v %v", day, err)
	}
	topic, err := s.V.Load("por-qué-el-cielo-es-azul")
	if err != nil || topic.Meta.Source != "daily" || topic.Meta.Field != "Física" || topic.Meta.Status != "prepared" {
		t.Fatalf("topic note: %+v %v", topic.Meta, err)
	}

	// same day again: resumes, no new proposal and no model call
	calls := len(f.reqs)
	s.In = newReader("")
	out.Reset()
	if err := s.Daily(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != calls || !strings.Contains(out.String(), "Tu reto de hoy: Por qué el cielo es azul") {
		t.Errorf("resume must not draw a new topic (calls %d→%d): %s", calls, len(f.reqs), out.String())
	}

	// next day: a new draw is made
	s.Now = func() time.Time { return time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC) }
	f.replies = nil
	for i := 0; i < maxRerolls; i++ {
		f.replies = append(f.replies, jsonReply(first))
	}
	s.In = newReader(strings.Repeat("n\n", maxRerolls))
	picks = make([]int, maxRerolls)
	if err := s.Daily(context.Background(), "Historia"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.V.LoadFrom("daily", "2026-10-28"); err == nil {
		t.Error("rejecting every proposal must not create a daily note")
	}
}

func TestDailyAllFieldsAndCustomField(t *testing.T) {
	proposal := jsonReply(`{"field":"X","topic":"Tema","hook":"Gancho"}`)
	rejectAll := strings.Repeat("n\n", maxRerolls)
	replies := func() []provider.Response {
		var r []provider.Response
		for i := 0; i < maxRerolls; i++ {
			r = append(r, proposal)
		}
		return r
	}

	// AllFields draws from the built-in fields plus configured extras, without duplicates
	s, _ := newSession(t, &fake{replies: replies()}, rejectAll)
	s.Now = func() time.Time { return time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC) }
	s.Cfg.Interests = []string{"Historia", "Astronomía", "historia del ARTE"}
	var got []int
	s.Pick = func(n int) int { got = append(got, n); return n - 1 }
	if err := s.Daily(context.Background(), AllFields); err != nil {
		t.Fatal(err)
	}
	want := len(config.DefaultInterests()) + 1 // Astronomía is the only extra
	if len(got) == 0 || got[0] != want {
		t.Errorf("all-fields pool = %v, want %d", got, want)
	}

	// any text is accepted as a one-off field
	f := &fake{replies: replies()}
	s, _ = newSession(t, f, rejectAll)
	s.Now = func() time.Time { return time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC) }
	s.Pick = func(n int) int { return 0 }
	if err := s.Daily(context.Background(), "  Arquitectura "); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.reqs[0].Prompt, "Field: Arquitectura\n") {
		t.Errorf("custom field must reach the prompt trimmed: %q", f.reqs[0].Prompt)
	}

	if !slices.Contains(config.DefaultInterests(), "Historia del arte") {
		t.Error("Historia del arte must be a default field")
	}
}

func TestDailyCompletedTopicIsNotRepeated(t *testing.T) {
	s, out := newSession(t, &fake{}, "")
	s.Now = func() time.Time { return time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC) }
	topic := noteFor("tema-hecho", "Tema hecho", "explained", 90)
	s.V.Save(topic)
	s.V.SaveIn("daily", noteFor("2026-10-27", "Tema hecho", "", 0))
	if err := s.Daily(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Ya lo completaste hoy (dominio 90/100)") {
		t.Errorf("output: %s", out.String())
	}
}

func TestDailyWithAnotherThemeDrawsAgain(t *testing.T) {
	first := `{"field":"Historia","topic":"La Biblioteca de Alejandría","hook":"¿Qué se perdió?"}`
	second := `{"field":"Dinosaurios","topic":"Por qué las aves son dinosaurios","hook":"Un T. rex y una gallina."}`
	f := &fake{replies: []provider.Response{jsonReply(first), reply(prepReply, 10), jsonReply(second), reply(prepReply, 10)}}
	s, _ := newSession(t, f, "s\n")
	s.Now = func() time.Time { return time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC) }
	s.Cfg.Interests = []string{"Historia"}
	s.Pick = func(int) int { return 0 }
	if err := s.Daily(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	// the same theme (or none) resumes; another theme draws today's challenge again
	calls := len(f.reqs)
	s.In = newReader("")
	if err := s.Daily(context.Background(), "historia"); err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != calls {
		t.Errorf("the same theme must resume, not draw again")
	}
	s.In = newReader("s\n")
	if err := s.Daily(context.Background(), "Dinosaurios"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.reqs[calls].Prompt, "Field: Dinosaurios") {
		t.Errorf("new draw prompt: %q", f.reqs[calls].Prompt)
	}
	day, _ := s.V.LoadFrom("daily", "2026-10-27")
	if day.Meta.Title != "Por qué las aves son dinosaurios" {
		t.Errorf("today's challenge = %q", day.Meta.Title)
	}
}
