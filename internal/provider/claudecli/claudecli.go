// Package claudecli runs the user's locally installed `claude` binary in headless
// mode. It never reads or reuses the user's credentials: the CLI handles auth.
package claudecli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/krilinxito/studyhomie/internal/provider"
)

type CLI struct {
	// Bin is the executable name or path. Defaults to "claude".
	Bin string
}

func New() *CLI { return &CLI{Bin: "claude"} }

func (c *CLI) Name() string { return "claude-cli" }

// args builds a clean invocation: no user MCP servers, skills or slash commands,
// so every phase starts from a small, predictable context.
func args(req provider.Request) []string {
	tools := req.Tools
	prompt := req.Prompt
	var dirs []string
	if len(req.Files) > 0 {
		// the CLI's Read tool understands PDFs and images; give it access to their folders
		if !slices.Contains(tools, "Read") {
			tools = append(slices.Clone(tools), "Read")
		}
		for _, f := range req.Files {
			if d := filepath.Dir(f); !slices.Contains(dirs, d) {
				dirs = append(dirs, d)
			}
		}
		prompt += "\n\nFiles to read with the Read tool:\n- " + strings.Join(req.Files, "\n- ")
	}
	a := []string{
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--strict-mcp-config",
		"--disable-slash-commands",
		"--tools", strings.Join(tools, ","),
	}
	if len(tools) > 0 {
		// headless mode cannot answer permission prompts, so pre-approve exactly these tools
		a = append(a, "--allowedTools", strings.Join(tools, ","))
	}
	for _, d := range dirs {
		a = append(a, "--add-dir", d)
	}
	if req.System != "" {
		a = append(a, "--system-prompt", req.System)
	}
	if req.Model != "" {
		a = append(a, "--model", req.Model)
	}
	if req.SessionID != "" {
		a = append(a, "--resume", req.SessionID)
	}
	return a
}

type event struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Session string `json:"session_id"`
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
	Usage struct {
		InputTokens         int `json:"input_tokens"`
		OutputTokens        int `json:"output_tokens"`
		CacheCreationTokens int `json:"cache_creation_input_tokens"`
		CacheReadTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

func (c *CLI) Complete(ctx context.Context, req provider.Request, onText func(string)) (provider.Response, error) {
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.CommandContext(ctx, bin, args(req)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return provider.Response{}, err
	}
	if err := cmd.Start(); err != nil {
		return provider.Response{}, fmt.Errorf("start %s: %w (is Claude Code installed and logged in?)", bin, err)
	}

	var resp provider.Response
	var streamed strings.Builder
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var ev event
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "assistant":
			for _, b := range ev.Message.Content {
				if b.Type == "text" && b.Text != "" {
					streamed.WriteString(b.Text)
					if onText != nil {
						onText(b.Text)
					}
				}
			}
		case "result":
			resp.SessionID = ev.Session
			resp.Text = ev.Result
			resp.Usage = provider.Usage{
				InputTokens:  ev.Usage.InputTokens,
				OutputTokens: ev.Usage.OutputTokens,
				CacheTokens:  ev.Usage.CacheCreationTokens + ev.Usage.CacheReadTokens,
			}
			if ev.IsError {
				_ = cmd.Wait()
				return resp, fmt.Errorf("claude returned an error: %s", ev.Result)
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		return resp, fmt.Errorf("claude exited: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if resp.Text == "" {
		resp.Text = streamed.String()
	}
	return resp, nil
}
