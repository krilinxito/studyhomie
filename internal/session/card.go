package session

import "github.com/krilinxito/studyhomie/internal/vault"

// Card kinds a rich UI can draw instead of plain text.
const (
	CardDaily     = "daily"     // Field, Topic, Hook: today's proposed challenge
	CardPhase     = "phase"     // Phase: prepare | quiz | feynman
	CardQuestion  = "question"  // N, Total, Question, Choices (empty for an open question)
	CardScore     = "score"     // Label, Score, Detail
	CardResources = "resources" // Resources: the verified material of a guide
)

// Card is one structured piece of output. The terminal never sees cards: it keeps
// the plain text, and only draws a card when Session.Card is set.
type Card struct {
	Kind      string           `json:"kind"`
	Field     string           `json:"field,omitempty"`
	Topic     string           `json:"topic,omitempty"`
	Hook      string           `json:"hook,omitempty"`
	Phase     string           `json:"phase,omitempty"`
	N         int              `json:"n,omitempty"`
	Total     int              `json:"total,omitempty"`
	Question  string           `json:"question,omitempty"`
	Choices   []string         `json:"choices,omitempty"`
	Label     string           `json:"label,omitempty"`
	Score     int              `json:"score,omitempty"`
	Detail    string           `json:"detail,omitempty"`
	Resources []vault.Resource `json:"resources,omitempty"`
}

// card sends c to the UI and reports whether one is attached. Callers that also
// print the same information as text skip it when this returns true.
func (s *Session) card(c Card) bool {
	if s.Card == nil {
		return false
	}
	s.Card(c)
	return true
}
