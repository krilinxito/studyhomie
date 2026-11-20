// Package openaicompat talks to any service that speaks the OpenAI Chat Completions
// API: OpenRouter, Groq, DeepSeek, Mistral, xAI, Together, and local servers such as
// Ollama or LM Studio. The API keeps no conversation, so this backend does.
package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/provider/httpx"
)

// Options describe one service.
type Options struct {
	Name         string   // provider id shown in the UI
	BaseURL      string   // ends before /chat/completions, e.g. https://api.groq.com/openai/v1
	KeyEnvs      []string // where the key may be; none for local servers
	DefaultModel string
	WebPlugin    bool // OpenRouter: web search through its "web" plugin
}

type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type part struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type Client struct {
	opt Options
	mem *httpx.Memory[message]
}

func New(o Options) *Client {
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	return &Client{opt: o, mem: httpx.NewMemory[message](o.Name)}
}

func (c *Client) Name() string { return c.opt.Name }

func (c *Client) Complete(ctx context.Context, req provider.Request, onText func(string)) (provider.Response, error) {
	if c.opt.BaseURL == "" {
		return provider.Response{}, errors.New("falta base_url en studyhomie.toml (por ejemplo http://localhost:11434/v1)")
	}
	id, msgs, err := c.mem.Begin(req.SessionID)
	if err != nil {
		return provider.Response{}, err
	}
	images, extra, err := httpx.Attachments(req.Files)
	if err != nil {
		return provider.Response{}, err
	}
	var user any = req.Prompt + extra
	if len(images) > 0 {
		parts := []part{{Type: "text", Text: req.Prompt + extra}}
		for _, im := range images {
			parts = append(parts, part{Type: "image_url", ImageURL: &imageURL{URL: im.DataURL()}})
		}
		user = parts
	}
	msgs = append(msgs, message{Role: "user", Content: user})

	model := req.Model
	if model == "" {
		model = c.opt.DefaultModel
	}
	if model == "" {
		return provider.Response{}, errors.New("falta el modelo: define model en studyhomie.toml")
	}
	send := msgs
	if req.System != "" {
		send = append([]message{{Role: "system", Content: req.System}}, msgs...)
	}
	body := map[string]any{
		"model":          model,
		"messages":       send,
		"stream":         true,
		"stream_options": map[string]bool{"include_usage": true},
	}
	if c.opt.WebPlugin && wantsSearch(req.Tools) {
		body["plugins"] = []map[string]string{{"id": "web"}}
	}
	headers := map[string]string{}
	if key := httpx.Key(c.opt.KeyEnvs...); key != "" {
		headers["Authorization"] = "Bearer " + key
	} else if len(c.opt.KeyEnvs) > 0 {
		return provider.Response{}, fmt.Errorf("falta la clave: define la variable de entorno %s", c.opt.KeyEnvs[0])
	}

	var text strings.Builder
	var usage provider.Usage
	err = httpx.PostStream(ctx, c.opt.BaseURL+"/chat/completions", headers, body, func(data []byte) error {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				Prompt     int `json:"prompt_tokens"`
				Completion int `json:"completion_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &chunk); err != nil {
			return nil // a keep-alive or a line this client does not know
		}
		if chunk.Error != nil {
			return errors.New(chunk.Error.Message)
		}
		for _, ch := range chunk.Choices {
			if s := ch.Delta.Content; s != "" {
				text.WriteString(s)
				if onText != nil {
					onText(s)
				}
			}
		}
		if chunk.Usage != nil {
			usage = provider.Usage{InputTokens: chunk.Usage.Prompt, OutputTokens: chunk.Usage.Completion}
		}
		return nil
	})
	if err != nil {
		return provider.Response{}, err
	}
	c.mem.Save(id, append(msgs, message{Role: "assistant", Content: text.String()}))
	return provider.Response{Text: text.String(), SessionID: id, Usage: usage}, nil
}

func wantsSearch(tools []string) bool {
	for _, t := range tools {
		if t == "WebSearch" || t == "WebFetch" {
			return true
		}
	}
	return false
}

// Models lists the models the service offers.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	headers := map[string]string{}
	if key := httpx.Key(c.opt.KeyEnvs...); key != "" {
		headers["Authorization"] = "Bearer " + key
	}
	return httpx.ListModels(ctx, c.opt.BaseURL+"/models", headers)
}
