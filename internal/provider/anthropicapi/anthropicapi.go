// Package anthropicapi talks to the Claude API with the user's own API key. The API
// is stateless, so this backend keeps each phase's conversation in memory.
package anthropicapi

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/krilinxito/studyhomie/internal/provider"
)

// DefaultModel is used when the config does not name one.
const DefaultModel = "claude-opus-5-5"

const (
	maxTokens   = 32000
	maxPauses   = 8 // server tools may pause a long turn; we resume it this many times at most
	maxToolUses = 8
)

type API struct {
	client anthropic.Client

	mu      sync.Mutex
	history map[string][]anthropic.MessageParam
	counter int
}

// New builds a backend. By default the key comes from ANTHROPIC_API_KEY (or an
// `ant auth login` profile); extra options let tests point at a fake server.
func New(opts ...option.RequestOption) *API {
	return &API{client: anthropic.NewClient(opts...), history: map[string][]anthropic.MessageParam{}}
}

func (a *API) Name() string { return "anthropic-api" }

// tools maps the provider-neutral tool names to the API's server-side tools.
func tools(names []string) []anthropic.ToolUnionParam {
	var out []anthropic.ToolUnionParam
	for _, n := range names {
		switch n {
		case "WebSearch":
			out = append(out, anthropic.ToolUnionParam{OfWebSearchTool20260209: &anthropic.WebSearchTool20260209Param{
				MaxUses: anthropic.Int(maxToolUses),
			}})
		case "WebFetch":
			out = append(out, anthropic.ToolUnionParam{OfWebFetchTool20260209: &anthropic.WebFetchTool20260209Param{
				MaxUses: anthropic.Int(maxToolUses),
			}})
		}
	}
	return out
}

// userMessage builds the user turn: attached files first, then the prompt.
func userMessage(prompt string, files []string) (anthropic.MessageParam, error) {
	var blocks []anthropic.ContentBlockParamUnion
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return anthropic.MessageParam{}, err
		}
		switch strings.ToLower(filepath.Ext(f)) {
		case ".png":
			blocks = append(blocks, anthropic.NewImageBlockBase64("image/png", base64.StdEncoding.EncodeToString(data)))
		case ".jpg", ".jpeg":
			blocks = append(blocks, anthropic.NewImageBlockBase64("image/jpeg", base64.StdEncoding.EncodeToString(data)))
		case ".webp":
			blocks = append(blocks, anthropic.NewImageBlockBase64("image/webp", base64.StdEncoding.EncodeToString(data)))
		case ".gif":
			blocks = append(blocks, anthropic.NewImageBlockBase64("image/gif", base64.StdEncoding.EncodeToString(data)))
		case ".pdf":
			blocks = append(blocks, anthropic.NewDocumentBlock(anthropic.Base64PDFSourceParam{Data: base64.StdEncoding.EncodeToString(data)}))
		default:
			blocks = append(blocks, anthropic.NewTextBlock("File "+filepath.Base(f)+":\n"+string(data)))
		}
	}
	blocks = append(blocks, anthropic.NewTextBlock(prompt))
	return anthropic.NewUserMessage(blocks...), nil
}

func textOf(m anthropic.Message) string {
	var b strings.Builder
	for _, c := range m.Content {
		if t, ok := c.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

func usageOf(u anthropic.Usage) provider.Usage {
	return provider.Usage{
		InputTokens:  int(u.InputTokens),
		OutputTokens: int(u.OutputTokens),
		CacheTokens:  int(u.CacheCreationInputTokens + u.CacheReadInputTokens),
	}
}

func (a *API) Complete(ctx context.Context, req provider.Request, onText func(string)) (provider.Response, error) {
	id, msgs, err := a.begin(req.SessionID)
	if err != nil {
		return provider.Response{}, err
	}
	um, err := userMessage(req.Prompt, req.Files)
	if err != nil {
		return provider.Response{}, err
	}
	msgs = append(msgs, um)

	model := req.Model
	if model == "" {
		model = DefaultModel
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: maxTokens,
		Messages:  msgs,
		Tools:     tools(req.Tools),
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}

	var text strings.Builder
	var last anthropic.Message
	for pause := 0; ; pause++ {
		last, err = a.stream(ctx, params, func(s string) {
			text.WriteString(s)
			if onText != nil {
				onText(s)
			}
		})
		if err != nil {
			return provider.Response{}, hint(err)
		}
		params.Messages = append(params.Messages, last.ToParam())
		if last.StopReason != anthropic.StopReasonPauseTurn {
			break
		}
		if pause >= maxPauses {
			return provider.Response{}, errors.New("the model kept pausing its turn; giving up")
		}
	}

	switch last.StopReason {
	case anthropic.StopReasonRefusal:
		return provider.Response{}, fmt.Errorf("el modelo rechazó la solicitud (%s)", last.StopDetails.Category)
	case anthropic.StopReasonMaxTokens:
		return provider.Response{}, errors.New("la respuesta se cortó por longitud máxima; intenta un tema más acotado")
	}

	a.mu.Lock()
	a.history[id] = params.Messages
	a.mu.Unlock()
	return provider.Response{Text: text.String(), SessionID: id, Usage: usageOf(last.Usage)}, nil
}

// begin returns the session id and a copy of its history, or opens a new session.
func (a *API) begin(id string) (string, []anthropic.MessageParam, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id == "" {
		a.counter++
		return fmt.Sprintf("api-%d", a.counter), nil, nil
	}
	h, ok := a.history[id]
	if !ok {
		return "", nil, fmt.Errorf("unknown session %q", id)
	}
	return id, append([]anthropic.MessageParam(nil), h...), nil
}

func (a *API) stream(ctx context.Context, params anthropic.MessageNewParams, onText func(string)) (anthropic.Message, error) {
	s := a.client.Messages.NewStreaming(ctx, params)
	defer s.Close()
	var msg anthropic.Message
	for s.Next() {
		ev := s.Current()
		if err := msg.Accumulate(ev); err != nil {
			return msg, err
		}
		if d, ok := ev.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
			if t, ok := d.Delta.AsAny().(anthropic.TextDelta); ok {
				onText(t.Text)
			}
		}
	}
	return msg, s.Err()
}

// hint adds what to do when the key is missing or wrong.
func hint(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == 401 {
		return fmt.Errorf("%w\n\nConfigura tu clave: define la variable de entorno ANTHROPIC_API_KEY", err)
	}
	return err
}
