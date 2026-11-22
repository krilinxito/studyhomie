package session

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/links"
	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/sources"
	"github.com/krilinxito/studyhomie/internal/stats"
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
	// show the right control: kind is "line" (one answer) or "block" (free text). label is in
	// Spanish, the language the program is written in: a UI translates it with Label, and can
	// still tell which question it is by the Spanish text.
	Ask func(kind, label, def string)
	// Card, if set, receives structured output a UI draws as rich elements. Where a
	// card replaces text, the text is not printed; the terminal leaves this nil.
	Card func(Card)
	// Sources finds and checks material in public catalogues; nil leaves what the model proposed
	// as it is and turns «Buscar más material» off.
	Sources *sources.Client
	// Checkpoint, if set, receives a quiz or Feynman session after every step (questions
	// generated, an answer given), so a UI can save it and resume it later.
	Checkpoint func(Practice)
	// StartPractice, if set, runs the quiz and Feynman the chat asks for on their own, next to
	// the chat, instead of inside it. It returns the practice id; title is the guide's.
	StartPractice func(p Practice) (id string, err error)
}

func (s *Session) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// recordEvent appends a study event to the vault's stats log. Errors are ignored so stats
// never interrupt studying.
func (s *Session) recordEvent(ev stats.Event) {
	if s.V == nil {
		return
	}
	if ev.At.IsZero() {
		ev.At = s.now()
	}
	_, _ = stats.Record(s.V.Root, ev)
}

func (s *Session) pick(n int) int {
	if s.Pick != nil {
		return s.Pick(n)
	}
	return rand.IntN(n)
}

// printf writes to the learner; a session without output (a short web request) stays quiet.
func (s *Session) printf(format string, a ...any) {
	if s.Out != nil {
		fmt.Fprintf(s.Out, s.tr(format), a...)
	}
}

// rich reports whether a UI draws this session (the web app); the terminal leaves Card nil.
func (s *Session) rich() bool { return s.Card != nil }

// termf writes text meant only for the terminal: typing conventions, file paths and the
// technical notices a learner in the web app has no use for.
func (s *Session) termf(format string, a ...any) {
	if !s.rich() {
		s.printf(format, a...)
	}
}

// status says what the session is busy with. The terminal prints it as a line ending in "...";
// a UI shows it in its busy indicator, so the chat transcript is not filled with progress lines.
func (s *Session) status(format string, a ...any) {
	text := s.trf(format, a...)
	if !s.card(Card{Kind: CardStatus, Detail: text + "…"}) {
		s.printf("%s...\n", text)
	}
}

func (s *Session) convo(system string, tools ...string) *Convo {
	return &Convo{
		P: s.P, System: system, Tools: tools, Model: s.Cfg.Model, Language: s.aiLang(),
		Window: s.Cfg.ContextWindow, CompactAt: s.Cfg.CompactAt,
		Timeout: callTimeout(s.Cfg.Timeout, tools),
	}
}

// callTimeout is how long one AI call may take: the configured minutes, three times that for a
// call that searches the web (a whole guide with its sources), none when the limit is 0.
func callTimeout(minutes int, tools []string) time.Duration {
	if minutes <= 0 {
		return 0
	}
	d := time.Duration(minutes) * time.Minute
	if slices.Contains(tools, "WebSearch") || slices.Contains(tools, "WebFetch") {
		d *= 3
	}
	return d
}

// readLine prompts and returns a trimmed line; def is used for an empty answer.
func (s *Session) readLine(label, def string) (string, error) {
	if s.Ask != nil {
		s.Ask("line", label, def) // a UI shows the label next to its input, so it is not printed
	} else if def != "" {
		s.printf("%s [%s]: ", Label(s.lang(), label), def)
	} else {
		s.printf("%s: ", Label(s.lang(), label))
	}
	line, err := s.In.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == FinishNow {
		return "", errFinishNow
	}
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
		case t == FinishNow:
			return "", false, errFinishNow
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

// Prepare writes the study guide of a topic, with concept notes down to the configured depth.
func (s *Session) Prepare(ctx context.Context, topic, source string) (string, error) {
	return s.Guide(ctx, GuideReq{Topic: topic, Source: source, Depth: s.Cfg.Depth})
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
			s.termf("Descarté un enlace que no responde: %s\n", r.URL)
			continue
		}
		out = append(out, r)
	}
	return out
}

func (s *Session) guideBody(existing, guide string, refs []vault.SourceRef, local []vault.Resource) string {
	body := vault.ReplaceSection(existing, s.h("Guía"), guide)
	return s.V.SetRecursos(body, s.h("Recursos"), refs, local)
}

// enrich checks the material the model proposed against its catalogue (a video must exist, a DOI
// must resolve) and completes it: real title, author or channel, year, reliability. Without a
// catalogue client (the terminal offline, tests) it is kept as it is.
func (s *Session) enrich(ctx context.Context, in []vault.Resource) []vault.Resource {
	if s.Sources == nil {
		return in
	}
	var out []vault.Resource
	for _, r := range in {
		if !vault.External(r) {
			out = append(out, r)
			continue
		}
		checked, ok := s.Sources.Verify(ctx, r)
		if !ok {
			s.termf("Descarté una fuente que no existe: %s\n", r.URL)
			continue
		}
		checked.Why = r.Why
		if checked.Lang == "" && vault.YouTubeID(checked.URL) == "" {
			checked.Lang = s.lang()
		}
		out = append(out, checked)
	}
	return out
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

// afterPrepare continues with the test and the Feynman phase of an already prepared topic. On a
// roadmap, the next topic is unlocked once both are over, not between them.
func (s *Session) afterPrepare(ctx context.Context, slug string) error {
	if err := s.practice(ctx, slug); err != nil {
		return err
	}
	return s.advance(ctx, slug)
}

func (s *Session) practice(ctx context.Context, slug string) error {
	if ok, _ := s.confirm("\n¿Respondemos unas preguntas?"); !ok {
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
	if err := s.quiz(ctx, slug, n, diff); err != nil {
		return err
	}
	if ok, _ := s.confirm("\n¿Lo explicas con tus palabras?"); !ok {
		return nil
	}
	return s.feynman(ctx, slug)
}

func (s *Session) confirm(label string) (bool, error) {
	a, err := s.readLine(label+yesNo, "s")
	if err != nil {
		return false, err
	}
	return yes(a), nil
}
