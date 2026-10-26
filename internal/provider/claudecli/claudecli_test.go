package claudecli

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/provider"
)

func TestArgsCleanContext(t *testing.T) {
	a := args(provider.Request{Prompt: "hi", System: "sys", Tools: []string{"WebSearch", "WebFetch"}, SessionID: "abc"})
	for _, want := range []string{"--strict-mcp-config", "--disable-slash-commands", "--resume", "abc", "WebSearch,WebFetch", "--system-prompt", "--allowedTools"} {
		if !slices.Contains(a, want) {
			t.Errorf("args missing %q: %v", want, a)
		}
	}
}

func TestArgsFilesEnableReadAndDir(t *testing.T) {
	a := args(provider.Request{Prompt: "describe", Files: []string{filepath.Join("some", "dir", "a.png"), filepath.Join("some", "dir", "b.pdf")}})
	if !slices.Contains(a, "Read") || !slices.Contains(a, "--add-dir") || !slices.Contains(a, filepath.Join("some", "dir")) {
		t.Errorf("files must enable Read and add their dir: %v", a)
	}
	if n := strings.Count(strings.Join(a, " "), "--add-dir"); n != 1 {
		t.Errorf("same dir must be added once, got %d", n)
	}
	if !strings.Contains(a[1], "a.png") || !strings.Contains(a[1], "b.pdf") {
		t.Errorf("prompt must name the files: %q", a[1])
	}
}

func TestArgsNoSessionByDefault(t *testing.T) {
	if slices.Contains(args(provider.Request{Prompt: "hi"}), "--resume") {
		t.Error("fresh request must not resume a session")
	}
}
