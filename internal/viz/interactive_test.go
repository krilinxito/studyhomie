package viz

import (
	"strings"
	"testing"
)

func TestParseLibs(t *testing.T) {
	html := `<!doctype html><html><head><meta name="studyhomie-libs" content="Tone.js, three, katex, unknownlib"></head></html>`
	known, unknown := ParseLibs(html)
	if got := strings.Join(known, ","); got != "three,tone,katex" {
		t.Errorf("known = %q, want three,tone,katex", got)
	}
	if got := strings.Join(unknown, ","); got != "unknownlib" {
		t.Errorf("unknown = %q, want unknownlib", got)
	}
}

func TestValidateHTMLAllowsWhitelistedAndRejectsOthers(t *testing.T) {
	good := []string{
		`<!doctype html><html><head><meta name="studyhomie-libs" content="tone"></head><body><script>Tone.start();</script></body></html>`,
		`<!doctype html><html><head><meta name="studyhomie-libs" content="three, katex"></head><body><script>new THREE.Scene();</script></body></html>`,
		`<!doctype html><html><head><link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.9.4/leaflet.min.css"><script src="https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.9.4/leaflet.min.js"></script></head><body><script>L.map('m');</script></body></html>`,
		`<!doctype html><html><head><script src="https://cdn.jsdelivr.net/npm/p5@1.11.3/lib/p5.min.js"></script></head><body><script>new p5();</script></body></html>`,
	}
	for i, h := range good {
		if err := ValidateHTML(h); err != nil {
			t.Errorf("good[%d] rejected: %v", i, err)
		}
	}

	bad := map[string]string{
		"empty":        "",
		"unknown lib":  `<html><head><meta name="studyhomie-libs" content="jquery"></head><body><script>1</script></body></html>`,
		"evil cdn":     `<html><script src="https://evil.example/a.js"></script></html>`,
		"http cdn":     `<html><script src="http://cdnjs.cloudflare.com/ajax/libs/p5.js/1.0/p5.js"></script></html>`,
		"protocol rel": `<html><script src="//cdnjs.cloudflare.com/ajax/libs/p5.js/1.0/p5.js"></script></html>`,
		"external img": `<html><img src="https://example.com/a.png"><script>1</script></html>`,
		"alert":        `<html><script>alert('hi')</script></html>`,
		"fetch":        `<html><script>fetch('/api')</script></html>`,
		"storage":      `<html><script>localStorage.setItem('a','b')</script></html>`,
		"css import":   `<html><style>@import 'https://cdnjs.cloudflare.com/a.css';</style><script>1</script></html>`,
		"no script":    `<html><body>hi</body></html>`,
		"incomplete":   `<html><script>1</script>`,
		"too large":    `<html><script>` + strings.Repeat("x", MaxHTMLBytes) + `</script></html>`,
	}
	for name, h := range bad {
		if err := ValidateHTML(h); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestUsesCDN(t *testing.T) {
	cdn, tiles := UsesCDN(`<html><head><meta name="studyhomie-libs" content="three, tone"></head></html>`)
	if cdn || tiles {
		t.Errorf("bundled libs should not require CDN: cdn=%v tiles=%v", cdn, tiles)
	}
	cdn, tiles = UsesCDN(`<html><head><meta name="studyhomie-libs" content="leaflet"></head></html>`)
	if !cdn || !tiles {
		t.Errorf("leaflet should require CDN and tiles: cdn=%v tiles=%v", cdn, tiles)
	}
	cdn, tiles = UsesCDN(`<html><head><meta name="studyhomie-libs" content="matter"></head></html>`)
	if !cdn || tiles {
		t.Errorf("matter should require CDN without tiles: cdn=%v tiles=%v", cdn, tiles)
	}
}
