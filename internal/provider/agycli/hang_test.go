package agycli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/krilinxito/studyhomie/internal/provider"
)

// TestMain lets the test binary stand in for agy: with HANG_AGY set it starts a conversation and
// never answers, the way a model stuck thinking does.
func TestMain(m *testing.M) {
	if pidFile := os.Getenv("HANG_AGY"); pidFile != "" {
		_ = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644)
		fmt.Println(`{"event":"init","conversation_id":"c1"}`)
		time.Sleep(20 * time.Second) // longer than the test waits; it then ends on its own
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCompleteStopsWhenTheCallRunsOutOfTime(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := self
	if runtime.GOOS == "windows" {
		// through a .cmd wrapper, like an npm install: killing cmd.exe leaves the real process
		// holding the pipe, and the call must end anyway
		bin = filepath.Join(t.TempDir(), "agy.cmd")
		if err := os.WriteFile(bin, []byte("@\""+self+"\" %*\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Setenv("HANG_AGY", pidFile)
	defer func() { // the process a wrapper leaves behind
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(string(b)); err == nil {
				if p, err := os.FindProcess(pid); err == nil {
					_ = p.Kill()
				}
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = (&CLI{Bin: bin}).Complete(ctx, provider.Request{Prompt: "hola"}, nil)
	if err == nil || time.Since(start) > waitDelay+5*time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}
}
