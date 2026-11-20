package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/provider"
)

func TestStreamsAndRemembersTheConversation(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			fmt.Fprint(w, `{"data":[{"id":"b-model"},{"id":"a-model"}]}`)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k-123" {
			t.Errorf("auth header = %q", got)
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Ho\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"la\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	t.Setenv("TEST_KEY", "k-123")
	c := New(Options{Name: "openrouter", BaseURL: srv.URL + "/v1/", KeyEnvs: []string{"TEST_KEY"}, DefaultModel: "m1", WebPlugin: true})

	var streamed strings.Builder
	r1, err := c.Complete(context.Background(), provider.Request{System: "sé breve", Prompt: "hola", Tools: []string{"WebSearch"}}, func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if r1.Text != "Hola" || streamed.String() != "Hola" || r1.Usage.InputTokens != 12 || r1.SessionID == "" {
		t.Fatalf("first reply: %+v (streamed %q)", r1, streamed.String())
	}
	msgs := bodies[0]["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" || bodies[0]["model"] != "m1" {
		t.Errorf("first request: %v", bodies[0])
	}
	if _, ok := bodies[0]["plugins"]; !ok {
		t.Error("OpenRouter's web plugin must be on when the phase searches")
	}

	if _, err := c.Complete(context.Background(), provider.Request{Prompt: "¿y luego?", SessionID: r1.SessionID, Model: "m2"}, nil); err != nil {
		t.Fatal(err)
	}
	msgs = bodies[1]["messages"].([]any)
	if len(msgs) != 3 || msgs[1].(map[string]any)["content"] != "Hola" || bodies[1]["model"] != "m2" {
		t.Errorf("the second turn must carry the first: %v", msgs)
	}
	if _, ok := bodies[1]["plugins"]; ok {
		t.Error("no web plugin when the phase does not search")
	}

	models, err := c.Models(context.Background())
	if err != nil || strings.Join(models, ",") != "a-model,b-model" {
		t.Errorf("models: %v %v", models, err)
	}
}

func TestMissingKeyAndModel(t *testing.T) {
	t.Setenv("NOPE_KEY", "")
	c := New(Options{Name: "groq", BaseURL: "http://x", KeyEnvs: []string{"NOPE_KEY"}, DefaultModel: "m"})
	if _, err := c.Complete(context.Background(), provider.Request{Prompt: "hi"}, nil); err == nil || !strings.Contains(err.Error(), "NOPE_KEY") {
		t.Errorf("missing key: %v", err)
	}
	local := New(Options{Name: "ollama", BaseURL: "http://x"})
	if _, err := local.Complete(context.Background(), provider.Request{Prompt: "hi"}, nil); err == nil || !strings.Contains(err.Error(), "modelo") {
		t.Errorf("missing model: %v", err)
	}
}

func TestAPIErrorsAreReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"model not found"}}`, http.StatusNotFound)
	}))
	defer srv.Close()
	c := New(Options{Name: "ollama", BaseURL: srv.URL, DefaultModel: "nope"})
	if _, err := c.Complete(context.Background(), provider.Request{Prompt: "hi"}, nil); err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "model not found") {
		t.Errorf("error = %v", err)
	}
}
