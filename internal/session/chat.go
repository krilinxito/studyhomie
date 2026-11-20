package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/roadmap"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// ChatMsg is one message of the learner in the chat, as the UI sends it (one JSON line).
type ChatMsg struct {
	Text   string      `json:"text"`
	Depth  *int        `json:"depth,omitempty"`  // depth selected for new guides
	Guide  string      `json:"guide,omitempty"`  // slug of the guide open next to the chat
	Action *ChatAction `json:"action,omitempty"` // a button the learner pressed: run it without asking the model
	Files  []string    `json:"files,omitempty"`  // local paths of attached files
	// Material: the files are notes to build a study line from; otherwise they go with the question
	Material bool `json:"material,omitempty"`
}

// ChatAction is something the chat starts: from the model's reply, or from a button.
type ChatAction struct {
	Action     string `json:"action"`
	Topic      string `json:"topic,omitempty"`
	Focus      string `json:"focus,omitempty"`
	Depth      *int   `json:"depth,omitempty"`
	N          int    `json:"n,omitempty"`
	Difficulty string `json:"difficulty,omitempty"`
	Field      string `json:"field,omitempty"`
	Subject    string `json:"subject,omitempty"`
	Title      string `json:"title,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Brief      string `json:"brief,omitempty"`
	Save       bool   `json:"save,omitempty"`
	Reach      int    `json:"reach,omitempty"`   // roadmap: how much of the field it covers (1-3); 0 asks
	Roadmap    string `json:"roadmap,omitempty"` // next: the roadmap whose current topic to open
}

// ChatOpts starts a chat.
type ChatOpts struct {
	First   *ChatMsg // the opening message, if the chat starts with one
	Guide   string   // the guide the chat is about, if any
	History string   // transcript of an earlier part of the conversation, when resumed
}

// ErrChatClosed ends a chat when its input closes (the UI parked or closed it).
var ErrChatClosed = errors.New("chat closed")

// readChat waits for the learner's next chat message.
func (s *Session) readChat() (ChatMsg, error) {
	if s.Ask != nil {
		s.Ask("chat", "", "")
	}
	line, err := s.In.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		if errors.Is(err, io.EOF) {
			return ChatMsg{}, ErrChatClosed
		}
		return ChatMsg{}, err
	}
	line = strings.TrimSpace(line)
	var m ChatMsg
	if strings.HasPrefix(line, "{") {
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			return ChatMsg{}, failf("mensaje inválido: %w", err)
		}
		return m, nil
	}
	m.Text = line // a terminal types plain text
	return m, nil
}

// Chat is the conversation everything else starts from. It answers doubts itself and runs the
// flows (guide, quiz, Feynman, daily, roadmap, material…) when the learner asks for them, then
// goes back to listening. It ends when its input closes.
func (s *Session) Chat(ctx context.Context, o ChatOpts) error {
	c := s.quickConvo(prompts.Get("chat", s.aiLang()))
	guide := vault.Slugify(o.Guide)
	announced := ""
	history := strings.TrimSpace(o.History)
	depth := s.Cfg.Depth
	next := o.First
	for {
		var m ChatMsg
		if next != nil {
			m, next = *next, nil
		} else {
			var err error
			if m, err = s.readChat(); err != nil {
				if errors.Is(err, ErrChatClosed) {
					return nil
				}
				return err
			}
		}
		if m.Depth != nil {
			depth = max(0, min(*m.Depth, MaxDepth))
		}
		if g := vault.Slugify(m.Guide); g != "" && s.V.Exists(g) {
			guide = g
		}
		var err error
		switch {
		case m.Material && len(m.Files) > 0:
			guide, err = s.chatMaterial(ctx, m.Files, guide, depth)
		case m.Action != nil:
			guide, err = s.runAction(ctx, *m.Action, guide, depth)
		case strings.TrimSpace(m.Text) == "" && len(m.Files) == 0:
			continue
		default:
			text := m.Text
			if len(m.Files) > 0 {
				names := make([]string, len(m.Files))
				for i, f := range m.Files {
					names[i] = filepath.Base(f)
				}
				if strings.TrimSpace(text) == "" {
					text = "(No message: the learner only sent these files. Say briefly what they show and ask what they want to do with them.)"
				}
				text = "ATTACHED FILES (look at them before answering): " + strings.Join(names, ", ") + "\n\n" + text
			}
			prompt := s.chatPrompt(text, guide, &announced, depth, history)
			history = ""
			var act *ChatAction
			var answer string
			c.Files = m.Files
			answer, act, err = s.chatTurn(ctx, c, prompt)
			c.Files = nil
			if err == nil && act != nil {
				if act.Action == "none" {
					if act.Save && guide != "" {
						s.saveDoubt(guide, m.Text, answer)
					}
				} else {
					guide, err = s.runAction(ctx, *act, guide, depth)
				}
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// a failed step must not end the conversation
			s.printf("\nNo pude completarlo: %s\n", s.errText(err))
		}
	}
}

// chatPrompt prefixes the learner's words with what the model needs to know this turn.
func (s *Session) chatPrompt(text, guide string, announced *string, depth int, history string) string {
	var b strings.Builder
	if history != "" {
		b.WriteString("CONVERSATION SO FAR:\n" + tail(history, 6000) + "\n\n")
	}
	if guide != "" && guide != *announced {
		if n, err := s.V.Load(guide); err == nil {
			fmt.Fprintf(&b, "ACTIVE GUIDE: %s (id %s)\nSummary: %s\n\n", n.Meta.Title, guide, head(s.noteContext(guide, n), 3000))
			*announced = guide
		}
	}
	fmt.Fprintf(&b, "DEPTH: %d\n\nLEARNER:\n%s", depth, strings.TrimSpace(text))
	return b.String()
}

// chatTurn sends one message and streams the answer, holding back the JSON block at the end.
func (s *Session) chatTurn(ctx context.Context, c *Convo, prompt string) (string, *ChatAction, error) {
	var full strings.Builder
	sent := 0
	flush := func(final bool) {
		text := full.String()
		limit := len(text)
		if i := strings.Index(text, fence+"json"); i >= 0 {
			limit = i
		} else if !final {
			// the opening of the JSON block may be arriving: hold back as many bytes as it has
			limit = max(sent, len(text)-len(fence+"json"))
		}
		for limit > sent && limit < len(text) && !utf8Start(text[limit]) {
			limit--
		}
		if limit > sent {
			s.printf("%s", text[sent:limit])
			sent = limit
		}
	}
	resp, err := c.Say(ctx, prompt, func(chunk string) {
		full.WriteString(chunk)
		flush(false)
	})
	if err != nil {
		return "", nil, err
	}
	if full.Len() == 0 { // a backend that does not stream
		full.WriteString(resp.Text)
	}
	flush(true)
	s.printf("\n")
	text, payload := splitJSON(full.String())
	if payload == "" {
		return text, nil, nil
	}
	var act ChatAction
	if err := json.Unmarshal([]byte(payload), &act); err != nil || act.Action == "" {
		return text, nil, nil
	}
	return text, &act, nil
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// runAction runs one flow and returns the guide the chat is about afterwards.
func (s *Session) runAction(ctx context.Context, a ChatAction, guide string, depth int) (string, error) {
	needGuide := func() error {
		if guide == "" {
			return inputErrorf("primero abre o genera una guía")
		}
		return nil
	}
	switch a.Action {
	case "guide":
		d := depth
		if a.Depth != nil {
			d = *a.Depth
		}
		topic := strings.TrimSpace(a.Topic)
		if topic == "" {
			return guide, inputErrorf("¿de qué tema hago la guía?")
		}
		slug, err := s.Guide(ctx, GuideReq{Topic: topic, Focus: a.Focus, Depth: d, Source: "adhoc"})
		if err != nil || slug == "" {
			return guide, err
		}
		return slug, nil
	case "quiz":
		if err := needGuide(); err != nil {
			return guide, err
		}
		n := a.N
		if n < 1 || n > 30 {
			n = 5
		}
		diff := a.Difficulty
		if diff == "" {
			diff = "media"
		}
		return guide, s.Quiz(ctx, guide, n, diff)
	case "feynman":
		if err := needGuide(); err != nil {
			return guide, err
		}
		return guide, s.Feynman(ctx, guide)
	case "interactive":
		if err := needGuide(); err != nil {
			return guide, err
		}
		return guide, s.Interactive(ctx, guide, a.Brief)
	case "concept":
		id, err := s.ConceptNote(ctx, a.Title, a.Summary, guide)
		if err == nil {
			s.card(Card{Kind: CardConcept, Slug: id, Topic: strings.TrimSpace(a.Title)})
		}
		return guide, err
	case "daily":
		return guide, s.Daily(ctx, a.Field)
	case "roadmap":
		subject := strings.TrimSpace(a.Subject)
		if subject == "" {
			subject = strings.TrimSpace(a.Topic)
		}
		if subject == "" {
			return guide, inputErrorf("¿qué materia quieres planear?")
		}
		d := depth
		rslug, err := s.Roadmap(ctx, subject, RoadmapOpts{Reach: a.Reach, GuideDepth: &d})
		if err != nil {
			return guide, err
		}
		return s.currentGuide(rslug, guide), nil
	case "next":
		slug, err := s.NextGuide(ctx, vault.Slugify(a.Roadmap))
		if err != nil || slug == "" {
			return guide, err
		}
		return slug, nil
	}
	return guide, failf("acción desconocida %q", a.Action)
}

// CardConcept says a concept note is ready (Slug, Topic).
const CardConcept = "concept"

// chatMaterial builds a roadmap from attached files and returns the guide the chat is about after.
func (s *Session) chatMaterial(ctx context.Context, paths []string, guide string, depth int) (string, error) {
	rslug, err := s.Material(ctx, paths, RoadmapOpts{GuideDepth: &depth})
	if err != nil {
		return guide, err
	}
	return s.currentGuide(rslug, guide), nil
}

// currentGuide is the guide of the topic the learner is on in a roadmap, or fallback when it has none.
func (s *Session) currentGuide(rslug, fallback string) string {
	_, topics, err := s.roadmapTopics(rslug)
	if err != nil {
		return fallback
	}
	if n, ok := roadmap.Next(topics, s.passScore()); ok && hasGuide(n) {
		return n.Slug
	}
	return fallback
}

// saveDoubt files a question and its answer in the guide's "Dudas" section.
func (s *Session) saveDoubt(guide, question, answer string) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return
	}
	entry := s.doubtEntry(strings.Join(strings.Fields(question), " "), answer)
	err := s.V.Update(guide, func(n *vault.Note) error {
		after := "Guía"
		if vault.Section(n.Body, baseHeading) != "" {
			after = baseHeading
		}
		all := entry
		if prev := vault.Section(n.Body, doubtsHeading); prev != "" {
			all = prev + "\n\n" + entry
		}
		n.Body = vault.PlaceSection(n.Body, s.h(doubtsHeading), all, after)
		return nil
	})
	if err != nil {
		s.printf("Aviso: no pude guardar la duda en la guía: %v\n", err)
	}
}

// ConceptNote writes a concept note on its own (asked for in the chat), linked to a guide when
// there is one. A concept the vault already has is only linked.
func (s *Session) ConceptNote(ctx context.Context, title, summary, guide string) (string, error) {
	title = strings.TrimSpace(title)
	id := vault.ConceptID(title)
	if id == "" {
		return "", inputErrorf("el concepto necesita un nombre")
	}
	if guide != "" && s.V.Exists(guide) {
		return s.Explain(ctx, guide, title, summary)
	}
	gTitle := ""
	if s.V.ConceptExists(id) {
		if gTitle != "" {
			return id, s.linkConcept(id, guide, gTitle)
		}
		return id, nil
	}
	s.status("Escribiendo la nota «%s»", title)
	plan := ConceptPlan{Topic: gTitle}
	c := PlannedConcept{ID: id, Title: title, Summary: strings.TrimSpace(summary), Level: 1}
	plan.Concepts = []PlannedConcept{c}
	req := GuideReq{Topic: gTitle}
	if gTitle == "" {
		req.Topic = title
	}
	if err := s.writeConcept(ctx, c, plan, req, guide); err != nil {
		return "", err
	}
	if guide == "" {
		// no guide to point back to
		_ = s.V.UpdateConcept(id, func(cn *vault.Concept) error {
			cn.Meta.Guides = nil
			cn.Body = vault.ReplaceSection(cn.Body, s.h("Aparece en"), s.tr("Surgió en el chat."))
			return nil
		})
	} else {
		_ = s.V.Update(guide, func(n *vault.Note) error {
			for _, x := range n.Meta.Concepts {
				if x == id {
					return nil
				}
			}
			n.Meta.Concepts = append(n.Meta.Concepts, id)
			return nil
		})
	}
	return id, nil
}
