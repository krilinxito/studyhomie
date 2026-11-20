package openaiapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/krilinxito/studyhomie/internal/provider"
)

func TestResponsesStreamAndContinue(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer sk-x" {
			t.Errorf("request %s auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hola \"}\n\n")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"mundo\"}\n\n")
		fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_%d\",\"usage\":{\"input_tokens\":40,\"output_tokens\":2}}}\n\n", len(bodies))
	}))
	defer srv.Close()
	BaseURL = srv.URL + "/v1"
	t.Setenv(KeyEnv, "sk-x")
	c := New("gpt-test")

	var streamed string
	r, err := c.Complete(context.Background(), provider.Request{System: "sys", Prompt: "hola", Tools: []string{"WebSearch", "WebFetch"}}, func(s string) { streamed += s })
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "Hola mundo" || streamed != "Hola mundo" || r.SessionID != "resp_1" || r.Usage.InputTokens != 40 {
		t.Fatalf("reply = %+v", r)
	}
	if bodies[0]["instructions"] != "sys" || bodies[0]["model"] != "gpt-test" || bodies[0]["tools"] == nil {
		t.Errorf("first body: %v", bodies[0])
	}
	if _, err := c.Complete(context.Background(), provider.Request{System: "sys", Prompt: "sigue", SessionID: r.SessionID}, nil); err != nil {
		t.Fatal(err)
	}
	if bodies[1]["previous_response_id"] != "resp_1" || bodies[1]["instructions"] != "sys" || bodies[1]["tools"] != nil {
		t.Errorf("the next turn continues on OpenAI's side and resends the instructions: %v", bodies[1])
	}
}

func TestFailedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"cuota agotada\"}}}\n\n")
	}))
	defer srv.Close()
	BaseURL = srv.URL
	t.Setenv(KeyEnv, "sk-x")
	if _, err := New("m").Complete(context.Background(), provider.Request{Prompt: "hi"}, nil); err == nil || err.Error() != "cuota agotada" {
		t.Errorf("error = %v", err)
	}
}
