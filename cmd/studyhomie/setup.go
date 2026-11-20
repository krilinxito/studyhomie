package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/providers"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// initProject creates studyhomie.toml and the vault in this folder. With flags it sets those
// values without asking anything, and on an existing file it changes only them: an installer
// (a person or their coding agent) can configure everything in one line.
func initProject(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(out)
	prov := fs.String("provider", "", "la IA: "+strings.Join(providerIDs(), ", "))
	model := fs.String("model", "", "modelo (vacío: el del proveedor)")
	quick := fs.String("quick-model", "", "modelo para respuestas cortas")
	baseURL := fs.String("base-url", "", "URL de un servidor local o compatible con OpenAI")
	vaultDir := fs.String("vault", "", "carpeta del vault")
	port := fs.Int("port", 0, "puerto de la interfaz web")
	if err := fs.Parse(args); err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	cfg := config.Default()
	exists := false
	if _, err := os.Stat(config.FileName); err == nil {
		exists = true
		if len(set) == 0 {
			return fmt.Errorf("%s ya existe; para cambiar algo usa flags, por ejemplo: studyhomie init --provider codex", config.FileName)
		}
		if cfg, err = config.Load(config.FileName); err != nil {
			return err
		}
	}
	if set["provider"] {
		if _, ok := providers.Find(*prov); !ok {
			return fmt.Errorf("proveedor %q no existe; opciones: %s", *prov, strings.Join(providerIDs(), ", "))
		}
		cfg.Provider = *prov
	}
	if set["model"] {
		cfg.Model = *model
	}
	if set["quick-model"] {
		cfg.QuickModel = *quick
	}
	if set["base-url"] {
		cfg.BaseURL = *baseURL
	}
	if set["vault"] {
		cfg.Vault = *vaultDir
		cfg.Vaults = cfg.VaultList()
	}
	if set["port"] {
		cfg.Port = *port
	}
	if _, err := vault.Open(cfg.Vault); err != nil {
		return err
	}
	if err := config.Write(config.FileName, cfg); err != nil {
		return err
	}
	verb := "Creado"
	if exists {
		verb = "Actualizado"
	}
	fmt.Fprintf(out, "%s %s (IA: %s) y vault en %s\n", verb, config.FileName, cfg.Provider, absOr(cfg.Vault))
	if msg := providers.Check(cfg); msg != "" {
		fmt.Fprintln(out, "Falta algo para usar la IA:", msg)
	} else {
		fmt.Fprintln(out, "Siguiente: studyhomie check --live  (prueba la IA)  y  studyhomie  (abre la interfaz)")
	}
	return nil
}

func providerIDs() []string {
	var ids []string
	for _, in := range providers.Catalog {
		ids = append(ids, in.ID)
	}
	return ids
}

func absOr(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// errNotReady makes `check` exit with status 1, for scripts and agents.
var errNotReady = errors.New("la IA configurada no está lista")

// check reports what an installer needs to know: the config in use, the vault, and which AIs
// could answer now. With --live it asks the configured one for a one-word reply.
func check(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(out)
	live := fs.Bool("live", false, "mandar un mensaje de prueba a la IA configurada")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(config.FileName)
	if err != nil {
		return err
	}
	file := absOr(config.FileName)
	if _, err := os.Stat(config.FileName); err != nil {
		file += " (no existe: se usan los valores por defecto; créalo con studyhomie init)"
	}
	fmt.Fprintf(out, "StudyHomie %s\nConfiguración: %s\nVault: %s\nInterfaz: http://127.0.0.1:%d\n\n", version, file, absOr(cfg.Vault), cfg.Port)

	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	fmt.Fprintln(out, "IAs disponibles (la marcada con > es la configurada):")
	for _, st := range providers.Statuses(sctx, cfg) {
		mark := "  "
		if st.ID == cfg.Provider || cfg.Provider == "" && st.ID == "claude-cli" {
			mark = "> "
		}
		state := "lista"
		if !st.Ready {
			state = st.Reason
		}
		fmt.Fprintf(out, "%s%-18s %-26s %s\n", mark, st.ID, st.Name, state)
	}
	fmt.Fprintln(out)
	if msg := providers.Check(cfg); msg != "" {
		fmt.Fprintln(out, "No está lista:", msg)
		return errNotReady
	}
	if !*live {
		fmt.Fprintf(out, "La IA configurada (%s) parece lista. Pruébala de verdad con: studyhomie check --live\n", cfg.Provider)
		return nil
	}
	p, err := providers.New(cfg)
	if err != nil {
		return err
	}
	lctx, lcancel := context.WithTimeout(ctx, 2*time.Minute)
	defer lcancel()
	start := time.Now()
	resp, err := p.Complete(lctx, provider.Request{Prompt: "Responde solo con la palabra: listo", Model: cfg.Model}, nil)
	if err != nil {
		fmt.Fprintln(out, "La prueba falló:", err)
		return errNotReady
	}
	fmt.Fprintf(out, "Funciona: %s respondió %q en %.1f s.\n", cfg.Provider, strings.TrimSpace(resp.Text), time.Since(start).Seconds())
	return nil
}
