package material

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Chunk is a piece of the source document.
type Chunk struct {
	ID      int
	Heading string
	Text    string
}

// Split cuts text into chunks of about max characters, preferring heading and
// paragraph boundaries. A heading starts a new chunk when the current one is
// already reasonably full, so related paragraphs stay together.
func Split(text string, max int) []Chunk {
	if max < 200 {
		max = 200
	}
	var chunks []Chunk
	var cur strings.Builder
	heading, curHeading := "", ""
	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			chunks = append(chunks, Chunk{ID: len(chunks), Heading: curHeading, Text: t})
		}
		cur.Reset()
	}
	add := func(p string) {
		if cur.Len() > 0 && cur.Len()+len(p) > max {
			flush()
		}
		if cur.Len() == 0 {
			curHeading = heading
		}
		cur.WriteString(p + "\n\n")
	}
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if strings.HasPrefix(para, "#") {
			heading = strings.TrimSpace(strings.TrimLeft(strings.SplitN(para, "\n", 2)[0], "# "))
			if cur.Len() > max/3 {
				flush()
			}
		}
		// a single huge paragraph is cut near a sentence or word boundary
		for len(para) > max {
			cut := strings.LastIndexAny(para[:max], ". ")
			if cut < max/2 {
				cut = max
			}
			add(para[:cut])
			para = strings.TrimSpace(para[cut:])
		}
		add(para)
	}
	flush()
	return chunks
}

func tokens(s string) []string {
	f := func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), f) {
		if len([]rune(w)) >= 3 {
			out = append(out, stem(w))
		}
	}
	return out
}

// stem is a crude plural trimmer, enough to match "probabilidades" with "probabilidad".
func stem(w string) string {
	for _, suf := range []string{"es", "s"} {
		if len(w) > len(suf)+3 && strings.HasSuffix(w, suf) {
			return strings.TrimSuffix(w, suf)
		}
	}
	return w
}

// Top returns the k chunks that best match the query (TF-IDF), in document order.
func Top(chunks []Chunk, query string, k int) []Chunk {
	q := tokens(query)
	if len(chunks) == 0 || len(q) == 0 || k <= 0 {
		return nil
	}
	df := map[string]int{}
	tfs := make([]map[string]int, len(chunks))
	for i, c := range chunks {
		tf := map[string]int{}
		for _, t := range tokens(c.Heading + " " + c.Text) {
			tf[t]++
		}
		tfs[i] = tf
		for t := range tf {
			df[t]++
		}
	}
	type scored struct {
		i int
		s float64
	}
	var sc []scored
	for i := range chunks {
		var s float64
		for _, t := range q {
			if tf := tfs[i][t]; tf > 0 {
				idf := math.Log(1 + float64(len(chunks))/float64(df[t]))
				s += (1 + math.Log(float64(tf))) * idf
			}
		}
		if s > 0 {
			sc = append(sc, scored{i, s})
		}
	}
	sort.SliceStable(sc, func(a, b int) bool { return sc[a].s > sc[b].s })
	if len(sc) > k {
		sc = sc[:k]
	}
	sort.Slice(sc, func(a, b int) bool { return sc[a].i < sc[b].i })
	out := make([]Chunk, len(sc))
	for i, s := range sc {
		out[i] = chunks[s.i]
	}
	return out
}

// Outline condenses the whole document for planning: each chunk's heading and
// opening, thinned evenly if the result would exceed budget characters.
func Outline(chunks []Chunk, budget int) string {
	const head = 300
	var lines []string
	total := 0
	for _, c := range chunks {
		t := strings.Join(strings.Fields(c.Text), " ")
		if r := []rune(t); len(r) > head {
			t = string(r[:head]) + "…"
		}
		h := c.Heading
		if h == "" {
			h = "(sin título)"
		}
		l := "- [" + h + "] " + t + "\n"
		lines = append(lines, l)
		total += len(l)
	}
	step := 1
	if budget > 0 && total > budget {
		step = int(math.Ceil(float64(total) / float64(budget)))
	}
	var b strings.Builder
	for i := 0; i < len(lines); i += step {
		b.WriteString(lines[i])
	}
	return b.String()
}
