package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/krilinxito/studyhomie/internal/provider"
)

// stuck is a provider that never answers until its call is cancelled, like a model that keeps thinking.
type stuck struct{ calls int }

func (s *stuck) Name() string { return "stuck" }
func (s *stuck) Complete(ctx context.Context, _ provider.Request, _ func(string)) (provider.Response, error) {
	s.calls++
	<-ctx.Done()
	return provider.Response{}, ctx.Err()
}

func TestConvoCutsACallThatNeverAnswers(t *testing.T) {
	c := &Convo{P: &stuck{}, Timeout: 50 * time.Millisecond}
	start := time.Now()
	_, err := c.Say(context.Background(), "hola", nil)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}
	if !strings.Contains(ErrorText(err, "es"), "no respondió en 1 minutos") || IsInputError(err) {
		t.Errorf("timeout error = %q", ErrorText(err, "es"))
	}
	if en := ErrorText(err, "en"); strings.Contains(en, "no respondió") {
		t.Errorf("the timeout error is not translated: %q", en)
	}

	// the learner cancelling is not a timeout
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Say(ctx, "hola", nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled call = %v", err)
	}
}

func TestCallTimeout(t *testing.T) {
	if d := callTimeout(5, nil); d != 5*time.Minute {
		t.Errorf("plain call = %s", d)
	}
	if d := callTimeout(5, []string{"WebSearch", "WebFetch"}); d != 15*time.Minute {
		t.Errorf("a call that searches the web = %s", d)
	}
	if d := callTimeout(0, nil); d != 0 {
		t.Errorf("0 means no limit, got %s", d)
	}
	s, _ := newSession(t, &fake{}, "")
	s.Cfg.Timeout = 2
	if c := s.convo("x", "WebSearch"); c.Timeout != 6*time.Minute {
		t.Errorf("convo timeout = %s", c.Timeout)
	}
	if c := s.quickConvo("x"); c.Timeout != 2*time.Minute {
		t.Errorf("quick convo timeout = %s", c.Timeout)
	}
}
