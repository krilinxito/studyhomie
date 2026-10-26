// Package session runs the Preparation → Test → Feynman cycle.
package session

import (
	"context"

	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/provider"
)

// Convo is one conversation within a single phase. It tracks context usage and,
// when a turn crosses the threshold, replaces the history with a short summary
// so the context never grows without bound.
type Convo struct {
	P        provider.Provider
	System   string
	Tools    []string
	Files    []string // local files the model must look at (first turn only is enough: sessions keep them)
	Model    string
	Language string

	Window    int     // context window in tokens
	CompactAt float64 // fraction of Window that triggers compaction

	Compactions int // how many times the history was summarized

	sessionID string
	seed      string // summary carried into the next fresh context
}

func (c *Convo) threshold() int {
	if c.Window <= 0 || c.CompactAt <= 0 {
		return 0 // compaction disabled
	}
	return int(float64(c.Window) * c.CompactAt)
}

// Say sends one turn and compacts afterwards if the context grew too large.
func (c *Convo) Say(ctx context.Context, prompt string, onText func(string)) (provider.Response, error) {
	if c.seed != "" {
		prompt = "Summary of the conversation so far:\n" + c.seed + "\n\n" + prompt
		c.seed = ""
	}
	resp, err := c.P.Complete(ctx, provider.Request{
		System: c.System, Prompt: prompt, Tools: c.Tools, Files: c.Files, Model: c.Model, SessionID: c.sessionID,
	}, onText)
	if err != nil {
		return resp, err
	}
	c.sessionID = resp.SessionID
	if t := c.threshold(); t > 0 && resp.Usage.Total() > t {
		if err := c.compact(ctx); err != nil {
			return resp, err
		}
	}
	return resp, nil
}

// compact asks the model for a summary of the current session and starts the
// next turn from a fresh session seeded with it.
func (c *Convo) compact(ctx context.Context) error {
	resp, err := c.P.Complete(ctx, provider.Request{
		System: c.System, Prompt: prompts.Get("compact", c.Language), Model: c.Model, SessionID: c.sessionID,
	}, nil)
	if err != nil {
		return err
	}
	c.seed = resp.Text
	c.sessionID = ""
	c.Compactions++
	return nil
}
