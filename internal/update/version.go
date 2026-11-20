package update

import (
	"strconv"
	"strings"
)

// semver is a parsed version such as v1.2.3 or v1.2.3-rc.1 (build metadata is ignored).
type semver struct {
	nums [3]int
	pre  []string
}

func parse(v string) (semver, bool) {
	var s semver
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		if v[i+1:] == "" {
			return s, false
		}
		s.pre = strings.Split(v[i+1:], ".")
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return s, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" {
			return s, false
		}
		s.nums[i] = n
	}
	return s, true
}

// Valid reports whether v is a release version. Development builds ("dev") are not.
func Valid(v string) bool {
	_, ok := parse(v)
	return ok
}

// Compare orders two versions by semver precedence: -1, 0 or 1. Invalid versions sort first.
func Compare(a, b string) int {
	x, okx := parse(a)
	y, oky := parse(b)
	switch {
	case !okx && !oky:
		return 0
	case !okx:
		return -1
	case !oky:
		return 1
	}
	for i := range x.nums {
		if c := cmpInt(x.nums[i], y.nums[i]); c != 0 {
			return c
		}
	}
	// a version without a prerelease part is newer than any prerelease of it
	switch {
	case len(x.pre) == 0 && len(y.pre) == 0:
		return 0
	case len(x.pre) == 0:
		return 1
	case len(y.pre) == 0:
		return -1
	}
	for i := 0; i < len(x.pre) && i < len(y.pre); i++ {
		if c := cmpIdent(x.pre[i], y.pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(x.pre), len(y.pre))
}

// Newer reports whether latest is a newer release than current.
func Newer(latest, current string) bool {
	return Valid(latest) && Valid(current) && Compare(latest, current) > 0
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpIdent compares prerelease identifiers: numbers numerically, and below any word.
func cmpIdent(a, b string) int {
	na, ea := strconv.Atoi(a)
	nb, eb := strconv.Atoi(b)
	switch {
	case ea == nil && eb == nil:
		return cmpInt(na, nb)
	case ea == nil:
		return -1
	case eb == nil:
		return 1
	}
	return strings.Compare(a, b)
}
