// Package codexcli runs OpenAI's Codex CLI in non-interactive mode (`codex exec --json`).
// The CLI handles sign-in (ChatGPT account or API key); StudyHomie never sees it. Each call
// runs read-only in an empty folder: Codex is a coding agent, and here it only answers.
package codexcli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/krilinxito/studyhomie/internal/provider"
)

type CLI struct {
	Bin string // executable; defaults to "codex"
}

func New() *CLI { return &CLI{Bin: "codex"} }

func (c *CLI) Name() string { return "codex" }

// prompt is what goes on stdin: the instructions open a new conversation, since exec has no system prompt flag.
func prompt(req provider.Request) string {
	p := req.Prompt
	if req.SessionID == "" && req.System != "" {
		p = "INSTRUCTIONS (follow them for the whole conversation):\n" + req.System + "\n\n---\n\n" + p
	}
	if len(req.Files) > 0 {
		var docs []string
		for _, f := range req.Files {
			if !isImage(f) {
				docs = append(docs, f)
			}
		}
		if len(docs) > 0 {
			p += "\n\nRead these files:\n- " + strings.Join(docs, "\n- ")
		}
	}
	return p + "\n\n(Only answer. Do not create, edit or delete files.)"
}

func isImage(f string) bool {
	switch strings.ToLower(filepath.Ext(f)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		return true
	}
	return false
}

func args(req provider.Request) []string {
	search := "disabled"
	if slices.Contains(req.Tools, "WebSearch") || slices.Contains(req.Tools, "WebFetch") {
		search = "live"
	}
	a := []string{"exec", "--json", "--skip-git-repo-check", "--color", "never", "-c", `web_search="` + search + `"`}
	if req.Model != "" {
		a = append(a, "--model", req.Model)
	}
	for _, f := range req.Files {
		if isImage(f) {
			a = append(a, "--image", f)
		}
	}
	if req.SessionID != "" {
		// a resumed session keeps the sandbox it was started with
		return append(a, "resume", req.SessionID, "-")
	}
	return append(a, "--sandbox", "read-only", "-")
}

type event struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
	Item     struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
	Usage struct {
		Input  int `json:"input_tokens"`
		Cached int `json:"cached_input_tokens"`
		Output int `json:"output_tokens"`
	} `json:"usage"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
	Message string `json:"message"`
}

func workDir() string {
	d := filepath.Join(os.TempDir(), "studyhomie-codex")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// waitDelay bounds how long a cancelled call waits for the CLI's output to close. Killing a .cmd
// wrapper (an npm install) leaves the real process holding the pipes; after this they are closed
// for it, so a call that runs out of time always ends.
const waitDelay = 5 * time.Second

func (c *CLI) Complete(ctx context.Context, req provider.Request, onText func(string)) (provider.Response, error) {
	bin := c.Bin
	if bin == "" {
		bin = "codex"
	}
	cmd := exec.CommandContext(ctx, bin, args(req)...)
	cmd.Dir = workDir()
	cmd.Stdin = strings.NewReader(prompt(req))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay
	out, err := cmd.StdoutPipe()
	if err != nil {
		return provider.Response{}, err
	}
	if err := cmd.Start(); err != nil {
		return provider.Response{}, fmt.Errorf("no pude ejecutar %s: %w (¿está instalado Codex CLI y con sesión iniciada? `codex login`)", bin, err)
	}
	resp, perr := parse(out, onText)
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return provider.Response{}, ctx.Err()
	}
	if perr != nil {
		return provider.Response{}, perr
	}
	if werr != nil && resp.Text == "" {
		return provider.Response{}, fmt.Errorf("codex falló: %v\n%s", werr, strings.TrimSpace(stderr.String()))
	}
	if resp.SessionID == "" {
		return provider.Response{}, fmt.Errorf("codex no devolvió una sesión: %s", strings.TrimSpace(stderr.String()))
	}
	return resp, nil
}

// parse reads the JSON Lines events of `codex exec --json`. The answer is the agent's messages;
// a turn with several (a note before searching, then the answer) keeps them all, in order.
func parse(r interface{ Read([]byte) (int, error) }, onText func(string)) (provider.Response, error) {
	var resp provider.Response
	var msgs []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var ev event
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "thread.started":
			resp.SessionID = ev.ThreadID
		case "item.completed":
			if ev.Item.Type == "agent_message" && ev.Item.Text != "" {
				if len(msgs) > 0 && onText != nil {
					onText("\n\n")
				}
				msgs = append(msgs, ev.Item.Text)
				if onText != nil {
					onText(ev.Item.Text)
				}
			}
		case "turn.completed":
			resp.Usage = provider.Usage{InputTokens: ev.Usage.Input, OutputTokens: ev.Usage.Output, CacheTokens: ev.Usage.Cached}
		case "turn.failed":
			return resp, fmt.Errorf("codex: %s", ev.Error.Message)
		case "error":
			return resp, fmt.Errorf("codex: %s", ev.Message)
		}
	}
	resp.Text = strings.Join(msgs, "\n\n")
	return resp, sc.Err()
}
