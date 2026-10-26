package session

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const fence = "```"

// splitJSON separates the text before the last ```json fence from the JSON payload.
// If there is no fence, payload is empty and text is returned whole.
func splitJSON(s string) (text, payload string) {
	i := strings.LastIndex(s, fence+"json")
	if i < 0 {
		return strings.TrimSpace(s), ""
	}
	rest := s[i+len(fence+"json"):]
	end := strings.Index(rest, fence)
	if end < 0 {
		end = len(rest)
	}
	return strings.TrimSpace(s[:i]), strings.TrimSpace(rest[:end])
}

// decodeJSON parses a model reply that should be a single JSON block (fenced or bare).
func decodeJSON(s string, v any) error {
	_, payload := splitJSON(s)
	if payload == "" {
		payload = strings.TrimSpace(s)
	}
	if err := json.Unmarshal([]byte(payload), v); err != nil {
		return fmt.Errorf("model did not return valid JSON: %w", err)
	}
	return nil
}

var scoreRe = regexp.MustCompile(`(?m)^\s*SCORE:\s*(\d{1,3})\s*$`)

// extractScore finds the final "SCORE: N" line. ok is false if absent.
func extractScore(s string) (score int, body string, ok bool) {
	m := scoreRe.FindAllStringSubmatchIndex(s, -1)
	if len(m) == 0 {
		return 0, s, false
	}
	last := m[len(m)-1]
	n, _ := strconv.Atoi(s[last[2]:last[3]])
	if n > 100 {
		n = 100
	}
	return n, strings.TrimSpace(s[:last[0]]), true
}
