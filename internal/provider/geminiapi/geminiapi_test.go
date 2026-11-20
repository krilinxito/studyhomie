package geminiapi

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

func TestGeminiStreamsSkipsThoughtsAndRemembers(t *testing.T) {
	var bodies []map[string]any
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "g-key" {
			t.Errorf("key header = %q", r.Header.Get("x-goog-api-key"))
		}
		if strings.HasSuffix(r.URL.Path, "/models") {
			fmt.Fprint(w, `{"models":[{"name":"models/gemini-x","supportedGenerationMethods":["generateContent"]},{"name":"models/embed","supportedGenerationMethods":["embedContent"]}]}`)
			return
		}
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"pensando…\",\"thought\":true}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hola\"}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"!\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":20,\"candidatesTokenCount\":2}}\n\n")
	}))
	defer srv.Close()
	BaseURL = srv.URL + "/v1beta"
	t.Setenv("GEMINI_API_KEY", "g-key")
	c := New("gemini-x")

	r, err := c.Complete(context.Background(), provider.Request{System: "sys", Prompt: "hola", Tools: []string{"WebSearch"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "Hola!" || r.Usage.InputTokens != 20 {
		t.Fatalf("reply = %+v (the model's thoughts must not be part of the answer)", r)
	}
	if paths[0] != "/v1beta/models/gemini-x:streamGenerateContent?alt=sse" || bodies[0]["systemInstruction"] == nil || bodies[0]["tools"] == nil {
		t.Errorf("first request %s %v", paths[0], bodies[0])
	}
	if _, err := c.Complete(context.Background(), provider.Request{Prompt: "y", SessionID: r.SessionID}, nil); err != nil {
		t.Fatal(err)
	}
	turns := bodies[1]["contents"].([]any)
	if len(turns) != 3 || turns[1].(map[string]any)["role"] != "model" {
		t.Errorf("history: %v", turns)
	}
	if m, err := c.Models(context.Background()); err != nil || len(m) != 1 || m[0] != "gemini-x" {
		t.Errorf("models: %v %v", m, err)
	}
}

func TestGeminiNeedsKeyAndModel(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	if _, err := New("m").Complete(context.Background(), provider.Request{Prompt: "x"}, nil); err == nil || !strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Errorf("missing key: %v", err)
	}
	t.Setenv("GOOGLE_API_KEY", "k")
	if _, err := New("").Complete(context.Background(), provider.Request{Prompt: "x"}, nil); err == nil || !strings.Contains(err.Error(), "modelo") {
		t.Errorf("missing model: %v", err)
	}
}
