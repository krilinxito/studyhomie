package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/config"
)

func TestInitWithFlagsAndCheck(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	if err := initProject([]string{"--provider", "no-existe"}, &out); err == nil {
		t.Fatal("unknown provider accepted")
	}
	if err := initProject([]string{"--provider", "ollama", "--model", "qwen3:14b", "--vault", "notas"}, &out); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.FileName)
	if err != nil || cfg.Provider != "ollama" || cfg.Model != "qwen3:14b" || cfg.Vault != "notas" {
		t.Fatalf("config: %+v %v", cfg, err)
	}
	if _, err := os.Stat("notas/guias"); err != nil {
		t.Error("init must create the vault")
	}
	if err := initProject(nil, &out); err == nil {
		t.Error("init without flags on an existing file must refuse")
	}
	// flags on an existing file change only those values
	if err := initProject([]string{"--port", "9001"}, &out); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = config.Load(config.FileName); cfg.Port != 9001 || cfg.Provider != "ollama" || cfg.Model != "qwen3:14b" {
		t.Errorf("update must keep the rest: %+v", cfg)
	}
	// a provider that cannot answer: check exits with an error and says why
	if err := initProject([]string{"--provider", "openai-compatible", "--model", "m"}, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := check(context.Background(), nil, &out); err != errNotReady || !strings.Contains(out.String(), "base_url") {
		t.Errorf("check = %v\n%s", err, out.String())
	}
}
