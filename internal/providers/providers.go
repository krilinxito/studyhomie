// Package providers builds the AI backend selected in the config.
package providers

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/provider/anthropicapi"
	"github.com/krilinxito/studyhomie/internal/provider/claudecli"
)

func New(cfg config.Config) (provider.Provider, error) {
	switch cfg.Provider {
	case "claude-cli", "":
		return claudecli.New(), nil
	case "anthropic-api":
		return anthropicapi.New(), nil
	}
	return nil, fmt.Errorf("proveedor %q no soportado (usa claude-cli o anthropic-api)", cfg.Provider)
}

// Check returns a message when the selected backend is clearly not ready to use,
// so the problem is shown at startup instead of in the middle of a study session.
// An empty string means nothing obvious is wrong.
func Check(cfg config.Config) string {
	switch cfg.Provider {
	case "claude-cli", "":
		if _, err := exec.LookPath("claude"); err != nil {
			return "No encuentro el comando `claude`. Instala Claude Code y ejecuta `claude` una vez para iniciar sesión, " +
				"o cambia a `provider = \"anthropic-api\"` en studyhomie.toml y define ANTHROPIC_API_KEY."
		}
	case "anthropic-api":
		if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("ANTHROPIC_AUTH_TOKEN") == "" {
			return "Falta la variable de entorno ANTHROPIC_API_KEY con tu clave de la API de Anthropic."
		}
	}
	return ""
}
