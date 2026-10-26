package anthropicapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/krilinxito/studyhomie/internal/provider"
)

// sse renders one streamed assistant message.
func sse(text, stop string, in, out int) string {
	ev := func(name, data string) string { return "event: " + name + "\ndata: " + data + "\n\n" }
	q, _ := json.Marshal(text)
	return ev("message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":%d,"output_tokens":0}}}`, in)) +
		ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+string(q)+`}}`) +
		ev("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		ev("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q,"stop_sequence":null},"usage":{"output_tokens":%d}}`, stop, out)) +
		ev("message_stop", `{"type":"message_stop"}`)
}

type fakeAPI struct {
	replies []string
	bodies  []map[string]any
}

func (f *fakeAPI) start(t *testing.T) *API {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(raw, &body)
		f.bodies = append(f.bodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, f.replies[0])
		f.replies = f.replies[1:]
	}))
	t.Cleanup(srv.Close)
	return New(option.WithBaseURL(srv.URL), option.WithAPIKey("test"), option.WithMaxRetries(0))
}

func nMessages(body map[string]any) int { return len(body["messages"].([]any)) }

func TestCompleteStreamsAndReportsUsage(t *testing.T) {
	f := &fakeAPI{replies: []string{sse("Hola mundo", "end_turn", 120, 7)}}
	a := f.start(t)
	var chunks []string
	resp, err := a.Complete(context.Background(), provider.Request{System: "sys", Prompt: "hi"}, func(s string) { chunks = append(chunks, s) })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "Hola mundo" || strings.Join(chunks, "") != "Hola mundo" || resp.SessionID == "" {
		t.Errorf("resp = %+v chunks = %v", resp, chunks)
	}
	if resp.Usage.InputTokens != 120 || resp.Usage.OutputTokens != 7 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	b := f.bodies[0]
	if b["model"] != DefaultModel || b["stream"] != true {
		t.Errorf("request = %v", b)
	}
	if sys, _ := json.Marshal(b["system"]); !strings.Contains(string(sys), "sys") {
		t.Errorf("system prompt not sent: %s", sys)
	}
	if _, has := b["temperature"]; has {
		t.Error("sampling parameters are rejected by current models and must not be sent")
	}
}

func TestSessionContinuationKeepsHistory(t *testing.T) {
	f := &fakeAPI{replies: []string{sse("uno", "end_turn", 10, 1), sse("dos", "end_turn", 20, 1)}}
	a := f.start(t)
	r1, err := a.Complete(context.Background(), provider.Request{Prompt: "a"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := a.Complete(context.Background(), provider.Request{Prompt: "b", SessionID: r1.SessionID, Model: "claude-sonnet-5-5"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r2.SessionID != r1.SessionID || nMessages(f.bodies[1]) != 3 || f.bodies[1]["model"] != "claude-sonnet-5-5" {
		t.Errorf("second request must carry user/assistant/user: %d messages, body=%v", nMessages(f.bodies[1]), f.bodies[1]["model"])
	}
	// a fresh request must not see the old history
	f.replies = append(f.replies, sse("tres", "end_turn", 5, 1))
	if _, err := a.Complete(context.Background(), provider.Request{Prompt: "c"}, nil); err != nil {
		t.Fatal(err)
	}
	if nMessages(f.bodies[2]) != 1 {
		t.Errorf("fresh session leaked history: %d messages", nMessages(f.bodies[2]))
	}
	if _, err := a.Complete(context.Background(), provider.Request{Prompt: "x", SessionID: "nope"}, nil); err == nil {
		t.Error("unknown session must be an error")
	}
}

func TestPauseTurnIsResumed(t *testing.T) {
	f := &fakeAPI{replies: []string{sse("Buscando... ", "pause_turn", 10, 3), sse("Listo.", "end_turn", 30, 2)}}
	a := f.start(t)
	resp, err := a.Complete(context.Background(), provider.Request{Prompt: "q", Tools: []string{"WebSearch", "WebFetch"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "Buscando... Listo." {
		t.Errorf("text = %q", resp.Text)
	}
	if len(f.bodies) != 2 || nMessages(f.bodies[1]) != 2 {
		t.Fatalf("resume must resend the paused turn: %d requests", len(f.bodies))
	}
	raw, _ := json.Marshal(f.bodies[0]["tools"])
	for _, want := range []string{"web_search_20260209", "web_fetch_20260209"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("tools missing %s: %s", want, raw)
		}
	}
	if resp.Usage.InputTokens != 30 {
		t.Errorf("usage must come from the last request, got %+v", resp.Usage)
	}
}

func TestTruncatedAndErrors(t *testing.T) {
	f := &fakeAPI{replies: []string{sse("cortad", "max_tokens", 10, 5)}}
	a := f.start(t)
	if _, err := a.Complete(context.Background(), provider.Request{Prompt: "q"}, nil); err == nil || !strings.Contains(err.Error(), "cortó") {
		t.Errorf("a truncated answer must be an error, got %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	}))
	defer srv.Close()
	bad := New(option.WithBaseURL(srv.URL), option.WithAPIKey("x"), option.WithMaxRetries(0))
	if _, err := bad.Complete(context.Background(), provider.Request{Prompt: "q"}, nil); err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("auth errors must say how to configure the key, got %v", err)
	}
}

func TestUserMessageAttachments(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o644)
		return p
	}
	msg, err := userMessage("describe", []string{write("a.png", "PNG"), write("b.pdf", "%PDF"), write("c.txt", "notas")})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(msg)
	s := string(raw)
	for _, want := range []string{`"image/png"`, `"application/pdf"`, "File c.txt", "notas", "describe"} {
		if !strings.Contains(s, want) {
			t.Errorf("message missing %s: %s", want, s)
		}
	}
	if strings.Index(s, "describe") < strings.Index(s, "notas") {
		t.Error("files must come before the prompt")
	}
	if _, err := userMessage("x", []string{filepath.Join(dir, "missing.png")}); err == nil {
		t.Error("a missing file must be an error")
	}
}
