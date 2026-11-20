// Package config loads studyhomie.toml.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const FileName = "studyhomie.toml"

type Config struct {
	Provider      string   `toml:"provider"`       // see providers.Catalog: claude-cli, codex, agy, anthropic-api, openai, gemini, openrouter, ollama…
	BaseURL       string   `toml:"base_url"`       // local servers and other OpenAI-compatible services
	Model         string   `toml:"model"`          // empty = the provider's default
	QuickModel    string   `toml:"quick_model"`    // for short answers (base concepts, doubts); empty = Model
	Port          int      `toml:"port"`           // local web UI
	Language      string   `toml:"language"`       // interface and AI language: es, en, fr, zh, pt, or any language name for the AI alone
	Vault         string   `toml:"vault"`          // the vault in use
	Vaults        []string `toml:"vaults"`         // every vault you added, to switch between them
	Interests     []string `toml:"interests"`      // fields the Daily draws from
	ContextWindow int      `toml:"context_window"` // tokens
	CompactAt     float64  `toml:"compact_at"`     // fraction of the window that triggers compaction
	Depth         int      `toml:"depth"`          // levels of prerequisite concept notes a guide gets by default (0-4)
	Parallel      int      `toml:"parallel"`       // concept notes written at the same time
	Iceberg       bool     `toml:"iceberg"`        // map the prerequisites down to the basics even when writing fewer
	Updates       string   `toml:"updates"`        // new versions: "ask" (default), "auto" or "never"
	PassScore     int      `toml:"pass_score"`     // mastery (0-100) a roadmap topic needs to unlock the next one
	Timeout       int      `toml:"timeout"`        // minutes one AI call may take before it is cut (three times that when it searches the web); 0 = no limit
}

// DefaultInterests are the fields the Daily draws from when none are configured.
func DefaultInterests() []string {
	return []string{
		"Filosofía", "Historia", "Historia del arte", "Ciencias sociales", "Matemática", "Física",
		"Química", "Biología", "Medicina", "Ciencias de la computación",
	}
}

func Default() Config {
	return Config{
		Provider:      "claude-cli",
		Port:          8765,
		Language:      "", // the web app picks the browser's language the first time
		Vault:         "vault",
		ContextWindow: 200000,
		CompactAt:     0.6,
		Depth:         1,
		Parallel:      3,
		Iceberg:       true,
		Updates:       "ask",
		PassScore:     70,
		Timeout:       5,
		Interests:     DefaultInterests(),
	}
}

// Load reads the file at path, filling gaps with defaults. A missing file yields defaults.
func Load(path string) (Config, error) {
	c := Default()
	_, err := toml.DecodeFile(path, &c)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return c, err
	}
	return c, nil
}

// Write saves the config to path.
func Write(path string, c Config) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}

// VaultList is every known vault, the one in use first, without repeats (paths compared cleaned
// and case-insensitively, as Windows does).
func (c Config) VaultList() []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range append([]string{c.Vault}, c.Vaults...) {
		key := strings.ToLower(filepath.Clean(v))
		if strings.TrimSpace(v) == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}
