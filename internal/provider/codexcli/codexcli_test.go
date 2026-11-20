package codexcli

import (
	"slices"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/provider"
)

const sample = `{"type":"thread.started","thread_id":"th_1"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"i0","type":"reasoning","text":"thinking"}}
{"type":"item.completed","item":{"id":"i1","type":"web_search","query":"fourier"}}
{"type":"item.completed","item":{"id":"i2","type":"agent_message","text":"Busqué fuentes."}}
{"type":"item.completed","item":{"id":"i3","type":"agent_message","text":"La respuesta."}}
{"type":"turn.completed","usage":{"input_tokens":120,"cached_input_tokens":20,"output_tokens":9}}
`

func TestParse(t *testing.T) {
	var streamed strings.Builder
	r, err := parse(strings.NewReader(sample), func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if r.SessionID != "th_1" || r.Text != "Busqué fuentes.\n\nLa respuesta." || streamed.String() != r.Text || r.Usage.CacheTokens != 20 {
		t.Errorf("parsed %+v, streamed %q", r, streamed.String())
	}
	if _, err := parse(strings.NewReader(`{"type":"turn.failed","error":{"message":"usage limit"}}`), nil); err == nil || !strings.Contains(err.Error(), "usage limit") {
		t.Errorf("failed turn: %v", err)
	}
}

func TestArgsAndPrompt(t *testing.T) {
	first := provider.Request{System: "sys", Prompt: "hola", Model: "m", Tools: []string{"WebSearch"}, Files: []string{"a.png", "b.txt"}}
	a := args(first)
	if a[0] != "exec" || !slices.Contains(a, "--json") || !slices.Contains(a, `web_search="live"`) || !slices.Contains(a, "read-only") || a[len(a)-1] != "-" {
		t.Errorf("first turn args: %v", a)
	}
	if !slices.Contains(a, "--image") || slices.Contains(a, "b.txt") {
		t.Errorf("images go as --image, other files are named in the prompt: %v", a)
	}
	p := prompt(first)
	if !strings.HasPrefix(p, "INSTRUCTIONS") || !strings.Contains(p, "b.txt") || strings.Contains(p, "a.png") {
		t.Errorf("prompt: %q", p)
	}
	next := args(provider.Request{Prompt: "sigue", SessionID: "th_1"})
	i := slices.Index(next, "resume")
	if i < 0 || next[i+1] != "th_1" || slices.Contains(next, "--sandbox") || !slices.Contains(next, `web_search="disabled"`) {
		t.Errorf("follow-up args: %v", next)
	}
	if strings.Contains(prompt(provider.Request{System: "sys", Prompt: "x", SessionID: "s"}), "INSTRUCTIONS") {
		t.Error("the instructions are sent once, at the start of the session")
	}
}
