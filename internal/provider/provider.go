// Package provider defines the interface every AI backend implements.
package provider

import "context"

// Request is a single model call. Backends map it onto their own transport.
type Request struct {
	System string // system prompt
	Prompt string // user prompt for this turn
	// Tools lists the capabilities the model may use (e.g. "WebSearch", "WebFetch").
	// Backends ignore names they do not support.
	Tools []string
	// SessionID continues a prior conversation of the same phase. Empty starts fresh.
	SessionID string
	Model     string
	// Files are local files (PDF, images...) the model must look at. The backend
	// decides how to deliver them.
	Files []string
}

// Usage reports token consumption for a call.
type Usage struct {
	InputTokens  int
	OutputTokens int
	CacheTokens  int
}

// Total returns the tokens that count against the context window.
func (u Usage) Total() int { return u.InputTokens + u.OutputTokens + u.CacheTokens }

// Response is the final result of a call.
type Response struct {
	Text      string
	SessionID string
	Usage     Usage
}

// Provider is implemented by claudecli, anthropicapi and any future backend.
type Provider interface {
	Name() string
	// Complete runs the request and returns the final answer. If onText is not nil
	// it receives text chunks as they are produced.
	Complete(ctx context.Context, req Request, onText func(string)) (Response, error)
}
