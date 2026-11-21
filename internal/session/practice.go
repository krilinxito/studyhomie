package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/krilinxito/studyhomie/internal/i18n"
	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/stats"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// Practice kinds.
const (
	PracticeQuiz    = "quiz"
	PracticeFeynman = "feynman"
)

// How a practice ended (Practice.Outcome).
const (
	OutcomeDone    = "done"    // finished and graded: it counts for mastery
	OutcomePartial = "partial" // closed early: graded on what was answered, mastery untouched
	OutcomeEmpty   = "empty"   // closed before answering anything: nothing to grade
)

// FinishNow is the line a UI sends to a practice waiting for an answer to close it right away:
// what was answered is graded and the rest counts as blank.
const FinishNow = "\x04finish-now"

var errFinishNow = errors.New("finish now")

// QuizQuestion is one question of a test, with the reference answer used to grade it.
type QuizQuestion struct {
	ID       int      `json:"id"`
	Type     string   `json:"type"`
	Question string   `json:"question"`
	Choices  []string `json:"choices"`
	Answer   string   `json:"answer"`
}

// FeynmanTurn is one round of a Feynman session: what the learner explained and the critique.
type FeynmanTurn struct {
	Learner string `json:"learner"`
	Reply   string `json:"reply"`
}

// Practice is a quiz or a Feynman session as it goes. A UI saves it at every checkpoint, so it
// can be resumed after a pause (or a restart) without generating the questions again.
type Practice struct {
	ID         string         `json:"id,omitempty"` // the UI's id, stamped on the stats event
	Kind       string         `json:"kind"`         // quiz | feynman
	Slug       string         `json:"slug"`
	N          int            `json:"n,omitempty"`
	Difficulty string         `json:"difficulty,omitempty"`
	Questions  []QuizQuestion `json:"questions,omitempty"`
	Answers    []string       `json:"answers,omitempty"` // one per answered question, in order
	Turns      []FeynmanTurn  `json:"turns,omitempty"`
	Seconds    int            `json:"seconds,omitempty"` // time spent, over every sitting

	// FinishNow closes it as soon as it starts: grade what was answered, as if the learner had
	// asked for it at the next question.
	FinishNow bool `json:"-"`
	// Set when it ends.
	Outcome string `json:"outcome,omitempty"`
	Score   int    `json:"score,omitempty"`
}

// Answered is how many questions (quiz) or explanations (Feynman) the learner gave.
func (p Practice) Answered() int {
	if p.Kind == PracticeFeynman {
		return len(p.Turns)
	}
	return len(p.Answers)
}

// Total is the number of questions of a quiz (once generated, else the number asked for); 0 for Feynman.
func (p Practice) Total() int {
	if p.Kind == PracticeFeynman {
		return 0
	}
	if len(p.Questions) > 0 {
		return len(p.Questions)
	}
	return p.N
}

// checkpoint hands the practice to the UI so it can be resumed from here.
func (s *Session) checkpoint(p *Practice, base int, started func() int) {
	p.Seconds = base + started()
	if s.Checkpoint != nil {
		s.Checkpoint(*p)
	}
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

// difficulties are the levels of a test, as the chat and the UI name them.
var difficulties = []string{i18n.N("básica"), i18n.N("media"), i18n.N("avanzada")}

// Quiz generates a test, collects answers and grades them. On a roadmap, passing it writes the
// next topic's guide.
func (s *Session) Quiz(ctx context.Context, slug string, n int, difficulty string) error {
	return s.QuizFrom(ctx, &Practice{Kind: PracticeQuiz, Slug: slug, N: n, Difficulty: difficulty})
}

// QuizFrom runs a test from where p left it: the questions it already has are not generated
// again, and it goes on at the first one without an answer. A test closed early does not unlock
// the next roadmap topic.
func (s *Session) QuizFrom(ctx context.Context, p *Practice) error {
	if err := s.quizFrom(ctx, p); err != nil || p.Outcome != OutcomeDone {
		return err
	}
	return s.advance(ctx, p.Slug)
}

func (s *Session) quiz(ctx context.Context, slug string, n int, difficulty string) error {
	return s.quizFrom(ctx, &Practice{Kind: PracticeQuiz, Slug: slug, N: n, Difficulty: difficulty})
}

func (s *Session) quizFrom(ctx context.Context, p *Practice) error {
	start, base := s.now(), p.Seconds
	elapsed := func() int { return int(s.now().Sub(start).Seconds()) }
	p.Kind = PracticeQuiz
	if p.N < 1 {
		p.N = 5
	}
	if p.Difficulty == "" {
		p.Difficulty = "media"
	}
	slug := p.Slug
	note, err := s.V.Load(slug)
	if err != nil {
		return failf("no hay guía para «%s»; ejecuta primero la preparación: %w", slug, err)
	}
	system := prompts.Get("quiz", s.aiLang())
	s.card(Card{Kind: CardPhase, Phase: "quiz"})
	if len(p.Questions) == 0 && !p.FinishNow {
		var gp strings.Builder
		fmt.Fprintf(&gp, "GENERATE a test.\nTopic: %s\n", note.Meta.Title)
		if area := noteArea(note); area != "" {
			fmt.Fprintf(&gp, "Area: %s\n", area)
		}
		fmt.Fprintf(&gp, "What the learner studied: %s\n", s.V.LoadHandoff(slug))
		if guideSec := strings.TrimSpace(vault.Section(note.Body, "Guía")); guideSec != "" {
			fmt.Fprintf(&gp, "Guide excerpt (formulas/examples): %s\n", head(guideSec, 2200))
		}
		fmt.Fprintf(&gp, "Number of questions: %d\nDifficulty: %s", p.N, p.Difficulty)
		s.status("Generando la prueba")
		resp, err := s.convo(system).Say(ctx, gp.String(), nil)
		if err != nil {
			return err
		}
		var gen struct {
			Questions []QuizQuestion `json:"questions"`
		}
		if err := decodeJSON(resp.Text, &gen); err != nil {
			return err
		}
		if len(gen.Questions) == 0 {
			return failf("la prueba salió vacía")
		}
		p.Questions = gen.Questions
		s.checkpoint(p, base, elapsed)
	}

	stopped := p.FinishNow
	for i := len(p.Answers); i < len(p.Questions) && !stopped; i++ {
		q := p.Questions[i]
		qLabel := ""
		if strings.EqualFold(q.Type, "exercise") {
			qLabel = s.tr("Ejercicio")
		}
		if !s.card(Card{Kind: CardQuestion, N: i + 1, Total: len(p.Questions), Label: qLabel, Question: q.Question, Choices: q.Choices}) {
			s.printf("\n%d/%d. %s\n", i+1, len(p.Questions), q.Question)
			for _, ch := range q.Choices {
				s.printf("   %s\n", ch)
			}
		}
		a, err := s.readLine("Respuesta", "")
		if errors.Is(err, errFinishNow) {
			stopped = true
			break
		}
		if err != nil {
			return err
		}
		p.Answers = append(p.Answers, a)
		s.checkpoint(p, base, elapsed)
	}
	answered := min(len(p.Answers), len(p.Questions))
	partial := answered < len(p.Questions)
	if partial && answered == 0 {
		p.Outcome = OutcomeEmpty
		s.printf("\nNo respondiste ninguna pregunta, así que no hay nada que corregir: la práctica se cierra sin nota.\n")
		return nil
	}

	var gb strings.Builder
	gb.WriteString("GRADE this test.\nTopic: " + note.Meta.Title + "\n")
	if partial {
		fmt.Fprintf(&gb, "The learner stopped after %d of %d questions: grade only these.\n", answered, len(p.Questions))
	}
	for i, q := range p.Questions[:answered] {
		fmt.Fprintf(&gb, "\nQuestion %d: %s\nReference answer: %s\nLearner's answer: %s\n", q.ID, q.Question, q.Answer, p.Answers[i])
	}
	s.status("Corrigiendo")
	resp, err := s.convo(system).Say(ctx, gb.String(), nil)
	if err != nil {
		return err
	}
	var g grading
	if err := decodeJSON(resp.Text, &g); err != nil {
		return err
	}
	g.Score = max(0, min(g.Score, 100))
	score := g.Score
	detail := s.trf("dificultad %s", s.tr(p.Difficulty))
	header := s.trf("Puntaje: **%d/100** (dificultad: %s)", score, s.tr(p.Difficulty)) + "\n\n"
	if partial {
		// the questions left count as blank
		score = g.Score * answered / len(p.Questions)
		detail = s.trf("incompleta: %d de %d", answered, len(p.Questions))
		header = s.trf("Puntaje: **%d/100** (incompleta: %d de %d; dificultad: %s)", score, answered, len(p.Questions), s.tr(p.Difficulty)) + "\n\n"
	}
	var rep strings.Builder
	answers := map[int]string{}
	byID := map[int]QuizQuestion{}
	for i, q := range p.Questions[:answered] {
		byID[q.ID] = q
		answers[q.ID] = p.Answers[i]
	}
	for _, r := range g.Results {
		mark := "✗"
		if r.Correct {
			mark = "✓"
		}
		fmt.Fprintf(&rep, "- %s **%s**\n  - %s %s\n  - %s\n", mark, byID[r.ID].Question, s.tr("Tu respuesta:"), answers[r.ID], r.Feedback)
	}
	if partial {
		for _, q := range p.Questions[answered:] {
			fmt.Fprintf(&rep, "- – **%s**\n  - %s\n", q.Question, s.tr("Sin responder."))
		}
	}
	if len(g.Gaps) > 0 {
		rep.WriteString("\n" + s.tr("Para repasar:") + " " + strings.Join(g.Gaps, "; ") + "\n")
	}
	if s.card(Card{Kind: CardScore, Label: s.tr("Preguntas"), Score: score, Detail: detail}) {
		s.printf("\n%s", rep.String()) // the card already shows the score
	} else {
		s.printf("\n%s%s", header, rep.String())
	}
	if partial {
		s.printf("\n%s\n", s.tr("Quedó registrada como incompleta: no cambia lo que llevas del tema."))
	}

	var title, area string
	err = s.V.Update(slug, func(n *vault.Note) error {
		n.Body = vault.ReplaceSection(n.Body, s.h("Prueba"), header+rep.String())
		if !partial {
			n.Meta.Status = "tested"
			n.Meta.Mastery = score
		}
		title, area = n.Meta.Title, noteArea(*n)
		return nil
	})
	if err != nil {
		return err
	}
	p.Seconds = base + elapsed()
	p.Score = score
	correct := 0
	for _, r := range g.Results {
		if r.Correct {
			correct++
		}
	}
	ev := stats.Event{Kind: stats.KindQuiz, Topic: slug, Title: title, Area: area, Score: score, Seconds: p.Seconds, Practice: p.ID,
		Difficulty: p.Difficulty, Correct: correct, Questions: len(p.Questions)}
	p.Outcome = OutcomeDone
	if partial {
		p.Outcome = OutcomePartial
		ev.Partial = true
		ev.XP = max(1, stats.XPFor(stats.KindQuiz, score)*answered/len(p.Questions))
	}
	s.recordEvent(ev)
	if len(g.Gaps) > 0 {
		h := s.V.LoadHandoff(slug) + "\n\nGaps found in the test: " + strings.Join(g.Gaps, "; ")
		return s.V.SaveHandoff(slug, h)
	}
	return nil
}

// Feynman lets the learner explain the topic and critiques each round. On a roadmap, passing it
// writes the next topic's guide.
func (s *Session) Feynman(ctx context.Context, slug string) error {
	return s.FeynmanFrom(ctx, &Practice{Kind: PracticeFeynman, Slug: slug})
}

// FeynmanFrom runs a Feynman session from where p left it: the model gets the rounds so far.
func (s *Session) FeynmanFrom(ctx context.Context, p *Practice) error {
	if err := s.feynmanFrom(ctx, p); err != nil || p.Outcome != OutcomeDone {
		return err
	}
	return s.advance(ctx, p.Slug)
}

func (s *Session) feynman(ctx context.Context, slug string) error {
	return s.feynmanFrom(ctx, &Practice{Kind: PracticeFeynman, Slug: slug})
}

// transcript is the Feynman conversation as the note keeps it.
func (s *Session) feynmanTranscript(turns []FeynmanTurn) string {
	var b strings.Builder
	for _, t := range turns {
		fmt.Fprintf(&b, "**%s** %s\n\n**StudyHomie:** %s\n\n", s.tr("Tú:"), t.Learner, t.Reply)
	}
	return b.String()
}

func (s *Session) feynmanFrom(ctx context.Context, p *Practice) error {
	start, base := s.now(), p.Seconds
	elapsed := func() int { return int(s.now().Sub(start).Seconds()) }
	p.Kind = PracticeFeynman
	slug := p.Slug
	note, err := s.V.Load(slug)
	if err != nil {
		return failf("no hay guía para «%s»; ejecuta primero la preparación: %w", slug, err)
	}
	c := s.convo(prompts.Get("feynman", s.aiLang()))
	header := fmt.Sprintf("Topic: %s\nStudy summary (ground truth): %s\n\n", note.Meta.Title, s.V.LoadHandoff(slug))
	// the first call of this sitting carries the context; a resumed session also carries its rounds
	first := header
	if len(p.Turns) > 0 {
		var b strings.Builder
		b.WriteString(header + "CONVERSATION SO FAR (the session was paused and resumed):\n")
		for _, t := range p.Turns {
			fmt.Fprintf(&b, "Learner: %s\nYou: %s\n\n", t.Learner, t.Reply)
		}
		first = b.String()
	}

	s.card(Card{Kind: CardPhase, Phase: "feynman"})
	if len(p.Turns) == 0 && !p.FinishNow {
		s.printf("\nExplícame «%s» como si yo no supiera nada.\n", note.Meta.Title)
		if s.rich() {
			s.printf("Escríbela con tus palabras; te iré haciendo preguntas. Cuando quieras, pulsa «Terminar y evaluar».\n\n")
		} else {
			s.printf("Escribe tu explicación; una línea con solo «.» la envía, «/fin» termina la sesión.\n\n")
		}
	}

	stopped := p.FinishNow
	for !stopped {
		text, finished, err := s.readBlock()
		if errors.Is(err, errFinishNow) {
			stopped = true
			break
		}
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
		if first != "" {
			prompt = first + "Learner's explanation:\n" + prompt
			first = ""
		}
		resp, err := c.Say(ctx, prompt, nil)
		if err != nil {
			return err
		}
		if !finished {
			s.printf("\n%s\n\n", resp.Text)
			p.Turns = append(p.Turns, FeynmanTurn{Learner: text, Reply: resp.Text})
			s.checkpoint(p, base, elapsed)
			continue
		}
		score, body, ok := extractScore(resp.Text)
		s.printf("\n%s\n", body)
		transcript := s.feynmanTranscript(p.Turns)
		if text != "" {
			transcript += fmt.Sprintf("**%s** %s\n\n", s.tr("Tú:"), text)
		}
		transcript += fmt.Sprintf("**%s** %s\n", s.tr("Evaluación final:"), body)
		if ok && !s.card(Card{Kind: CardScore, Label: s.tr("Explicación"), Score: score}) {
			s.printf("\nPuntaje Feynman: %d/100\n", score)
		}
		p.Seconds = base + elapsed()
		p.Outcome, p.Score = OutcomeDone, score
		return s.saveFeynman(p, transcript, ok, false)
	}

	// closed early: evaluate what was explained, without touching mastery
	if len(p.Turns) == 0 {
		p.Outcome = OutcomeEmpty
		s.printf("\nNo llegaste a explicar nada, así que no hay nada que evaluar: la práctica se cierra sin nota.\n")
		return nil
	}
	prompt := "FINAL EVALUATION requested: the learner stopped early and left the session incomplete. Evaluate only what they explained so far."
	if first != "" {
		prompt = first + prompt
	}
	s.status("Evaluando lo que explicaste")
	resp, err := c.Say(ctx, prompt, nil)
	if err != nil {
		return err
	}
	score, body, ok := extractScore(resp.Text)
	s.printf("\n%s\n", body)
	if ok && !s.card(Card{Kind: CardScore, Label: s.tr("Explicación"), Score: score, Detail: s.tr("incompleta")}) {
		s.printf("\nPuntaje Feynman (incompleta): %d/100\n", score)
	}
	s.printf("\n%s\n", s.tr("Quedó registrada como incompleta: no cambia lo que llevas del tema."))
	transcript := s.feynmanTranscript(p.Turns) + fmt.Sprintf("**%s** %s\n", s.tr("Evaluación (incompleta):"), body)
	p.Seconds = base + elapsed()
	p.Outcome, p.Score = OutcomePartial, score
	return s.saveFeynman(p, transcript, ok, true)
}

func (s *Session) saveFeynman(p *Practice, transcript string, scored, partial bool) error {
	slug, score := p.Slug, p.Score
	var title, area string
	err := s.V.Update(slug, func(note *vault.Note) error {
		note.Body = vault.ReplaceSection(note.Body, s.h("Feynman"), transcript)
		title, area = note.Meta.Title, noteArea(*note)
		if partial {
			return nil
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
		return nil
	})
	if err != nil {
		return err
	}
	ev := stats.Event{Kind: stats.KindFeynman, Topic: slug, Title: title, Area: area, Score: score, Seconds: p.Seconds, Practice: p.ID}
	if partial {
		ev.Partial = true
		ev.XP = max(1, stats.XPFor(stats.KindFeynman, score)/2)
	}
	s.recordEvent(ev)
	return nil
}
