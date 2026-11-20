// Package geminiapi talks to Google's Gemini API, with Google Search grounding for
// web search. The API keeps no conversation, so this backend does.
package geminiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/provider/httpx"
)

// BaseURL is the Gemini API; tests point it elsewhere.
var BaseURL = "https://generativelanguage.googleapis.com/v1beta"

// KeyEnvs hold the API key, in order of preference.
var KeyEnvs = []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}

type part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inline_data,omitempty"`
}

type inlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

type turn struct {
	Role  string `json:"role"` // user | model
	Parts []part `json:"parts"`
}

type Client struct {
	model string
	mem   *httpx.Memory[turn]
}

// New builds a backend; defaultModel is used when a request names none.
func New(defaultModel string) *Client {
	return &Client{model: defaultModel, mem: httpx.NewMemory[turn]("gemini")}
}

func (c *Client) Name() string { return "gemini" }

func (c *Client) Complete(ctx context.Context, req provider.Request, onText func(string)) (provider.Response, error) {
	key := httpx.Key(KeyEnvs...)
	if key == "" {
		return provider.Response{}, fmt.Errorf("falta la clave: define la variable de entorno %s", KeyEnvs[0])
	}
	model := req.Model
	if model == "" {
		model = c.model
	}
	if model == "" {
		return provider.Response{}, errors.New("falta el modelo: elígelo en Ajustes o define model en studyhomie.toml")
	}
	id, turns, err := c.mem.Begin(req.SessionID)
	if err != nil {
		return provider.Response{}, err
	}
	images, extra, err := httpx.Attachments(req.Files)
	if err != nil {
		return provider.Response{}, err
	}
	var parts []part
	for _, im := range images {
		parts = append(parts, part{InlineData: &inlineData{MimeType: im.MIME, Data: im.Base64()}})
	}
	parts = append(parts, part{Text: req.Prompt + extra})
	turns = append(turns, turn{Role: "user", Parts: parts})

	body := map[string]any{"contents": turns}
	if req.System != "" {
		body["systemInstruction"] = map[string]any{"parts": []part{{Text: req.System}}}
	}
	for _, t := range req.Tools {
		if t == "WebSearch" {
			body["tools"] = []map[string]any{{"google_search": map[string]any{}}}
		}
	}

	var text strings.Builder
	var usage provider.Usage
	url := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", BaseURL, strings.TrimPrefix(model, "models/"))
	err = httpx.PostStream(ctx, url, map[string]string{"x-goog-api-key": key}, body, func(data []byte) error {
		var chunk struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text    string `json:"text"`
						Thought bool   `json:"thought"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			UsageMetadata *struct {
				Prompt     int `json:"promptTokenCount"`
				Candidates int `json:"candidatesTokenCount"`
			} `json:"usageMetadata"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &chunk); err != nil {
			return nil
		}
		if chunk.Error != nil {
			return errors.New(chunk.Error.Message)
		}
		for _, cand := range chunk.Candidates {
			for _, p := range cand.Content.Parts {
				if p.Thought || p.Text == "" {
					continue // the model's own reasoning is not part of the answer
				}
				text.WriteString(p.Text)
				if onText != nil {
					onText(p.Text)
				}
			}
			if cand.FinishReason == "SAFETY" {
				return errors.New("el modelo bloqueó la respuesta por sus filtros de seguridad")
			}
		}
		if chunk.UsageMetadata != nil {
			usage = provider.Usage{InputTokens: chunk.UsageMetadata.Prompt, OutputTokens: chunk.UsageMetadata.Candidates}
		}
		return nil
	})
	if err != nil {
		return provider.Response{}, err
	}
	c.mem.Save(id, append(turns, turn{Role: "model", Parts: []part{{Text: text.String()}}}))
	return provider.Response{Text: text.String(), SessionID: id, Usage: usage}, nil
}

// Models lists the models that can write text.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BaseURL+"/models?pageSize=1000", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-goog-api-key", httpx.Key(KeyEnvs...))
	resp, err := httpx.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, &httpx.StatusError{Code: resp.StatusCode, Body: resp.Status}
	}
	var list struct {
		Models []struct {
			Name    string   `json:"name"`
			Methods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	var out []string
	for _, m := range list.Models {
		for _, g := range m.Methods {
			if g == "generateContent" {
				out = append(out, strings.TrimPrefix(m.Name, "models/"))
				break
			}
		}
	}
	sort.Strings(out)
	return out, nil
}
