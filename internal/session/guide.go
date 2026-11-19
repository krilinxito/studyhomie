package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/vault"
	"github.com/krilinxito/studyhomie/internal/viz"
)

// GuideReq asks for a study guide and, below it, the concept notes it stands on.
type GuideReq struct {
	Topic  string
	Focus  string // what the learner wants out of it, free text
	Source string // roadmap | material | daily | adhoc
	Depth  int    // levels of prerequisites that get their own note; 0 is the guide alone
}

// MaxDepth bounds how far down the prerequisites a plan goes.
const MaxDepth = 4

// maxPlanned bounds the concept notes of one guide.
const maxPlanned = 20

// PlannedConcept is one concept note of a plan.
type PlannedConcept struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Summary  string   `json:"summary,omitempty"`
	Category string   `json:"category,omitempty"`
	Requires []string `json:"requires,omitempty"`
	Level    int      `json:"level"`            // 1: the topic needs it directly
	Exists   bool     `json:"exists,omitempty"` // already in the vault: linked, not rewritten
}

// ConceptPlan is the tree of concept notes under a guide.
type ConceptPlan struct {
	Topic    string           `json:"topic"`
	Depth    int              `json:"depth"`
	Base     []string         `json:"base"` // ids the topic needs directly
	Concepts []PlannedConcept `json:"concepts"`
}

func (p ConceptPlan) byID() map[string]PlannedConcept {
	m := make(map[string]PlannedConcept, len(p.Concepts))
	for _, c := range p.Concepts {
		m[c.ID] = c
	}
	return m
}

// rawPlan is what the planner model returns.
type rawPlan struct {
	Title    string   `json:"title"`
	Requires []string `json:"requires"`
	Concepts []struct {
		ID       string   `json:"id"`
		Title    string   `json:"title"`
		Summary  string   `json:"summary"`
		Category string   `json:"category"`
		Requires []string `json:"requires"`
	} `json:"concepts"`
}

// normalizePlan turns the model's plan into a clean tree: ids normalized, references to unknown
// concepts dropped (unless the vault has them), levels computed from the topic down, everything
// below depth or unreachable cut, and at most maxPlanned concepts kept, the deepest ones first to go.
func normalizePlan(raw rawPlan, topic string, depth int, vaultTitle func(id string) (string, bool)) ConceptPlan {
	all := map[string]PlannedConcept{}
	var order []string
	for _, c := range raw.Concepts {
		id := vault.ConceptID(c.ID)
		if id == "" {
			id = vault.ConceptID(c.Title)
		}
		title := strings.TrimSpace(c.Title)
		if id == "" || title == "" || all[id].ID != "" {
			continue
		}
		pc := PlannedConcept{ID: id, Title: title, Summary: strings.TrimSpace(c.Summary), Category: vault.ConceptID(c.Category), Requires: c.Requires}
		if t, ok := vaultTitle(id); ok {
			pc.Exists = true
			if t != "" {
				pc.Title = t
			}
		}
		all[id] = pc
		order = append(order, id)
	}
	topicID := vault.ConceptID(topic)
	// resolve a reference: a planned concept, or one the vault already has
	resolve := func(ref string) (string, bool) {
		id := vault.ConceptID(ref)
		if id == "" || id == topicID {
			return "", false
		}
		if _, ok := all[id]; ok {
			return id, true
		}
		if t, ok := vaultTitle(id); ok {
			all[id] = PlannedConcept{ID: id, Title: t, Exists: true}
			order = append(order, id)
			return id, true
		}
		return "", false
	}
	clean := func(refs []string, self string) []string {
		var out []string
		seen := map[string]bool{self: true}
		for _, r := range refs {
			if id, ok := resolve(r); ok && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		return out
	}
	base := clean(raw.Requires, "")
	for _, id := range append([]string(nil), order...) {
		c := all[id]
		c.Requires = clean(c.Requires, id)
		all[id] = c
	}
	plan := levels(all, order, base, depth)
	plan.Topic = topic
	return plan
}

// levels keeps the concepts reachable from base within depth levels (breadth first, so each gets
// its shortest distance to the topic). A concept the vault already has is linked but not expanded:
// its own prerequisites are already written.
func levels(all map[string]PlannedConcept, order, base []string, depth int) ConceptPlan {
	level := map[string]int{}
	queue := []string{}
	for _, id := range base {
		if _, ok := all[id]; ok && level[id] == 0 {
			level[id] = 1
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		c := all[id]
		if c.Exists || level[id] >= depth {
			continue
		}
		for _, r := range c.Requires {
			if _, ok := all[r]; ok && level[r] == 0 {
				level[r] = level[id] + 1
				queue = append(queue, r)
			}
		}
	}
	rank := map[string]int{}
	for i, id := range order {
		rank[id] = i
	}
	var kept []PlannedConcept
	for _, id := range order {
		if l := level[id]; l > 0 {
			c := all[id]
			c.Level = l
			kept = append(kept, c)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Level < kept[j].Level })
	if len(kept) > maxPlanned {
		kept = kept[:maxPlanned]
	}
	in := map[string]bool{}
	for _, c := range kept {
		in[c.ID] = true
	}
	var b []string
	for _, id := range base {
		if in[id] {
			b = append(b, id)
		}
	}
	for i := range kept {
		var reqs []string
		for _, r := range kept[i].Requires {
			if in[r] {
				reqs = append(reqs, r)
			}
		}
		kept[i].Requires = reqs
	}
	return ConceptPlan{Depth: depth, Base: b, Concepts: kept}
}

// planEdit is how the learner changes a proposed plan: concepts to leave out and new ones to add.
type planEdit struct {
	Skip []string `json:"skip"`
	Add  []string `json:"add"`
}

// applyEdit removes the skipped concepts (and whatever only they led to) and adds new ones the
// topic needs directly.
func applyEdit(p ConceptPlan, e planEdit, vaultTitle func(id string) (string, bool)) ConceptPlan {
	skip := map[string]bool{}
	for _, id := range e.Skip {
		skip[vault.ConceptID(id)] = true
	}
	all := map[string]PlannedConcept{}
	var order []string
	for _, c := range p.Concepts {
		if !skip[c.ID] {
			all[c.ID] = c
			order = append(order, c.ID)
		}
	}
	base := append([]string(nil), p.Base...)
	for _, t := range e.Add {
		t = strings.TrimSpace(t)
		id := vault.ConceptID(t)
		if id == "" || skip[id] {
			continue
		}
		if _, ok := all[id]; !ok {
			c := PlannedConcept{ID: id, Title: t}
			if vt, ok := vaultTitle(id); ok {
				c.Exists = true
				if vt != "" {
					c.Title = vt
				}
			}
			all[id] = c
			order = append(order, id)
		}
		base = append(base, id)
	}
	out := levels(all, order, base, p.Depth+1) // an added concept must not push the others out
	out.Topic, out.Depth = p.Topic, p.Depth
	return out
}

// conceptTitle looks a concept up in the vault for the planner.
func (s *Session) conceptTitle(id string) (string, bool) {
	c, err := s.V.LoadConcept(id)
	if err != nil {
		return "", false
	}
	return c.Meta.Title, true
}

func (s *Session) planConcepts(ctx context.Context, req GuideReq, note vault.Note) (ConceptPlan, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "TOPIC: %s\nDEPTH: %d\n", req.Topic, req.Depth)
	if req.Focus != "" {
		fmt.Fprintf(&b, "FOCUS: %s\n", req.Focus)
	}
	if note.Meta.Summary != "" {
		fmt.Fprintf(&b, "By the end the learner should be able to: %s\n", note.Meta.Summary)
	}
	if existing, _ := s.V.ListConcepts(); len(existing) > 0 {
		b.WriteString("\nEXISTING CONCEPTS:\n")
		for _, c := range existing {
			fmt.Fprintf(&b, "%s: %s\n", c.Slug, c.Meta.Title)
		}
	}
	if g := s.grounding(note); g != "" {
		b.WriteString("\nSOURCE MATERIAL (excerpts):\n" + head(g, 4000) + "\n")
	}
	resp, err := s.quickConvo(prompts.Get("plan", s.Cfg.Language)).Say(ctx, b.String(), nil)
	if err != nil {
		return ConceptPlan{}, err
	}
	var raw rawPlan
	if err := decodeJSON(resp.Text, &raw); err != nil {
		return ConceptPlan{}, err
	}
	return normalizePlan(raw, req.Topic, req.Depth, s.conceptTitle), nil
}

// Card kinds of the guide pipeline.
const (
	CardPlan     = "plan"     // Plan: the proposed concept notes, waiting for approval
	CardProgress = "progress" // Items: what is being written and how far it got
	CardGuide    = "guide"    // Slug, Topic: a guide is ready to read
)

// ProgressItem is one note being written.
type ProgressItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`  // guide | concept
	State string `json:"state"` // pending | working | done | linked | error
}

// approvePlan shows the plan and lets the learner accept it, drop or add concepts, keep only the
// guide, or stop. It returns ok=false when the learner stopped.
func (s *Session) approvePlan(p ConceptPlan) (ConceptPlan, bool, error) {
	for {
		pc := p
		if !s.card(Card{Kind: CardPlan, Plan: &pc}) {
			s.printf("\nNotas de concepto para «%s» (profundidad %d):\n", p.Topic, p.Depth)
			for _, c := range p.Concepts {
				mark := ""
				if c.Exists {
					mark = " (ya en tu vault)"
				}
				s.printf("%s- %s [%s]%s\n", strings.Repeat("  ", c.Level-1), c.Title, c.ID, mark)
			}
			s.printf("Responde s para generarlas, g para solo la guía, n para cancelar,\n" +
				"o edita: -id quita un concepto, +Título añade uno (ej.: -media +Varianza).\n")
		}
		a, err := s.readLine("¿Genero la guía con estas notas?", "s")
		if err != nil {
			return p, false, err
		}
		a = strings.TrimSpace(a)
		switch strings.ToLower(a) {
		case "", "s", "si", "sí", "y", "ok":
			return p, true, nil
		case "g", "guia", "guía":
			p.Concepts, p.Base = nil, nil
			return p, true, nil
		case "n", "no", "cancelar":
			return p, false, nil
		}
		var e planEdit
		if strings.HasPrefix(a, "{") {
			if err := json.Unmarshal([]byte(a), &e); err != nil {
				s.printf("No entendí la edición: %v\n", err)
				continue
			}
		} else {
			e = parseEditLine(a)
		}
		p = applyEdit(p, e, s.conceptTitle)
		if len(e.Skip) == 0 && len(e.Add) == 0 {
			s.printf("No entendí la respuesta.\n")
		}
	}
}

// parseEditLine reads "-id +Some title -other" edits typed in a terminal.
func parseEditLine(a string) planEdit {
	var e planEdit
	for _, part := range strings.Fields(strings.NewReplacer(" -", "\x00-", " +", "\x00+").Replace(" " + a)) {
		_ = part
	}
	for _, chunk := range strings.Split(strings.NewReplacer(" -", "\x00-", " +", "\x00+").Replace(" "+a), "\x00") {
		chunk = strings.TrimSpace(chunk)
		switch {
		case strings.HasPrefix(chunk, "-") && len(chunk) > 1:
			e.Skip = append(e.Skip, strings.TrimSpace(chunk[1:]))
		case strings.HasPrefix(chunk, "+") && len(chunk) > 1:
			e.Add = append(e.Add, strings.TrimSpace(chunk[1:]))
		}
	}
	return e
}

// progress tracks the notes being written and sends the whole list to the UI on every change.
type progress struct {
	s     *Session
	mu    sync.Mutex
	items []ProgressItem
}

func (p *progress) set(id, state string) {
	p.mu.Lock()
	for i := range p.items {
		if p.items[i].ID == id {
			p.items[i].State = state
		}
	}
	items := append([]ProgressItem(nil), p.items...)
	p.mu.Unlock()
	p.s.card(Card{Kind: CardProgress, Items: items})
}

// Guide plans the concept notes of a topic down to req.Depth levels, asks the learner to approve
// the plan, then writes the guide and the new concept notes at the same time. Concepts already in
// the vault are linked, never rewritten. It returns the guide's slug.
func (s *Session) Guide(ctx context.Context, req GuideReq) (string, error) {
	req.Topic = strings.TrimSpace(req.Topic)
	req.Focus = strings.TrimSpace(req.Focus)
	slug := vault.Slugify(req.Topic)
	if slug == "" {
		return "", fmt.Errorf("invalid topic %q", req.Topic)
	}
	req.Depth = max(0, min(req.Depth, MaxDepth))
	if req.Source == "" {
		req.Source = "adhoc"
	}
	var note vault.Note
	if s.V.Exists(slug) {
		var err error
		if note, err = s.V.Load(slug); err != nil {
			return "", err
		}
		req.Topic = note.Meta.Title
	} else {
		note = vault.Note{Slug: slug, Meta: vault.Meta{Title: req.Topic, Source: req.Source}}
	}

	s.card(Card{Kind: CardPhase, Phase: "prepare"})
	plan := ConceptPlan{Topic: req.Topic, Depth: req.Depth}
	if req.Depth > 0 {
		s.printf("Planeando los conceptos base de «%s» (profundidad %d)...\n", req.Topic, req.Depth)
		p, err := s.planConcepts(ctx, req, note)
		switch {
		case err != nil && ctx.Err() != nil:
			return "", ctx.Err()
		case err != nil:
			s.printf("Aviso: no pude planear los conceptos (%v); escribo solo la guía.\n", err)
		case len(p.Concepts) > 0:
			var ok bool
			if plan, ok, err = s.approvePlan(p); err != nil {
				return "", err
			} else if !ok {
				s.printf("De acuerdo, no generé nada.\n")
				return "", nil
			}
		}
	}

	prog := &progress{s: s, items: []ProgressItem{{ID: slug, Title: req.Topic, Kind: "guide", State: "pending"}}}
	var fresh []PlannedConcept
	for _, c := range plan.Concepts {
		st := "pending"
		if c.Exists {
			st = "linked"
		} else {
			fresh = append(fresh, c)
		}
		prog.items = append(prog.items, ProgressItem{ID: c.ID, Title: c.Title, Kind: "concept", State: st})
	}
	if len(plan.Concepts) > 0 {
		s.printf("Escribiendo la guía y %d notas de concepto nuevas (%d ya estaban en tu vault)...\n",
			len(fresh), len(plan.Concepts)-len(fresh))
	} else {
		s.printf("Preparando la guía de «%s» (busca material en línea, puede tardar)...\n", req.Topic)
	}
	prog.set(slug, "working")

	conceptErrs := make([]error, len(fresh))
	writeConcepts := func() {
		parallel := max(1, s.Cfg.Parallel)
		sem := make(chan struct{}, parallel)
		var wg sync.WaitGroup
		for i, c := range fresh {
			if parallel == 1 {
				prog.set(c.ID, "working")
				conceptErrs[i] = s.writeConcept(ctx, c, plan, req, slug)
				prog.set(c.ID, stateOf(conceptErrs[i]))
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				prog.set(c.ID, "working")
				conceptErrs[i] = s.writeConcept(ctx, c, plan, req, slug)
				prog.set(c.ID, stateOf(conceptErrs[i]))
			}()
		}
		wg.Wait()
	}

	var guideErr error
	if max(1, s.Cfg.Parallel) == 1 {
		guideErr = s.writeGuide(ctx, &note, req, plan)
		prog.set(slug, stateOf(guideErr))
		if guideErr == nil {
			writeConcepts()
		}
	} else {
		done := make(chan struct{})
		go func() {
			defer close(done)
			writeConcepts()
		}()
		guideErr = s.writeGuide(ctx, &note, req, plan)
		prog.set(slug, stateOf(guideErr))
		<-done
	}
	if guideErr != nil {
		return "", guideErr
	}
	for _, c := range plan.Concepts {
		if c.Exists {
			if err := s.linkConcept(c.ID, slug, req.Topic); err != nil {
				s.printf("Aviso: no pude enlazar «%s»: %v\n", c.Title, err)
			}
		}
	}
	failed := 0
	for i, err := range conceptErrs {
		if err != nil && ctx.Err() == nil {
			failed++
			s.printf("Aviso: no pude escribir «%s»: %v\n", fresh[i].Title, err)
		}
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	s.card(Card{Kind: CardGuide, Slug: slug, Topic: req.Topic})
	if len(plan.Concepts) > 0 {
		s.printf("Listo: la guía y %d notas de concepto en %s/.\n", len(plan.Concepts)-failed, vault.ConceptsDir)
	}
	return slug, nil
}

func stateOf(err error) string {
	if err != nil {
		return "error"
	}
	return "done"
}

// writeGuide writes the guide itself, verifies its links and widgets, and saves the note.
func (s *Session) writeGuide(ctx context.Context, note *vault.Note, req GuideReq, plan ConceptPlan) error {
	prompt := "Topic: " + req.Topic
	if req.Focus != "" {
		prompt += "\nFOCUS: " + req.Focus
	}
	if note.Meta.Summary != "" {
		prompt += "\nBy the end the learner should be able to: " + note.Meta.Summary
	}
	if len(plan.Concepts) > 0 {
		prompt += "\n\nCONCEPT NOTES:\n"
		for _, c := range plan.Concepts {
			prompt += c.ID + ": " + c.Title + "\n"
		}
	}
	if g := s.grounding(*note); g != "" {
		prompt += "\n\nSOURCE MATERIAL (excerpts from the learner's document):\n" + g
	}

	c := s.convo(prompts.Get("guide", s.Cfg.Language), "WebSearch", "WebFetch")
	resp, err := c.Say(ctx, prompt, nil)
	if err != nil {
		return err
	}
	guide, payload := splitJSON(resp.Text)
	guide, warns := viz.Clean(guide)
	for _, w := range warns {
		s.printf("Aviso: %s\n", w)
	}

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

	if disk, err := s.V.Load(note.Slug); err == nil {
		note.Body = disk.Body // keep a quiz or doubts written while the guide was being prepared
	}
	if note.Meta.Status == "" || note.Meta.Status == "new" {
		note.Meta.Status = "prepared" // re-preparing a guide must not undo later progress
	}
	note.Meta.Resources = resources
	note.Meta.Depth, note.Meta.Focus = req.Depth, req.Focus
	if len(plan.Concepts) > 0 || req.Depth > 0 {
		note.Meta.Concepts, note.Meta.Base = nil, plan.Base
		for _, c := range plan.Concepts {
			note.Meta.Concepts = append(note.Meta.Concepts, c.ID)
		}
	}
	note.Body = guideBody(note.Body, guide, resources)
	if err := s.V.Save(*note); err != nil {
		return err
	}
	handoff := out.Handoff
	if handoff == "" {
		handoff = guide
	}
	if err := s.V.SaveHandoff(note.Slug, handoff); err != nil {
		return err
	}
	if !s.card(Card{Kind: CardResources, Resources: resources}) || len(resources) == 0 {
		// a UI shows the guide in its reading panel; the terminal prints it
		if s.Card == nil {
			s.printf("\n%s\n\n", guide)
		}
	}
	s.printf("Guardado en %s/%s/%s.md\n", s.V.Root, vault.GuidesDir, note.Slug)
	return nil
}

// writeConcept writes one new concept note.
func (s *Session) writeConcept(ctx context.Context, c PlannedConcept, plan ConceptPlan, req GuideReq, guide string) error {
	byID := plan.byID()
	var b strings.Builder
	fmt.Fprintf(&b, "CONCEPT\nid: %s\ntitle: %s\n", c.ID, c.Title)
	if c.Summary != "" {
		fmt.Fprintf(&b, "what the learner must understand: %s\n", c.Summary)
	}
	fmt.Fprintf(&b, "\nGUIDE\ntopic: %s\n", req.Topic)
	if req.Focus != "" {
		fmt.Fprintf(&b, "focus: %s\n", req.Focus)
	}
	if len(c.Requires) > 0 {
		b.WriteString("\nBUILDS ON\n")
		for _, r := range c.Requires {
			fmt.Fprintf(&b, "%s: %s\n", r, byID[r].Title)
		}
	}
	b.WriteString("\nSIBLING CONCEPTS\n")
	for _, o := range plan.Concepts {
		if o.ID != c.ID {
			fmt.Fprintf(&b, "%s: %s\n", o.ID, o.Title)
		}
	}
	resp, err := s.quickConvo(prompts.Get("concept", s.Cfg.Language)).Say(ctx, b.String(), nil)
	if err != nil {
		return err
	}
	text, payload := splitJSON(resp.Text)
	text, warns := viz.Clean(strings.TrimSpace(text))
	for _, w := range warns {
		s.printf("Aviso (%s): %s\n", c.Title, w)
	}
	if text == "" {
		return fmt.Errorf("la nota salió vacía")
	}
	var meta struct {
		Aliases []string `json:"aliases"`
		Tags    []string `json:"tags"`
	}
	if payload != "" {
		_ = decodeJSON(payload, &meta) // the note is still good without them
	}
	body := "# " + c.Title + "\n\n" + text + "\n\n## Aparece en\n\n" + appearsLine(guide, req.Topic) + "\n"
	return s.V.SaveConcept(vault.Concept{
		Slug: c.ID,
		Meta: vault.ConceptMeta{
			Title: c.Title, Aliases: meta.Aliases, Tags: meta.Tags, Category: c.Category,
			Summary: c.Summary, Requires: c.Requires, Guides: []string{guide},
		},
		Body: body,
	})
}

func appearsLine(guide, title string) string { return "- [[" + guide + "|" + title + "]]" }

// linkConcept records that a guide uses a concept the vault already had.
func (s *Session) linkConcept(id, guide, title string) error {
	return s.V.UpdateConcept(id, func(c *vault.Concept) error {
		for _, g := range c.Meta.Guides {
			if g == guide {
				return nil
			}
		}
		c.Meta.Guides = append(c.Meta.Guides, guide)
		line := appearsLine(guide, title)
		if sec := vault.Section(c.Body, "Aparece en"); sec != "" {
			c.Body = vault.ReplaceSection(c.Body, "Aparece en", sec+"\n"+line)
		} else {
			c.Body = vault.ReplaceSection(c.Body, "Aparece en", line)
		}
		return nil
	})
}
