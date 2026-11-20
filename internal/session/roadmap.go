package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/krilinxito/studyhomie/internal/i18n"
	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/roadmap"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// RoadmapOpts are the learner's choices for a new roadmap. Zero values are asked (Reach) or taken
// from the config (GuideDepth).
type RoadmapOpts struct {
	Reach      int  // how much of the field it covers: roadmap.ReachFocused…ReachScratch
	GuideDepth *int // levels of concept notes each guide gets
}

// reachChoices are the answers to the reach question; each starts with its value.
var reachChoices = []string{
	i18n.N("1 · Enfocado: solo los temas de la materia, doy por sabidas sus bases"),
	i18n.N("2 · Con bases: las bases que necesita entran como temas propios"),
	i18n.N("3 · Desde cero: cada base en varios temas, y los campos de apoyo también"),
}

// reachName is how a reach reads in a sentence.
var reachName = map[int]string{1: i18n.N("enfocado"), 2: i18n.N("con bases"), 3: i18n.N("desde cero")}

// CardRoadmap shows a roadmap as a numbered sequence (Slug, Topic, Steps).
const CardRoadmap = "roadmap"

// RoadmapStep is one topic of a roadmap as a UI draws it.
type RoadmapStep struct {
	N       int    `json:"n"`
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
	Block   string `json:"block,omitempty"`
	State   string `json:"state"` // passed | current | locked
	Ready   bool   `json:"ready"` // its guide is written
	Mastery int    `json:"mastery,omitempty"`
}

// Roadmap asks about the learner's scope and traces a numbered sequence of topics for a subject.
// Only the first topic's guide is written now; each next one is written when the learner passes
// the one before. It returns the roadmap's slug.
func (s *Session) Roadmap(ctx context.Context, subject string, o RoadmapOpts) (string, error) {
	s.printf("Voy a hacerte unas preguntas para ajustar el plan de «%s».\n", subject)
	resp, err := s.convo(prompts.Get("scope", s.aiLang())).Say(ctx, "Subject: "+subject, nil)
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
		if !s.card(Card{Kind: CardQuestion, N: i + 1, Total: len(qs.Questions), Question: q}) {
			s.printf("\n%d/%d. %s\n", i+1, len(qs.Questions), q)
		}
		a, err := s.readLine("Respuesta", "")
		if err != nil {
			return "", err
		}
		if a == "" {
			a = "(sin respuesta)"
		}
		fmt.Fprintf(&scope, "Q: %s\nA: %s\n\n", q, a)
	}
	if o.Reach == 0 {
		if o.Reach, err = s.askReach(); err != nil {
			return "", err
		}
	}
	return s.buildRoadmap(ctx, roadmapReq{subject: subject, scope: scope.String(), source: "roadmap", opts: o})
}

// askReach asks how much of the field the roadmap covers.
func (s *Session) askReach() (int, error) {
	q := i18n.N("¿Qué tanto abarca la ruta?")
	choices := make([]string, len(reachChoices))
	for i, c := range reachChoices {
		choices[i] = s.tr(c)
	}
	if !s.card(Card{Kind: CardQuestion, Question: s.tr(q), Choices: choices}) {
		s.printf("\n%s\n", q)
		for _, c := range choices {
			s.printf("   %s\n", c)
		}
	}
	a, err := s.readLine("Profundidad de la ruta (1-3)", "2")
	if err != nil {
		return 0, err
	}
	return parseReach(a), nil
}

// parseReach reads an answer to the reach question: its leading digit, or 2.
func parseReach(a string) int {
	a = strings.TrimSpace(a)
	if a != "" && a[0] >= '1' && a[0] <= '0'+roadmap.MaxReach {
		return int(a[0] - '0')
	}
	return roadmap.ReachBases
}

type roadmapReq struct {
	subject, scope, outline, materialSlug, source string
	opts                                          RoadmapOpts
}

// buildRoadmap generates the plan, validates it, writes the roadmap note and one stub note per
// topic (so the connections show up in the vault graph), and writes the first topic's guide.
func (s *Session) buildRoadmap(ctx context.Context, r roadmapReq) (string, error) {
	reach := max(roadmap.ReachFocused, min(r.opts.Reach, roadmap.MaxReach))
	guideDepth := s.Cfg.Depth
	if r.opts.GuideDepth != nil {
		guideDepth = *r.opts.GuideDepth
	}
	guideDepth = max(0, min(guideDepth, MaxDepth))

	prompt := fmt.Sprintf("Subject: %s\nROADMAP DEPTH: %d\n\nLearner's answers about scope:\n%s", r.subject, reach, r.scope)
	if r.outline != "" {
		prompt += "\nSOURCE MATERIAL OUTLINE (build the roadmap from this):\n" + r.outline
	}
	s.status("Trazando el roadmap")
	resp, err := s.convo(prompts.Get("roadmap", s.aiLang())).Say(ctx, prompt, nil)
	if err != nil {
		return "", err
	}
	var plan roadmap.Plan
	if err := decodeJSON(resp.Text, &plan); err != nil {
		return "", err
	}
	plan, warnings, err := plan.Normalize()
	if err != nil {
		return "", failf("el roadmap generado no es válido: %w", err)
	}
	for _, w := range warnings {
		s.termf("Aviso: %s\n", w)
	}
	rslug := vault.Slugify(r.subject)
	if rslug == "" {
		return "", fmt.Errorf("invalid subject %q", r.subject)
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
	block := ""
	for i, n := range plan.Nodes {
		slug := n.Slug()
		var conn strings.Builder
		if i > 0 {
			prev := plan.Nodes[i-1].Slug()
			fmt.Fprintf(&conn, "- %s %s\n", s.tr("Antes:"), link(prev))
		}
		if i+1 < len(plan.Nodes) {
			fmt.Fprintf(&conn, "- %s %s\n", s.tr("Después:"), link(plan.Nodes[i+1].Slug()))
		}
		if len(n.Requires) > 0 {
			fmt.Fprintf(&conn, "- %s %s\n", s.tr("Requiere:"), linkAll(n.Requires))
		}
		if len(n.Related) > 0 {
			fmt.Fprintf(&conn, "- %s %s\n", s.tr("Relacionado:"), linkAll(n.Related))
		}
		fmt.Fprintf(&conn, "- %s [[%s]]\n", s.tr("Ruta:"), rslug)

		note := vault.Note{Slug: slug, Meta: vault.Meta{Title: n.Title, Status: "new", Source: r.source}}
		if s.V.Exists(slug) {
			if note, err = s.V.Load(slug); err != nil {
				return "", err
			}
		} else {
			note.Body = "> " + n.Summary + "\n"
		}
		note.Meta.Roadmap, note.Meta.Order, note.Meta.Requires = rslug, i+1, n.Requires
		note.Meta.Summary, note.Meta.Block = n.Summary, strings.TrimSpace(n.Block)
		if r.materialSlug != "" {
			note.Meta.Material = r.materialSlug
		}
		note.Body = vault.ReplaceSection(note.Body, s.h("Conexiones"), conn.String())
		if err := s.V.Save(note); err != nil {
			return "", err
		}

		if b := strings.TrimSpace(n.Block); b != "" && b != block {
			block = b
			fmt.Fprintf(&route, "\n**%s**\n\n", b)
		}
		fmt.Fprintf(&route, "%d. %s — %s\n", i+1, link(slug), n.Summary)
	}

	pass := s.passScore()
	how := s.trf("Ruta %s, en orden: cada tema se desbloquea al sacar %d/100 o más en el anterior (prueba o Feynman). Cada guía llega con %d %s de conceptos.",
		s.tr(reachName[reach]), pass, guideDepth, s.tr(plural(guideDepth, i18n.N("nivel"), i18n.N("niveles"))))
	body := "## " + s.h("Alcance") + "\n\n" + strings.TrimSpace(r.scope) + "\n\n## " + s.h("Cómo avanzar") + "\n\n" + how +
		"\n\n## " + s.h("Ruta") + "\n\n" + strings.TrimLeft(route.String(), "\n")
	rn := vault.Note{Slug: rslug, Meta: vault.Meta{Title: plan.Subject, Source: r.source, Material: r.materialSlug, Reach: reach, GuideDepth: &guideDepth}, Body: body}
	if rn.Meta.Title == "" {
		rn.Meta.Title = r.subject
	}
	if err := s.V.SaveIn("roadmaps", rn); err != nil {
		return "", err
	}
	if !s.showRoadmap(rslug) {
		s.printf("\nRoadmap «%s» (%d temas):\n\n%s", rn.Meta.Title, len(plan.Nodes), route.String())
	}
	s.termf("\nGuardado en %s/roadmaps/%s.md\n", s.V.Root, rslug)

	first, err := s.V.Load(plan.Nodes[0].Slug())
	if err != nil {
		return rslug, err
	}
	if !hasGuide(first) {
		s.printf("\nEmpiezo por el tema 1: «%s». Los siguientes se escriben a medida que apruebas cada uno.\n", first.Meta.Title)
		if _, err := s.topicGuide(ctx, first, rn); err != nil {
			return rslug, err
		}
	}
	return rslug, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// passScore is the mastery a roadmap topic needs to unlock the next one.
func (s *Session) passScore() int { return max(0, min(s.Cfg.PassScore, 100)) }

// hasGuide reports whether a topic note already has its study guide.
func hasGuide(n vault.Note) bool { return vault.Section(n.Body, "Guía") != "" }

// roadmapTopics returns the roadmap note and its topics. A missing roadmap is an input error.
func (s *Session) roadmapTopics(rslug string) (vault.Note, []vault.Note, error) {
	all, err := s.V.List()
	if err != nil {
		return vault.Note{}, nil, err
	}
	var topics []vault.Note
	for _, n := range all {
		if n.Meta.Roadmap == rslug {
			topics = append(topics, n)
		}
	}
	if len(topics) == 0 {
		return vault.Note{}, nil, inputErrorf("no encuentro el roadmap «%s» (usa el nombre con el que lo creaste)", rslug)
	}
	rn, err := s.V.LoadFrom("roadmaps", rslug)
	if err != nil {
		rn = vault.Note{Slug: rslug, Meta: vault.Meta{Title: rslug}} // topics outlive a deleted roadmap note
	}
	return rn, topics, nil
}

// RoadmapSteps returns a roadmap's topics in order with the learner's progress on each.
func RoadmapSteps(topics []vault.Note, pass int) []RoadmapStep {
	var out []RoadmapStep
	for i, st := range roadmap.Steps(topics, pass) {
		state := "locked"
		switch {
		case st.Passed:
			state = "passed"
		case st.Open:
			state = "current"
		}
		n := st.Note
		out = append(out, RoadmapStep{N: i + 1, Slug: n.Slug, Title: n.Meta.Title, Summary: n.Meta.Summary, Block: n.Meta.Block,
			State: state, Ready: hasGuide(n), Mastery: n.Meta.Mastery})
	}
	return out
}

// showRoadmap sends the roadmap card to a UI and reports whether one is attached.
func (s *Session) showRoadmap(rslug string) bool {
	if !s.rich() {
		return false
	}
	rn, topics, err := s.roadmapTopics(rslug)
	if err != nil {
		return false
	}
	return s.card(Card{Kind: CardRoadmap, Slug: rslug, Topic: rn.Meta.Title, Score: s.passScore(), Steps: RoadmapSteps(topics, s.passScore())})
}

// topicGuide writes the guide of a roadmap topic with the roadmap's guide depth. The depth was
// chosen with the roadmap, so the plan of concept notes is not put up for approval.
func (s *Session) topicGuide(ctx context.Context, n vault.Note, rn vault.Note) (string, error) {
	depth := s.Cfg.Depth
	if rn.Meta.GuideDepth != nil {
		depth = *rn.Meta.GuideDepth
	}
	source := n.Meta.Source
	if source == "" {
		source = "roadmap"
	}
	return s.Guide(ctx, GuideReq{Topic: n.Meta.Title, Source: source, Depth: depth, Auto: true})
}

// checkUnlocked refuses a guide for a roadmap topic whose earlier topics are not passed yet.
func (s *Session) checkUnlocked(n vault.Note) error {
	if n.Meta.Roadmap == "" {
		return nil
	}
	_, topics, err := s.roadmapTopics(n.Meta.Roadmap)
	if err != nil {
		return nil // the roadmap is gone: nothing locks the topic
	}
	if b, locked := roadmap.Blocker(topics, n.Slug, s.passScore()); locked {
		return inputErrorf("«%s» todavía está bloqueado: primero aprueba «%s» (tema %d) con %d/100 o más",
			n.Meta.Title, b.Meta.Title, b.Meta.Order, s.passScore())
	}
	return nil
}

// advance follows the roadmap of a topic whose score just changed: when the topic is passed and the
// next one has no guide yet, it writes it. Topics outside a roadmap are left alone.
func (s *Session) advance(ctx context.Context, slug string) error {
	n, err := s.V.Load(slug)
	if err != nil || n.Meta.Roadmap == "" {
		return nil
	}
	pass := s.passScore()
	rn, topics, err := s.roadmapTopics(n.Meta.Roadmap)
	if err != nil {
		return nil
	}
	if !roadmap.Passed(n, pass) {
		s.printf("\nPara desbloquear el siguiente tema de «%s» necesitas %d/100 en «%s» (llevas %d). Repasa y vuelve a intentarlo.\n",
			rn.Meta.Title, pass, n.Meta.Title, n.Meta.Mastery)
		return nil
	}
	next, ok := roadmap.Next(topics, pass)
	if !ok {
		s.showRoadmap(rn.Slug)
		s.printf("\n¡Completaste el roadmap «%s»!\n", rn.Meta.Title)
		return nil
	}
	if hasGuide(next) {
		s.printf("\nSiguiente tema de «%s»: %d. «%s», y su guía ya está lista.\n", rn.Meta.Title, next.Meta.Order, next.Meta.Title)
		return nil
	}
	s.printf("\nDesbloqueaste el tema %d de «%s»: «%s». Escribo su guía.\n", next.Meta.Order, rn.Meta.Title, next.Meta.Title)
	_, err = s.topicGuide(ctx, next, rn)
	return err
}

// NextGuide makes sure the topic the learner is on has its guide, writing it if needed, and
// returns its slug. It is what a UI's "next topic" button runs.
func (s *Session) NextGuide(ctx context.Context, rslug string) (string, error) {
	rn, topics, err := s.roadmapTopics(rslug)
	if err != nil {
		return "", err
	}
	n, ok := roadmap.Next(topics, s.passScore())
	if !ok {
		s.printf("¡Completaste todo el roadmap «%s»!\n", rn.Meta.Title)
		return "", nil
	}
	if hasGuide(n) {
		s.card(Card{Kind: CardGuide, Slug: n.Slug, Topic: n.Meta.Title})
		return n.Slug, nil
	}
	return s.topicGuide(ctx, n, rn)
}

// Next suggests the topic of a roadmap the learner is on and studies it: its guide (written now if
// needed), the test and the Feynman phase.
func (s *Session) Next(ctx context.Context, rslug string) error {
	rn, topics, err := s.roadmapTopics(rslug)
	if err != nil {
		return err
	}
	n, ok := roadmap.Next(topics, s.passScore())
	if !ok {
		s.printf("¡Completaste todo el roadmap «%s»!\n", rn.Meta.Title)
		return nil
	}
	s.printf("Siguiente tema: %s (%d/%d)\n", n.Meta.Title, n.Meta.Order, len(topics))
	if n.Meta.Summary != "" {
		s.printf("Objetivo: %s\n", n.Meta.Summary)
	}
	if ok, _ := s.confirm("¿Empezamos?"); !ok {
		return nil
	}
	if !hasGuide(n) {
		if _, err := s.topicGuide(ctx, n, rn); err != nil {
			return err
		}
	}
	return s.afterPrepare(ctx, n.Slug)
}
