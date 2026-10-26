package session

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/links"
	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// Session wires a provider, a vault and a terminal to the three phases.
type Session struct {
	P   provider.Provider
	V   *vault.Vault
	Cfg config.Config
	In  *bufio.Reader
	Out io.Writer

	// CheckLinks verifies cited URLs; tests replace it.
	CheckLinks func(ctx context.Context, urls []string) map[string]bool
	// Now and Pick make the Daily deterministic in tests. Nil means time.Now and a random index.
	Now  func() time.Time
	Pick func(n int) int
	// Ask, if set, is called right before the session waits for input, so a UI can
	// show the right control: kind is "line" (one answer) or "block" (free text).
	Ask func(kind, label, def string)
}

func (s *Session) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Session) pick(n int) int {
	if s.Pick != nil {
		return s.Pick(n)
	}
	return rand.IntN(n)
}

func (s *Session) printf(format string, a ...any) { fmt.Fprintf(s.Out, format, a...) }

func (s *Session) convo(system string, tools ...string) *Convo {
	return &Convo{
		P: s.P, System: system, Tools: tools, Model: s.Cfg.Model, Language: s.Cfg.Language,
		Window: s.Cfg.ContextWindow, CompactAt: s.Cfg.CompactAt,
	}
}

// readLine prompts and returns a trimmed line; def is used for an empty answer.
func (s *Session) readLine(label, def string) (string, error) {
	if def != "" {
		s.printf("%s [%s]: ", label, def)
	} else {
		s.printf("%s: ", label)
	}
	if s.Ask != nil {
		s.Ask("line", label, def) // after the question is printed, so a UI shows it first
	}
	line, err := s.In.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

// readBlock reads lines until one containing only "." (send) or "/fin" (finish).
func (s *Session) readBlock() (text string, finished bool, err error) {
	if s.Ask != nil {
		s.Ask("block", "", "")
	}
	var b strings.Builder
	for {
		line, rerr := s.In.ReadString('\n')
		t := strings.TrimSpace(line)
		switch {
		case t == ".":
			return strings.TrimSpace(b.String()), false, nil
		case t == "/fin":
			return strings.TrimSpace(b.String()), true, nil
		}
		b.WriteString(line)
		if rerr != nil {
			return strings.TrimSpace(b.String()), true, nil
		}
	}
}

// Prepare writes the study guide, verifies its external links and saves everything.
func (s *Session) Prepare(ctx context.Context, topic, source string) (string, error) {
	slug := vault.Slugify(topic)
	if slug == "" {
		return "", fmt.Errorf("invalid topic %q", topic)
	}
	var note vault.Note
	if s.V.Exists(slug) {
		var err error
		if note, err = s.V.Load(slug); err != nil {
			return "", err
		}
	} else {
		note = vault.Note{Slug: slug, Meta: vault.Meta{Title: topic, Source: source}}
	}

	prompt := "Topic: " + topic
	if note.Meta.Summary != "" {
		prompt += "\nBy the end the learner should be able to: " + note.Meta.Summary
	}
	if g := s.grounding(note); g != "" {
		prompt += "\n\nSOURCE MATERIAL (excerpts from the learner's document):\n" + g
	}

	s.printf("Preparando la guía de «%s» (busca material en línea, puede tardar)...\n", topic)
	c := s.convo(prompts.Get("prepare", s.Cfg.Language), "WebSearch", "WebFetch")
	resp, err := c.Say(ctx, prompt, nil)
	if err != nil {
		return "", err
	}
	guide, payload := splitJSON(resp.Text)

	var out struct {
		Resources []vault.Resource `json:"resources"`
		Handoff   string           `json:"handoff"`
	}
	if payload != "" {
		if err := decodeJSON(payload, &out); err != nil {
			s.printf("Aviso: no pude leer la lista de recursos (%v); guardo la guía sin ella.\n", err)
		}
	} else {
		s.printf("Aviso: la respuesta no incluyó recursos estructurados; guardo solo la guía.\n")
	}

	resources := s.verified(ctx, out.Resources)
	// keep simulations generated earlier, and do not propose building another one
	if have := localInteractive(note.Meta.Resources); len(have) > 0 {
		var rest []vault.Resource
		for _, r := range resources {
			if r.Type != "interactive" || r.URL != "" {
				rest = append(rest, r)
			}
		}
		resources = append(rest, have...)
	}

	if note.Meta.Status == "" || note.Meta.Status == "new" {
		note.Meta.Status = "prepared" // re-preparing a guide must not undo later progress
	}
	note.Meta.Resources = resources
	note.Body = guideBody(note.Body, guide, resources)
	if err := s.V.Save(note); err != nil {
		return "", err
	}
	handoff := out.Handoff
	if handoff == "" {
		handoff = guide
	}
	if err := s.V.SaveHandoff(slug, handoff); err != nil {
		return "", err
	}
	s.printf("\n%s\n\nGuardado en %s/topics/%s.md\n", guide, s.V.Root, slug)
	return slug, nil
}

// verified drops resources whose URL is dead and keeps URL-less ones (books).
func (s *Session) verified(ctx context.Context, in []vault.Resource) []vault.Resource {
	check := s.CheckLinks
	if check == nil {
		check = links.CheckAll
	}
	var urls []string
	for _, r := range in {
		if r.URL != "" {
			urls = append(urls, r.URL)
		}
	}
	alive := check(ctx, urls)
	var out []vault.Resource
	for _, r := range in {
		if r.URL != "" && !alive[r.URL] {
			s.printf("Descarté un enlace que no responde: %s\n", r.URL)
			continue
		}
		out = append(out, r)
	}
	return out
}

func guideBody(existing, guide string, res []vault.Resource) string {
	body := vault.ReplaceSection(existing, "Guía", guide)
	if lines := resourceLines(res); lines != "" {
		body = vault.ReplaceSection(body, "Recursos", lines)
	}
	return body
}

// resourceLines renders resources as a Markdown list. Vault-relative links
// (generated simulations) are written relative to the topics folder.
func resourceLines(res []vault.Resource) string {
	var b strings.Builder
	for _, r := range res {
		line := fmt.Sprintf("- **%s** (%s)", r.Title, r.Type)
		if r.URL != "" {
			u := r.URL
			if !strings.HasPrefix(u, "http") {
				u = "../" + u
			}
			line = fmt.Sprintf("- [%s](%s) (%s)", r.Title, u, r.Type)
		}
		if r.Why != "" {
			line += " — " + r.Why
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// localInteractive returns the simulations already generated for a note.
func localInteractive(res []vault.Resource) []vault.Resource {
	var out []vault.Resource
	for _, r := range res {
		if r.Type == "interactive" && strings.HasPrefix(r.URL, "interactive/") {
			out = append(out, r)
		}
	}
	return out
}

type question struct {
	ID       int      `json:"id"`
	Type     string   `json:"type"`
	Question string   `json:"question"`
	Choices  []string `json:"choices"`
	Answer   string   `json:"answer"`
}

type grading struct {
	Score   int `json:"score"`
	Results []struct {
		ID       int    `json:"id"`
		Correct  bool   `json:"correct"`
		Feedback string `json:"feedback"`
	} `json:"results"`
	Gaps []string `json:"gaps"`
}

// Quiz generates a test, collects answers and grades them.
func (s *Session) Quiz(ctx context.Context, slug string, n int, difficulty string) error {
	note, err := s.V.Load(slug)
	if err != nil {
		return fmt.Errorf("no hay guía para «%s»; ejecuta primero la preparación: %w", slug, err)
	}
	system := prompts.Get("quiz", s.Cfg.Language)
	genPrompt := fmt.Sprintf("GENERATE a test.\nTopic: %s\nWhat the learner studied: %s\nNumber of questions: %d\nDifficulty: %s",
		note.Meta.Title, s.V.LoadHandoff(slug), n, difficulty)
	s.printf("Generando la prueba...\n")
	resp, err := s.convo(system).Say(ctx, genPrompt, nil)
	if err != nil {
		return err
	}
	var gen struct {
		Questions []question `json:"questions"`
	}
	if err := decodeJSON(resp.Text, &gen); err != nil {
		return err
	}
	if len(gen.Questions) == 0 {
		return fmt.Errorf("la prueba salió vacía")
	}

	answers := make(map[int]string)
	for i, q := range gen.Questions {
		s.printf("\n%d/%d. %s\n", i+1, len(gen.Questions), q.Question)
		for _, ch := range q.Choices {
			s.printf("   %s\n", ch)
		}
		a, err := s.readLine("Respuesta", "")
		if err != nil {
			return err
		}
		answers[q.ID] = a
	}

	var gb strings.Builder
	gb.WriteString("GRADE this test.\nTopic: " + note.Meta.Title + "\n")
	for _, q := range gen.Questions {
		fmt.Fprintf(&gb, "\nQuestion %d: %s\nReference answer: %s\nLearner's answer: %s\n", q.ID, q.Question, q.Answer, answers[q.ID])
	}
	s.printf("\nCorrigiendo...\n")
	resp, err = s.convo(system).Say(ctx, gb.String(), nil)
	if err != nil {
		return err
	}
	var g grading
	if err := decodeJSON(resp.Text, &g); err != nil {
		return err
	}

	var rep strings.Builder
	fmt.Fprintf(&rep, "Puntaje: **%d/100** (dificultad: %s)\n\n", g.Score, difficulty)
	byID := map[int]question{}
	for _, q := range gen.Questions {
		byID[q.ID] = q
	}
	for _, r := range g.Results {
		mark := "✗"
		if r.Correct {
			mark = "✓"
		}
		fmt.Fprintf(&rep, "- %s **%s**\n  - Tu respuesta: %s\n  - %s\n", mark, byID[r.ID].Question, answers[r.ID], r.Feedback)
	}
	if len(g.Gaps) > 0 {
		rep.WriteString("\nPara repasar: " + strings.Join(g.Gaps, "; ") + "\n")
	}
	s.printf("\n%s", rep.String())

	if err := s.V.AppendSection(slug, "Prueba", rep.String()); err != nil {
		return err
	}
	note, _ = s.V.Load(slug)
	note.Meta.Status = "tested"
	note.Meta.Mastery = g.Score
	if err := s.V.Save(note); err != nil {
		return err
	}
	if len(g.Gaps) > 0 {
		h := s.V.LoadHandoff(slug) + "\n\nGaps found in the test: " + strings.Join(g.Gaps, "; ")
		return s.V.SaveHandoff(slug, h)
	}
	return nil
}

// Feynman lets the learner explain the topic and critiques each round.
func (s *Session) Feynman(ctx context.Context, slug string) error {
	note, err := s.V.Load(slug)
	if err != nil {
		return fmt.Errorf("no hay guía para «%s»; ejecuta primero la preparación: %w", slug, err)
	}
	c := s.convo(prompts.Get("feynman", s.Cfg.Language))
	header := fmt.Sprintf("Topic: %s\nStudy summary (ground truth): %s\n\n", note.Meta.Title, s.V.LoadHandoff(slug))

	s.printf("\nExplícame «%s» como si yo no supiera nada.\n", note.Meta.Title)
	s.printf("Escribe tu explicación; una línea con solo «.» la envía, «/fin» termina la sesión.\n\n")

	var transcript strings.Builder
	first := true
	for {
		text, finished, err := s.readBlock()
		if err != nil {
			return err
		}
		if text == "" && !finished {
			continue
		}
		prompt := text
		if finished {
			// no leading "/": the Claude CLI would take it for a slash command
			prompt = "FINAL EVALUATION requested: the learner ended the session. Evaluate the whole conversation now.\n" + text
		}
		if first {
			prompt = header + "Learner's explanation:\n" + prompt
			first = false
		}
		resp, err := c.Say(ctx, prompt, nil)
		if err != nil {
			return err
		}
		if !finished {
			s.printf("\n%s\n\n", resp.Text)
			fmt.Fprintf(&transcript, "**Tú:** %s\n\n**StudyHomie:** %s\n\n", text, resp.Text)
			continue
		}
		score, body, ok := extractScore(resp.Text)
		s.printf("\n%s\n", body)
		fmt.Fprintf(&transcript, "**Evaluación final:** %s\n", body)
		if ok {
			s.printf("\nPuntaje Feynman: %d/100\n", score)
		}
		return s.saveFeynman(slug, transcript.String(), score, ok)
	}
}

func (s *Session) saveFeynman(slug, transcript string, score int, scored bool) error {
	if err := s.V.AppendSection(slug, "Feynman", transcript); err != nil {
		return err
	}
	note, err := s.V.Load(slug)
	if err != nil {
		return err
	}
	wasTested := note.Meta.Status == "tested"
	note.Meta.Status = "explained"
	if scored {
		// combine with the quiz score only right after a quiz; later retries replace it
		if wasTested {
			note.Meta.Mastery = (note.Meta.Mastery + score) / 2
		} else {
			note.Meta.Mastery = score
		}
	}
	return s.V.Save(note)
}

// Study runs the three phases in order for one topic.
func (s *Session) Study(ctx context.Context, topic, source string) error {
	slug, err := s.Prepare(ctx, topic, source)
	if err != nil {
		return err
	}
	if note, err := s.V.Load(slug); err == nil {
		if why, ok := pendingInteractive(note); ok {
			s.printf("\nLa guía propone una simulación interactiva: %s\n", why)
			if yes, _ := s.confirm("¿La genero?"); yes {
				// a failed simulation must not block studying
				if err := s.Interactive(ctx, slug, why); err != nil {
					s.printf("No pude generar la simulación: %v\n", err)
				}
			}
		}
	}
	return s.afterPrepare(ctx, slug)
}

// afterPrepare continues with the test and the Feynman phase of an already prepared topic.
func (s *Session) afterPrepare(ctx context.Context, slug string) error {
	if ok, _ := s.confirm("\n¿Pasamos a la prueba?"); !ok {
		return nil
	}
	nStr, err := s.readLine("¿Cuántas preguntas?", "5")
	if err != nil {
		return err
	}
	n, convErr := strconv.Atoi(nStr)
	if convErr != nil || n < 1 {
		n = 5
	}
	diff, err := s.readLine("Dificultad (básica/media/avanzada)", "media")
	if err != nil {
		return err
	}
	if err := s.Quiz(ctx, slug, n, diff); err != nil {
		return err
	}
	if ok, _ := s.confirm("\n¿Pasamos a la fase Feynman?"); !ok {
		return nil
	}
	return s.Feynman(ctx, slug)
}

func (s *Session) confirm(label string) (bool, error) {
	a, err := s.readLine(label+" (s/n)", "s")
	if err != nil {
		return false, err
	}
	a = strings.ToLower(a)
	return a == "s" || a == "si" || a == "sí" || a == "y", nil
}
