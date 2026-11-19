package viz

import (
	"strings"
	"testing"
)

func TestCheckExpr(t *testing.T) {
	vars := map[string]bool{"x": true, "a": true, "b": true}
	good := []string{"a*sin(b*x)", "2x + 3(x-1)", "x^2 - 2**3", "-x", "max(a, b, 1)", "exp(-x^2/2)/sqrt(2pi)", "1.5e-3*x", "e^x", "pi"}
	for _, s := range good {
		if err := CheckExpr(s, vars); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	bad := []string{"", "y + 1", "sin x", "a +", "(a", "alert(1)", "x; y", "a..b", "window.x", "sin(x))"}
	for _, s := range bad {
		if err := CheckExpr(s, vars); err == nil {
			t.Errorf("%q should be rejected", s)
		}
	}
}

func TestCleanKeepsValidAndDropsBroken(t *testing.T) {
	md := strings.Join([]string{
		"Texto antes.",
		"```viz",
		`{"type":"plot","params":[{"name":"a","min":0,"max":2,"value":5}],"curves":[{"expr":"a*x"}]}`,
		"```",
		"",
		"```viz",
		`{"type":"plot","curves":[{"expr":"y*x"}]}`,
		"```",
		"```python",
		"print('```viz')",
		"```",
		"```viz",
		`{"type":"predict","question":"¿?","options":["a","b"],"answer":1}`,
		"```",
	}, "\n")
	out, warns := Clean(md)
	if len(warns) != 1 || !strings.Contains(warns[0], "variable desconocida") {
		t.Fatalf("warnings = %v", warns)
	}
	if strings.Count(out, "\n```viz\n") != 2 {
		t.Errorf("expected two viz blocks left:\n%s", out)
	}
	if !strings.Contains(out, `"value":2`) {
		t.Errorf("a slider value out of range must be clamped:\n%s", out)
	}
	if !strings.Contains(out, "print('```viz')") {
		t.Errorf("code blocks must be left alone:\n%s", out)
	}
}

func TestSampleAndTransform(t *testing.T) {
	if _, err := Parse(`{"type":"sample","outcomes":[{"label":"cara","p":1},{"label":"cruz","p":1}],"draws":3}`); err == nil {
		t.Error("several draws need numeric values")
	}
	b, err := Parse(`{"type":"sample","outcomes":[{"label":"1","value":1,"p":1},{"label":"2","value":2,"p":1}],"draws":2}`)
	if err != nil || b.Measure != "sum" {
		t.Errorf("sample: %v %+v", err, b)
	}
	if _, err := Parse(`{"type":"transform2d","matrix":[[1,"k"],[0,1]],"params":[{"name":"k","min":-2,"max":2,"value":1}]}`); err != nil {
		t.Error(err)
	}
	if _, err := Parse(`{"type":"transform2d","matrix":[[1,2,3],[0,1]]}`); err == nil {
		t.Error("a 2x3 matrix must be rejected")
	}
	if _, err := Parse(`{"type":"plot","params":[{"name":"sin","min":0,"max":1,"value":0}],"curves":[{"expr":"x"}]}`); err == nil {
		t.Error("a parameter cannot shadow a function")
	}
}
