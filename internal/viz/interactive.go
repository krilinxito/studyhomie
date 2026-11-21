package viz

import (
	"fmt"
	"regexp"
	"strings"
)

// MaxHTMLBytes bounds a generated interactive widget HTML document (not counting
// libraries injected by the server when the page is served).
const MaxHTMLBytes = 160 << 10

// Lib describes an optional library a generated HTML widget may request via
// <meta name="studyhomie-libs" content="...">. Bundled libraries are injected inline
// from the app's embedded static/vendor/ files so widgets work offline in opaque
// sandboxed iframes; CDN libraries are loaded from whitelisted CDNs when online.
type Lib struct {
	Name    string
	Bundled bool
	Global  string   // JS global checked to verify the library loaded (e.g. "L", "Matter")
	Scripts []string // static-relative paths if Bundled, or full HTTPS URLs if CDN
	Styles  []string // static-relative CSS paths if Bundled, or full HTTPS URLs if CDN
	Tiles   bool     // needs map tile image hosts in CSP (Leaflet)
}

// Libraries is the registry of libraries available to interactive HTML widgets.
var Libraries = map[string]Lib{
	"three": {
		Name:    "three",
		Bundled: true,
		Global:  "THREE",
		Scripts: []string{"vendor/three/three.min.js", "vendor/three/OrbitControls.js"},
	},
	"tone": {
		Name:    "tone",
		Bundled: true,
		Global:  "Tone",
		Scripts: []string{"vendor/tone/Tone.js"},
	},
	"jsxgraph": {
		Name:    "jsxgraph",
		Bundled: true,
		Global:  "JXG",
		Styles:  []string{"vendor/jsxgraph/jsxgraph.css"},
		Scripts: []string{"vendor/jsxgraph/jsxgraphcore.js"},
	},
	"katex": {
		Name:    "katex",
		Bundled: true,
		Global:  "katex",
		Styles:  []string{"vendor/katex/katex.min.css"},
		Scripts: []string{"vendor/katex/katex.min.js"},
	},
	"leaflet": {
		Name:    "leaflet",
		Bundled: false,
		Global:  "L",
		Styles:  []string{"https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.9.4/leaflet.min.css"},
		Scripts: []string{"https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.9.4/leaflet.min.js"},
		Tiles:   true,
	},
	"matter": {
		Name:    "matter",
		Bundled: false,
		Global:  "Matter",
		Scripts: []string{"https://cdnjs.cloudflare.com/ajax/libs/matter-js/0.20.0/matter.min.js"},
	},
	"p5": {
		Name:    "p5",
		Bundled: false,
		Global:  "p5",
		Scripts: []string{"https://cdnjs.cloudflare.com/ajax/libs/p5.js/1.11.3/p5.min.js"},
	},
}

// libOrder keeps injection order predictable.
var libOrder = []string{"three", "tone", "jsxgraph", "katex", "leaflet", "matter", "p5"}

var libAliases = map[string]string{
	"three": "three", "threejs": "three", "three.js": "three",
	"tone": "tone", "tonejs": "tone", "tone.js": "tone",
	"jsxgraph": "jsxgraph", "jxg": "jsxgraph",
	"katex":   "katex",
	"leaflet": "leaflet", "leafletjs": "leaflet", "leaflet.js": "leaflet",
	"matter": "matter", "matterjs": "matter", "matter.js": "matter", "matter-js": "matter",
	"p5": "p5", "p5js": "p5", "p5.js": "p5",
}

// AllowedCDNPrefixes are the external origins an HTML widget may load scripts/styles from.
var AllowedCDNPrefixes = []string{
	"https://cdnjs.cloudflare.com/",
	"https://cdn.jsdelivr.net/",
}

// AllowedTileHosts are map tile image hosts permitted in CSP when a widget uses Leaflet.
var AllowedTileHosts = []string{
	"https://*.tile.openstreetmap.org",
	"https://*.basemaps.cartocdn.com",
}

var (
	metaTagRe      = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
	metaNameLibsRe = regexp.MustCompile(`(?i)\bname\s*=\s*["']?\s*studyhomie-libs\s*["']?`)
	metaContentRe  = regexp.MustCompile(`(?i)\bcontent\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)

	// forbidden APIs in the sandbox
	forbiddenAPIRe = regexp.MustCompile(`(?i)(\bfetch\s*\(|XMLHttpRequest|WebSocket|importScripts|window\.open|localStorage|sessionStorage|document\.cookie|\b(?:window\.)?(?:alert|prompt|confirm)\s*\()`)
	// CSS external imports / urls
	externalCSSRe = regexp.MustCompile(`(?i)(@import\b|url\(\s*["']?(?:https?:|//))`)
	// HTML attributes carrying URLs
	urlAttrRe = regexp.MustCompile(`(?i)\b(src|href|action|data)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	// tag around an attribute (for script/link CDN check)
	externalTagRe = regexp.MustCompile(`(?is)<(script|link)\b[^>]*>`)
)

// ParseLibs extracts the declared libraries from <meta name="studyhomie-libs" content="...">.
// It returns canonical known names (in stable order) and any unrecognized names.
func ParseLibs(html string) (known []string, unknown []string) {
	seen := map[string]bool{}
	seenBad := map[string]bool{}
	for _, tag := range metaTagRe.FindAllString(html, -1) {
		if !metaNameLibsRe.MatchString(tag) {
			continue
		}
		m := metaContentRe.FindStringSubmatch(tag)
		if m == nil {
			continue
		}
		val := m[1]
		if val == "" {
			val = m[2]
		}
		if val == "" {
			val = m[3]
		}
		for _, raw := range strings.FieldsFunc(val, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == ';' }) {
			tok := strings.ToLower(strings.TrimSpace(raw))
			if tok == "" {
				continue
			}
			if canon, ok := libAliases[tok]; ok {
				seen[canon] = true
			} else if !seenBad[tok] {
				seenBad[tok] = true
				unknown = append(unknown, tok)
			}
		}
	}
	for _, name := range libOrder {
		if seen[name] {
			known = append(known, name)
		}
	}
	return known, unknown
}

// IsAllowedCDN reports whether u starts with one of AllowedCDNPrefixes.
func IsAllowedCDN(u string) bool {
	u = strings.TrimSpace(u)
	for _, p := range AllowedCDNPrefixes {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}

// UsesCDN reports whether the HTML widget loads any CDN library (via studyhomie-libs or
// direct whitelisted script/link tags) and whether it needs map tile image hosts.
func UsesCDN(html string) (cdn bool, tiles bool) {
	libs, _ := ParseLibs(html)
	for _, name := range libs {
		lib := Libraries[name]
		if !lib.Bundled {
			cdn = true
		}
		if lib.Tiles {
			tiles = true
		}
	}
	for _, tag := range externalTagRe.FindAllString(html, -1) {
		for _, m := range urlAttrRe.FindAllStringSubmatch(tag, -1) {
			u := attrVal(m)
			if IsAllowedCDN(u) {
				cdn = true
			}
		}
	}
	low := strings.ToLower(html)
	if strings.Contains(low, "tile.openstreetmap.org") || strings.Contains(low, "basemaps.cartocdn.com") {
		tiles = true
	}
	return cdn, tiles
}

func attrVal(m []string) string {
	for i := 2; i < len(m); i++ {
		if m[i] != "" {
			return strings.TrimSpace(m[i])
		}
	}
	return ""
}

// ValidateHTML checks that a generated interactive HTML page is complete, within size limits,
// uses only known libraries and whitelisted CDN script/style URLs, and avoids blocked APIs.
func ValidateHTML(html string) error {
	trimmed := strings.TrimSpace(html)
	low := strings.ToLower(trimmed)
	switch {
	case trimmed == "":
		return fmt.Errorf("the HTML document is empty")
	case len(html) > MaxHTMLBytes:
		return fmt.Errorf("the page is too large (%d KB, limit %d KB)", len(html)>>10, MaxHTMLBytes>>10)
	case !strings.Contains(low, "<script"):
		return fmt.Errorf("the page has no JavaScript, so nothing is interactive")
	case !strings.Contains(low, "</html>"):
		return fmt.Errorf("the HTML document is incomplete")
	}
	if _, unknown := ParseLibs(html); len(unknown) > 0 {
		return fmt.Errorf("unknown studyhomie-libs %q (allowed: %s)", strings.Join(unknown, ", "), strings.Join(libOrder, ", "))
	}
	if m := forbiddenAPIRe.FindString(html); m != "" {
		return fmt.Errorf("the page uses something that is not allowed in the sandbox: %q", m)
	}
	if m := externalCSSRe.FindString(html); m != "" {
		return fmt.Errorf("external CSS imports/urls are not allowed: %q", m)
	}
	// Strip valid whitelisted <script src="..."> and <link href="..."> tags before checking
	// that no other attribute points to http(s): or //.
	scrubbed := externalTagRe.ReplaceAllStringFunc(html, func(tag string) string {
		// Keep the tag as-is if any external URL inside it is not whitelisted, so the check below rejects it.
		for _, m := range urlAttrRe.FindAllStringSubmatch(tag, -1) {
			attr := strings.ToLower(m[1])
			u := attrVal(m)
			if isExternalURL(u) {
				if (attr != "src" && attr != "href") || !IsAllowedCDN(u) {
					return tag
				}
			}
		}
		return ""
	})
	for _, m := range urlAttrRe.FindAllStringSubmatch(scrubbed, -1) {
		u := attrVal(m)
		if isExternalURL(u) {
			return fmt.Errorf("external URL is not allowed in the sandbox: %q (use studyhomie-libs or cdnjs/jsdelivr)", u)
		}
	}
	return nil
}

func isExternalURL(u string) bool {
	low := strings.ToLower(strings.TrimSpace(u))
	return strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "//")
}
