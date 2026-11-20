// Package providers is the catalogue of AI backends and builds the one the config selects.
package providers

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/provider/agycli"
	"github.com/krilinxito/studyhomie/internal/provider/anthropicapi"
	"github.com/krilinxito/studyhomie/internal/provider/claudecli"
	"github.com/krilinxito/studyhomie/internal/provider/codexcli"
	"github.com/krilinxito/studyhomie/internal/provider/geminiapi"
	"github.com/krilinxito/studyhomie/internal/provider/httpx"
	"github.com/krilinxito/studyhomie/internal/provider/openaiapi"
	"github.com/krilinxito/studyhomie/internal/provider/openaicompat"
)

// Info describes one backend for the settings page and the startup check.
type Info struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`              // cli | api | local
	Bin          string   `json:"bin,omitempty"`     // cli: the executable
	KeyEnvs      []string `json:"keyEnvs,omitempty"` // api: where the key may be
	BaseURL      string   `json:"baseURL,omitempty"` // OpenAI-compatible services
	EditURL      bool     `json:"editURL,omitempty"` // the URL is the user's to set (local servers, custom)
	DefaultModel string   `json:"defaultModel,omitempty"`
	Search       bool     `json:"search"`                 // has its own web search: guides get verified resources
	Experimental bool     `json:"experimental,omitempty"` // works, with rough edges
	Note         string   `json:"note,omitempty"`
}

// Catalog lists every backend, in the order the settings page shows them.
var Catalog = []Info{
	{ID: "claude-cli", Name: "Claude Code", Kind: "cli", Bin: "claude", Search: true,
		Note: "Usa tu plan de Claude. Instálalo con `irm https://claude.ai/install.ps1 | iex` y ejecuta `claude` una vez."},
	{ID: "codex", Name: "Codex CLI (OpenAI)", Kind: "cli", Bin: "codex", Search: true,
		Note: "Usa tu cuenta de ChatGPT o una clave de OpenAI. Instálalo con `npm install -g @openai/codex` y ejecuta `codex login`."},
	{ID: "agy", Name: "Antigravity CLI (Google)", Kind: "cli", Bin: "agy", Search: true, Experimental: true,
		Note: "Usa tu cuenta de Google. Ejecuta `agy` una vez para iniciar sesión. Experimental: es un agente con herramientas propias."},
	{ID: "anthropic-api", Name: "API de Anthropic", Kind: "api", KeyEnvs: []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}, DefaultModel: anthropicapi.DefaultModel, Search: true},
	{ID: "openai", Name: "API de OpenAI", Kind: "api", KeyEnvs: []string{openaiapi.KeyEnv}, Search: true},
	{ID: "gemini", Name: "API de Gemini (Google)", Kind: "api", KeyEnvs: geminiapi.KeyEnvs, Search: true},
	{ID: "openrouter", Name: "OpenRouter", Kind: "api", KeyEnvs: []string{"OPENROUTER_API_KEY"}, BaseURL: "https://openrouter.ai/api/v1", Search: true,
		Note: "Un solo acceso a cientos de modelos (Gemini, Llama, Mistral, DeepSeek, Qwen…)."},
	{ID: "groq", Name: "Groq", Kind: "api", KeyEnvs: []string{"GROQ_API_KEY"}, BaseURL: "https://api.groq.com/openai/v1"},
	{ID: "deepseek", Name: "DeepSeek", Kind: "api", KeyEnvs: []string{"DEEPSEEK_API_KEY"}, BaseURL: "https://api.deepseek.com/v1"},
	{ID: "mistral", Name: "Mistral", Kind: "api", KeyEnvs: []string{"MISTRAL_API_KEY"}, BaseURL: "https://api.mistral.ai/v1"},
	{ID: "xai", Name: "xAI (Grok)", Kind: "api", KeyEnvs: []string{"XAI_API_KEY"}, BaseURL: "https://api.x.ai/v1"},
	{ID: "together", Name: "Together AI", Kind: "api", KeyEnvs: []string{"TOGETHER_API_KEY"}, BaseURL: "https://api.together.xyz/v1"},
	{ID: "ollama", Name: "Ollama (local)", Kind: "local", BaseURL: "http://localhost:11434/v1", EditURL: true,
		Note: "Modelos en tu PC. Los de 14B o más siguen mejor el formato de las guías."},
	{ID: "lmstudio", Name: "LM Studio (local)", Kind: "local", BaseURL: "http://localhost:1234/v1", EditURL: true},
	{ID: "openai-compatible", Name: "Otro compatible con OpenAI", Kind: "api", KeyEnvs: []string{"OPENAI_COMPAT_API_KEY"}, EditURL: true,
		Note: "Cualquier servicio con la API de Chat Completions de OpenAI: pon su URL. La clave, si hace falta, en OPENAI_COMPAT_API_KEY."},
}

// Find returns the catalogue entry of a backend id.
func Find(id string) (Info, bool) {
	if id == "" {
		id = "claude-cli"
	}
	for _, in := range Catalog {
		if in.ID == id {
			return in, true
		}
	}
	return Info{}, false
}

func New(cfg config.Config) (provider.Provider, error) {
	in, ok := Find(cfg.Provider)
	if !ok {
		return nil, fmt.Errorf("proveedor %q no soportado; opciones: %s", cfg.Provider, ids())
	}
	switch in.ID {
	case "claude-cli":
		return claudecli.New(), nil
	case "codex":
		return codexcli.New(), nil
	case "agy":
		return agycli.New(), nil
	case "anthropic-api":
		return anthropicapi.New(), nil
	case "openai":
		return openaiapi.New(cfg.Model), nil
	case "gemini":
		return geminiapi.New(cfg.Model), nil
	}
	url := in.BaseURL
	if in.EditURL && strings.TrimSpace(cfg.BaseURL) != "" {
		url = cfg.BaseURL
	}
	return openaicompat.New(openaicompat.Options{
		Name: in.ID, BaseURL: url, KeyEnvs: in.KeyEnvs, DefaultModel: cfg.Model, WebPlugin: in.ID == "openrouter",
	}), nil
}

func ids() string {
	var s []string
	for _, in := range Catalog {
		s = append(s, in.ID)
	}
	return strings.Join(s, ", ")
}

// Check returns a message when the selected backend is clearly not ready to use,
// so the problem is shown at startup instead of in the middle of a study session.
// An empty string means nothing obvious is wrong.
func Check(cfg config.Config) string {
	in, ok := Find(cfg.Provider)
	if !ok {
		return fmt.Sprintf("El proveedor %q no existe. Elige otro en Ajustes.", cfg.Provider)
	}
	switch in.Kind {
	case "cli":
		if _, err := exec.LookPath(in.Bin); err != nil {
			return fmt.Sprintf("No encuentro el comando `%s` (%s). %s", in.Bin, in.Name, in.Note)
		}
	case "api":
		if len(in.KeyEnvs) > 0 && httpx.Key(in.KeyEnvs...) == "" {
			return fmt.Sprintf("Falta la variable de entorno %s con tu clave de %s.", in.KeyEnvs[0], in.Name)
		}
	}
	if needsModel(in) && strings.TrimSpace(cfg.Model) == "" {
		return fmt.Sprintf("Elige un modelo de %s en Ajustes.", in.Name)
	}
	return ""
}

// needsModel: the CLIs and the Anthropic API have a default; the others need one named.
func needsModel(in Info) bool { return in.Kind != "cli" && in.DefaultModel == "" }

// Status is what the settings page shows next to each backend.
type Status struct {
	Info
	Ready  bool   `json:"ready"`
	Reason string `json:"reason,omitempty"`
}

// Statuses reports, for every backend, whether it could be used now: installed, key set, or
// (local servers) answering at their address. baseURL is the address the config gives a local server.
func Statuses(ctx context.Context, cfg config.Config) []Status {
	out := make([]Status, len(Catalog))
	var wg sync.WaitGroup
	for i, in := range Catalog {
		st := Status{Info: in, Ready: true}
		switch in.Kind {
		case "cli":
			if _, err := exec.LookPath(in.Bin); err != nil {
				st.Ready, st.Reason = false, "no instalado"
			}
		case "api":
			if len(in.KeyEnvs) > 0 && httpx.Key(in.KeyEnvs...) == "" && in.ID != "openai-compatible" {
				st.Ready, st.Reason = false, "falta "+in.KeyEnvs[0]
			}
		case "local":
			url := in.BaseURL
			if cfg.Provider == in.ID && strings.TrimSpace(cfg.BaseURL) != "" {
				url = cfg.BaseURL
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				pctx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
				defer cancel()
				if _, err := httpx.ListModels(pctx, strings.TrimRight(url, "/")+"/models", nil); err != nil {
					out[i].Ready, out[i].Reason = false, "no responde"
				}
			}()
		}
		out[i] = st
	}
	wg.Wait()
	return out
}

// claudeAliases are the model names Claude Code accepts besides full ids: each points to the latest of its family.
var claudeAliases = []string{"opus", "sonnet", "haiku"}

// AnthropicModelsURL is the Anthropic API's model list; tests point it elsewhere.
var AnthropicModelsURL = "https://api.anthropic.com/v1/models?limit=1000"

// Models lists the models a backend offers, when it can tell.
func Models(ctx context.Context, cfg config.Config) ([]string, error) {
	switch cfg.Provider {
	case "", "claude-cli":
		return claudeAliases, nil
	case "anthropic-api":
		headers := map[string]string{"anthropic-version": "2023-06-01"}
		if k := httpx.Key("ANTHROPIC_API_KEY"); k != "" {
			headers["x-api-key"] = k
		} else if t := httpx.Key("ANTHROPIC_AUTH_TOKEN"); t != "" {
			headers["Authorization"] = "Bearer " + t
		}
		return httpx.ListModels(ctx, AnthropicModelsURL, headers)
	}
	p, err := New(cfg)
	if err != nil {
		return nil, err
	}
	if l, ok := p.(interface {
		Models(context.Context) ([]string, error)
	}); ok {
		return l.Models(ctx)
	}
	return nil, nil
}
