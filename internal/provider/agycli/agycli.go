// Package agycli runs Google's Antigravity CLI (`agy`) in print mode. The CLI handles
// sign-in; StudyHomie never sees credentials. Experimental: agy is an agent with tools of
// its own, so each call runs in an empty folder and is told to only answer.
package agycli

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

	"github.com/krilinxito/studyhomie/internal/provider"
)

type CLI struct {
	Bin string // executable; defaults to "agy"
}

func New() *CLI { return &CLI{Bin: "agy"} }

func (c *CLI) Name() string { return "agy" }

// guard tells the agent what it may do: answer, and search only when the phase asks for it.
func guard(tools []string) string {
	if slices.Contains(tools, "WebSearch") || slices.Contains(tools, "WebFetch") {
		return "You may use search_web and read_url_content to find and check sources. Do not create, edit or delete files and do not run commands."
	}
	return "Answer directly. Do not use tools: do not search, create, edit or delete files, or run commands."
}

func args(req provider.Request) []string {
	prompt := req.Prompt
	if req.SessionID == "" && req.System != "" {
		// agy has no system prompt flag: the instructions open the conversation
		prompt = "INSTRUCTIONS (follow them for the whole conversation):\n" + req.System + "\n\n---\n\n" + prompt
	}
	prompt += "\n\n(" + guard(req.Tools) + ")"
	var dirs []string
	if len(req.Files) > 0 {
		prompt += "\n\nRead these files with view_file:\n- " + strings.Join(req.Files, "\n- ")
		for _, f := range req.Files {
			if d := filepath.Dir(f); !slices.Contains(dirs, d) {
				dirs = append(dirs, d)
			}
		}
	}
	a := []string{"--print", prompt, "--output-format", "stream-json", "--disable-slash-commands"}
	for _, d := range dirs {
		a = append(a, "--add-dir", d)
	}
	if req.Model != "" {
		a = append(a, "--model", req.Model)
	}
	if req.SessionID != "" {
		a = append(a, "--conversation", req.SessionID)
	}
	return a
}

type event struct {
	Event          string `json:"event"`
	ConversationID string `json:"conversation_id"`
	StepUpdate     struct {
		StepType  string `json:"step_type"`
		TextDelta string `json:"text_delta"`
	} `json:"step_update"`
	Result struct {
		ConversationID string `json:"conversation_id"`
		Status         string `json:"status"`
		Response       string `json:"response"`
		Usage          struct {
			Input     int `json:"input_tokens"`
			Output    int `json:"output_tokens"`
			CacheRead int `json:"cache_read_tokens"`
		} `json:"usage"`
	} `json:"result"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// workDir is an empty folder of our own, so the agent never runs in the learner's files.
func workDir() string {
	d := filepath.Join(os.TempDir(), "studyhomie-agy")
	_ = os.MkdirAll(d, 0o755)
	return d
}

func (c *CLI) Complete(ctx context.Context, req provider.Request, onText func(string)) (provider.Response, error) {
	bin := c.Bin
	if bin == "" {
		bin = "agy"
	}
	cmd := exec.CommandContext(ctx, bin, args(req)...)
	cmd.Dir = workDir()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return provider.Response{}, err
	}
	if err := cmd.Start(); err != nil {
		return provider.Response{}, fmt.Errorf("no pude ejecutar %s: %w (¿está instalado Antigravity CLI y con sesión iniciada?)", bin, err)
	}
	resp, streamed, perr := parse(out, onText)
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return provider.Response{}, ctx.Err()
	}
	if perr != nil {
		return provider.Response{}, perr
	}
	if werr != nil && resp.Text == "" {
		return provider.Response{}, fmt.Errorf("agy falló: %v\n%s", werr, strings.TrimSpace(stderr.String()))
	}
	if resp.Text == "" {
		resp.Text = streamed
	}
	if resp.SessionID == "" {
		return provider.Response{}, fmt.Errorf("agy no devolvió una conversación: %s", strings.TrimSpace(stderr.String()))
	}
	return resp, nil
}

// parse reads agy's stream-json events.
func parse(r interface{ Read([]byte) (int, error) }, onText func(string)) (provider.Response, string, error) {
	var resp provider.Response
	var streamed strings.Builder
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var ev event
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Event {
		case "init":
			resp.SessionID = ev.ConversationID
		case "step_update":
			if ev.StepUpdate.StepType == "agent_response" && ev.StepUpdate.TextDelta != "" {
				streamed.WriteString(ev.StepUpdate.TextDelta)
				if onText != nil {
					onText(ev.StepUpdate.TextDelta)
				}
			}
		case "result":
			if ev.Result.ConversationID != "" {
				resp.SessionID = ev.Result.ConversationID
			}
			resp.Text = strings.TrimRight(ev.Result.Response, "\n")
			resp.Usage = provider.Usage{InputTokens: ev.Result.Usage.Input, OutputTokens: ev.Result.Usage.Output, CacheTokens: ev.Result.Usage.CacheRead}
			if ev.Result.Status != "" && ev.Result.Status != "SUCCESS" {
				return resp, streamed.String(), fmt.Errorf("agy terminó con estado %s: %s", ev.Result.Status, ev.Result.Response)
			}
		case "error":
			return resp, streamed.String(), fmt.Errorf("agy: %s", ev.Error.Message)
		}
	}
	return resp, streamed.String(), sc.Err()
}

// Models lists the models agy offers (`agy models`).
func (c *CLI) Models(ctx context.Context) ([]string, error) {
	bin := c.Bin
	if bin == "" {
		bin = "agy"
	}
	cmd := exec.CommandContext(ctx, bin, "models")
	cmd.Dir = workDir()
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.SplitN(strings.TrimSpace(line), "\t", 2)
		if len(f) == 2 && f[0] != "" {
			ids = append(ids, f[0])
		}
	}
	return ids, nil
}
