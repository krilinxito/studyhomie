// Package config loads studyhomie.toml.
package config

import (
	"errors"
	"os"

	"github.com/BurntSushi/toml"
)

const FileName = "studyhomie.toml"

type Config struct {
	Provider      string   `toml:"provider"` // claude-cli | anthropic-api
	Model         string   `toml:"model"`    // empty = the provider's default
	Port          int      `toml:"port"`     // local web UI
	Language      string   `toml:"language"`
	Vault         string   `toml:"vault"`
	Interests     []string `toml:"interests"`      // fields the Daily draws from
	ContextWindow int      `toml:"context_window"` // tokens
	CompactAt     float64  `toml:"compact_at"`     // fraction of the window that triggers compaction
}

func Default() Config {
	return Config{
		Provider:      "claude-cli",
		Port:          8765,
		Language:      "español",
		Vault:         "vault",
		ContextWindow: 200000,
		CompactAt:     0.6,
		Interests: []string{
			"Filosofía", "Historia", "Ciencias sociales", "Matemática", "Física",
			"Química", "Biología", "Medicina", "Ciencias de la computación",
		},
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
