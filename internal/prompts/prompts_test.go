package prompts

import (
	"strings"
	"testing"
)

func TestLearnerFacingPromptsShareTheVoiceRules(t *testing.T) {
	for _, name := range []string{"prepare", "feynman", "quiz", "daily", "scope", "roadmap", "extend", "ask"} {
		got := Get(name, "español")
		if !strings.Contains(got, "Never use emojis") {
			t.Errorf("%s must carry the writing rules", name)
		}
		if strings.Contains(got, "{{language}}") {
			t.Errorf("%s left a {{language}} placeholder", name)
		}
	}
}

func TestProgramFacingPromptsStayWithoutVoiceRules(t *testing.T) {
	for _, name := range []string{"interactive", "extract", "compact"} {
		if strings.Contains(Get(name, "español"), "Never use emojis") {
			t.Errorf("%s produces output for the program, not for the learner to read", name)
		}
	}
}
