// Package openaiapi talks to OpenAI through the Responses API, which keeps the
// conversation on OpenAI's side (previous_response_id) and has its own web search.
package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/provider/httpx"
)

// BaseURL is OpenAI's API; tests point it elsewhere.
var BaseURL = "https://api.openai.com/v1"

// KeyEnv holds the API key.
const KeyEnv = "OPENAI_API_KEY"

type Client struct{ model string }

// New builds a backend; defaultModel is used when a request names none.
func New(defaultModel string) *Client { return &Client{model: defaultModel} }

func (c *Client) Name() string { return "openai" }

type content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type item struct {
	Role    string    `json:"role"`
	Content []content `json:"content"`
}

func (c *Client) Complete(ctx context.Context, req provider.Request, onText func(string)) (provider.Response, error) {
	key := httpx.Key(KeyEnv)
	if key == "" {
		return provider.Response{}, fmt.Errorf("falta la clave: define la variable de entorno %s", KeyEnv)
	}
	model := req.Model
	if model == "" {
		model = c.model
	}
	if model == "" {
		return provider.Response{}, errors.New("falta el modelo: elígelo en Ajustes o define model en studyhomie.toml")
	}
	images, extra, err := httpx.Attachments(req.Files)
	if err != nil {
		return provider.Response{}, err
	}
	parts := []content{{Type: "input_text", Text: req.Prompt + extra}}
	for _, im := range images {
		parts = append(parts, content{Type: "input_image", ImageURL: im.DataURL()})
	}
	body := map[string]any{
		"model":  model,
		"input":  []item{{Role: "user", Content: parts}},
		"stream": true,
		"store":  true, // so the next turn can continue from this one
	}
	if req.System != "" {
		body["instructions"] = req.System // instructions do not carry over between turns: sent every time
	}
	if req.SessionID != "" {
		body["previous_response_id"] = req.SessionID
	}
	for _, t := range req.Tools {
		if t == "WebSearch" {
			body["tools"] = []map[string]string{{"type": "web_search"}}
		}
	}

	var text strings.Builder
	var out provider.Response
	err = httpx.PostStream(ctx, BaseURL+"/responses", map[string]string{"Authorization": "Bearer " + key}, body, func(data []byte) error {
		var ev struct {
			Type     string `json:"type"`
			Delta    string `json:"delta"`
			Response struct {
				ID    string `json:"id"`
				Usage struct {
					Input  int `json:"input_tokens"`
					Output int `json:"output_tokens"`
				} `json:"usage"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			return nil
		}
		switch ev.Type {
		case "response.output_text.delta":
			text.WriteString(ev.Delta)
			if onText != nil {
				onText(ev.Delta)
			}
		case "response.completed", "response.incomplete":
			out.SessionID = ev.Response.ID
			out.Usage = provider.Usage{InputTokens: ev.Response.Usage.Input, OutputTokens: ev.Response.Usage.Output}
		case "response.failed":
			if ev.Response.Error != nil {
				return errors.New(ev.Response.Error.Message)
			}
			return errors.New("la respuesta falló")
		case "error":
			return errors.New(ev.Message)
		}
		return nil
	})
	if err != nil {
		return provider.Response{}, err
	}
	out.Text = text.String()
	return out, nil
}

// Models lists the models the key can use.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	return httpx.ListModels(ctx, BaseURL+"/models", map[string]string{"Authorization": "Bearer " + httpx.Key(KeyEnv)})
}
