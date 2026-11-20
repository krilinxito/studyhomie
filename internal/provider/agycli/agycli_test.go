package agycli

import (
	"slices"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/provider"
)

// recorded from agy 1.5.9: `agy --print ... --output-format stream-json`
const sample = `{"event":"init","conversation_id":"d3e3-conv","init":{"cwd":"C:\\tmp","tools":["search_web"],"permission_mode":"request-review"}}
{"event":"step_update","step_update":{"conversation_id":"d3e3-conv","step_index":0,"state":"DONE","step_type":"user_input"}}
{"event":"step_update","step_update":{"conversation_id":"d3e3-conv","step_index":1,"state":"ACTIVE","step_type":"agent_response","text_delta":"ok"}}
{"event":"step_update","step_update":{"conversation_id":"d3e3-conv","step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"\n","usage":{"input_tokens":9878,"output_tokens":33}}}
{"event":"result","result":{"conversation_id":"d3e3-conv","status":"SUCCESS","response":"ok\n","num_turns":1,"usage":{"input_tokens":9878,"output_tokens":33,"cache_read_tokens":0}}}
`

func TestParseRecordedOutput(t *testing.T) {
	var streamed strings.Builder
	r, s, err := parse(strings.NewReader(sample), func(t string) { streamed.WriteString(t) })
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "ok" || r.SessionID != "d3e3-conv" || r.Usage.InputTokens != 9878 || s != "ok\n" || streamed.String() != "ok\n" {
		t.Errorf("parsed %+v, streamed %q", r, s)
	}
}

func TestParseFailedRun(t *testing.T) {
	_, _, err := parse(strings.NewReader(`{"event":"result","result":{"status":"ERROR","response":"quota exceeded"}}`), nil)
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Errorf("error = %v", err)
	}
}

func TestArgs(t *testing.T) {
	a := args(provider.Request{System: "be brief", Prompt: "hola", Model: "gemini-4-neon-low", Tools: []string{"WebSearch"}})
	if a[0] != "--print" || !strings.HasPrefix(a[1], "INSTRUCTIONS") || !strings.Contains(a[1], "hola") || !strings.Contains(a[1], "search_web") {
		t.Errorf("first turn carries the instructions and allows search: %q", a[1])
	}
	for _, want := range []string{"stream-json", "--disable-slash-commands", "--model"} {
		if !slices.Contains(a, want) {
			t.Errorf("missing %q in %v", want, a)
		}
	}
	b := args(provider.Request{System: "be brief", Prompt: "sigue", SessionID: "c1"})
	if strings.Contains(b[1], "INSTRUCTIONS") || !slices.Contains(b, "--conversation") || !strings.Contains(b[1], "Do not use tools") {
		t.Errorf("a follow-up continues the conversation without repeating the instructions: %v", b)
	}
}
