package providers

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/config"
)

func TestNew(t *testing.T) {
	for _, name := range []string{"", "claude-cli", "anthropic-api"} {
		if p, err := New(config.Config{Provider: name}); err != nil || p == nil {
			t.Errorf("provider %q: %v", name, err)
		}
	}
	if _, err := New(config.Config{Provider: "otro"}); err == nil {
		t.Error("unknown provider must be an error")
	}
}

func TestCheckClaudeCLI(t *testing.T) {
	cfg := config.Config{Provider: "claude-cli"}
	t.Setenv("PATH", t.TempDir()) // nothing installed
	if msg := Check(cfg); !strings.Contains(msg, "claude") {
		t.Errorf("a missing claude command must be reported, got %q", msg)
	}

	dir := t.TempDir()
	name := "claude"
	if runtime.GOOS == "windows" {
		name = "claude.exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if msg := Check(cfg); msg != "" {
		t.Errorf("claude is installed, got %q", msg)
	}
}

func TestCheckAPIKey(t *testing.T) {
	cfg := config.Config{Provider: "anthropic-api"}
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	if msg := Check(cfg); !strings.Contains(msg, "ANTHROPIC_API_KEY") {
		t.Errorf("a missing key must be reported, got %q", msg)
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	if msg := Check(cfg); msg != "" {
		t.Errorf("key is set, got %q", msg)
	}
}

func TestEveryCatalogEntryBuilds(t *testing.T) {
	for _, in := range Catalog {
		p, err := New(config.Config{Provider: in.ID, Model: "m", BaseURL: "http://localhost:1/v1"})
		if err != nil || p == nil || p.Name() != in.ID {
			t.Errorf("%s: %v %v", in.ID, p, err)
		}
	}
}

func TestCheckAsksForAModelWhereThereIsNoDefault(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk")
	if msg := Check(config.Config{Provider: "openai"}); !strings.Contains(msg, "modelo") {
		t.Errorf("openai without a model: %q", msg)
	}
	if msg := Check(config.Config{Provider: "openai", Model: "m"}); msg != "" {
		t.Errorf("openai with key and model: %q", msg)
	}
	if msg := Check(config.Config{Provider: "ollama", Model: "qwen"}); msg != "" {
		t.Errorf("a local server needs no key: %q", msg)
	}
	t.Setenv("GROQ_API_KEY", "")
	if msg := Check(config.Config{Provider: "groq", Model: "m"}); !strings.Contains(msg, "GROQ_API_KEY") {
		t.Errorf("groq without key: %q", msg)
	}
}
