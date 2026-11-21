// Package sources finds and checks study material in public catalogues: Wikipedia, OpenAlex
// (papers), Crossref (DOIs), Open Library (books) and YouTube (oEmbed without a key; the Data API
// and Brave Search with one). It never makes a URL up: every result comes from one of them.
package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/krilinxito/studyhomie/internal/vault"
)

// Client talks to the catalogues. The base URLs are fields so tests can point them at a local server.
type Client struct {
	HTTP       *http.Client
	YouTubeKey string // optional: search videos (YouTube Data API)
	BraveKey   string // optional: search the web (Brave Search)

	Wikipedia     string // with %s for the language, e.g. https://%s.wikipedia.org
	OpenAlex      string
	Crossref      string
	OpenLibrary   string
	YouTubeOEmbed string
	YouTubeAPI    string
	Brave         string
}

// New is a client for the real services.
func New(youtubeKey, braveKey string) *Client {
	return &Client{
		HTTP:       &http.Client{Timeout: 12 * time.Second},
		YouTubeKey: strings.TrimSpace(youtubeKey), BraveKey: strings.TrimSpace(braveKey),
		Wikipedia:     "https://%s.wikipedia.org",
		OpenAlex:      "https://api.openalex.org",
		Crossref:      "https://api.crossref.org",
		OpenLibrary:   "https://openlibrary.org",
		YouTubeOEmbed: "https://www.youtube.com/oembed",
		YouTubeAPI:    "https://www.googleapis.com/youtube/v3",
		Brave:         "https://api.search.brave.com",
	}
}

const userAgent = "StudyHomie (study assistant; https://studyhomie.maxi42.space)"

// errStatus is an answer that was not 200.
type errStatus int

func (e errStatus) Error() string { return "status " + strconv.Itoa(int(e)) }

func (c *Client) getJSON(ctx context.Context, u string, hdr map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return errStatus(resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

// Query is what to look for.
type Query struct {
	Text  string
	Lang  string   // two letters: es, en, fr, zh, pt
	Kinds []string // video | web | book | paper; empty means all
	Easy  bool     // prefer introductory material: no papers
	Limit int      // per catalogue; 0 means 4
}

func (q Query) wants(kind string) bool {
	if q.Easy && kind == "paper" {
		return false
	}
	if len(q.Kinds) == 0 {
		return true
	}
	for _, k := range q.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Search asks every catalogue that fits the query at once and returns what they found, without
// repeats. A catalogue that fails or is slow is skipped: the others still answer.
func (c *Client) Search(ctx context.Context, q Query) []vault.Resource {
	if q.Limit <= 0 {
		q.Limit = 4
	}
	if q.Lang == "" {
		q.Lang = "es"
	}
	q.Text = strings.TrimSpace(q.Text)
	if q.Text == "" {
		return nil
	}
	type job func(context.Context, Query) ([]vault.Resource, error)
	var jobs []job
	if q.wants("web") {
		jobs = append(jobs, c.wikipedia)
		if c.BraveKey != "" {
			jobs = append(jobs, c.brave)
		}
	}
	if q.wants("paper") {
		jobs = append(jobs, c.openAlex)
	}
	if q.wants("book") {
		jobs = append(jobs, c.openLibrary)
	}
	if q.wants("video") && c.YouTubeKey != "" {
		jobs = append(jobs, c.youtube)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	results := make([][]vault.Resource, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rs, err := j(ctx, q); err == nil {
				results[i] = rs
			}
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	var out []vault.Resource
	for _, rs := range results {
		for _, r := range rs {
			k := vault.SourceKey(r)
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			r.Reliability = Reliability(r)
			out = append(out, r)
		}
	}
	return out
}

func (c *Client) wikipedia(ctx context.Context, q Query) ([]vault.Resource, error) {
	base := fmt.Sprintf(c.Wikipedia, q.Lang)
	var out struct {
		Pages []struct {
			Key         string `json:"key"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Excerpt     string `json:"excerpt"`
		} `json:"pages"`
	}
	u := base + "/w/rest.php/v1/search/page?" + url.Values{"q": {q.Text}, "limit": {strconv.Itoa(min(q.Limit, 3))}}.Encode()
	if err := c.getJSON(ctx, u, nil, &out); err != nil {
		return nil, err
	}
	var rs []vault.Resource
	for _, p := range out.Pages {
		sum := p.Description
		if sum == "" {
			sum = stripTags(p.Excerpt)
		}
		rs = append(rs, vault.Resource{Type: "web", Title: p.Title, URL: base + "/wiki/" + url.PathEscape(p.Key), Author: "Wikipedia", Lang: q.Lang, Summary: sum})
	}
	return rs, nil
}

func (c *Client) openAlex(ctx context.Context, q Query) ([]vault.Resource, error) {
	var out struct {
		Results []struct {
			Title      string `json:"display_name"`
			Year       int    `json:"publication_year"`
			DOI        string `json:"doi"`
			Cited      int    `json:"cited_by_count"`
			Type       string `json:"type"`
			Language   string `json:"language"`
			OpenAccess struct {
				URL string `json:"oa_url"`
			} `json:"open_access"`
			Authorships []struct {
				Author struct {
					Name string `json:"display_name"`
				} `json:"author"`
			} `json:"authorships"`
			Primary struct {
				Source struct {
					Name string `json:"display_name"`
				} `json:"source"`
			} `json:"primary_location"`
		} `json:"results"`
	}
	v := url.Values{"search": {q.Text}, "per-page": {strconv.Itoa(q.Limit)}, "sort": {"relevance_score:desc"}}
	if err := c.getJSON(ctx, c.OpenAlex+"/works?"+v.Encode(), nil, &out); err != nil {
		return nil, err
	}
	var rs []vault.Resource
	for _, w := range out.Results {
		if w.Title == "" {
			continue
		}
		r := vault.Resource{Type: "paper", Title: w.Title, Year: w.Year, Citations: w.Cited, Lang: w.Language, Level: "avanzado"}
		r.DOI = vault.DOIOf(w.DOI)
		switch {
		case w.OpenAccess.URL != "":
			r.URL = w.OpenAccess.URL
		case r.DOI != "":
			r.URL = "https://doi.org/" + r.DOI
		}
		if r.URL == "" {
			continue // nothing the learner can open
		}
		r.Author = authors(len(w.Authorships), func(i int) string { return w.Authorships[i].Author.Name })
		if s := w.Primary.Source.Name; s != "" {
			r.Summary = s
		}
		rs = append(rs, r)
	}
	return rs, nil
}

func (c *Client) openLibrary(ctx context.Context, q Query) ([]vault.Resource, error) {
	var out struct {
		Docs []struct {
			Key      string   `json:"key"`
			Title    string   `json:"title"`
			Authors  []string `json:"author_name"`
			Year     int      `json:"first_publish_year"`
			ISBN     []string `json:"isbn"`
			Language []string `json:"language"`
		} `json:"docs"`
	}
	v := url.Values{"q": {q.Text}, "limit": {strconv.Itoa(min(q.Limit, 4))}, "fields": {"key,title,author_name,first_publish_year,isbn,language"}}
	if err := c.getJSON(ctx, c.OpenLibrary+"/search.json?"+v.Encode(), nil, &out); err != nil {
		return nil, err
	}
	var rs []vault.Resource
	for _, d := range out.Docs {
		if d.Title == "" || d.Key == "" {
			continue
		}
		r := vault.Resource{Type: "book", Title: d.Title, URL: c.OpenLibrary + d.Key, Year: d.Year}
		r.Author = authors(len(d.Authors), func(i int) string { return d.Authors[i] })
		if len(d.ISBN) > 0 {
			r.ISBN = d.ISBN[0]
		}
		if len(d.Language) > 0 {
			r.Lang = lang3(d.Language[0])
		}
		rs = append(rs, r)
	}
	return rs, nil
}

func (c *Client) youtube(ctx context.Context, q Query) ([]vault.Resource, error) {
	var found struct {
		Items []struct {
			ID struct {
				VideoID string `json:"videoId"`
			} `json:"id"`
			Snippet struct {
				Title       string `json:"title"`
				Channel     string `json:"channelTitle"`
				Published   string `json:"publishedAt"`
				Description string `json:"description"`
			} `json:"snippet"`
		} `json:"items"`
	}
	v := url.Values{"part": {"snippet"}, "type": {"video"}, "maxResults": {strconv.Itoa(q.Limit)}, "q": {q.Text}, "relevanceLanguage": {q.Lang}, "safeSearch": {"strict"}, "videoEmbeddable": {"true"}, "key": {c.YouTubeKey}}
	if err := c.getJSON(ctx, c.YouTubeAPI+"/search?"+v.Encode(), nil, &found); err != nil {
		return nil, err
	}
	var ids []string
	byID := map[string]*vault.Resource{}
	var rs []vault.Resource
	for _, it := range found.Items {
		if it.ID.VideoID == "" {
			continue
		}
		r := vault.Resource{Type: "video", Title: html(it.Snippet.Title), URL: "https://www.youtube.com/watch?v=" + it.ID.VideoID, Author: html(it.Snippet.Channel), Summary: html(it.Snippet.Description), Lang: q.Lang}
		if len(it.Snippet.Published) >= 4 {
			r.Year, _ = strconv.Atoi(it.Snippet.Published[:4])
		}
		rs = append(rs, r)
		ids = append(ids, it.ID.VideoID)
	}
	for i := range rs {
		byID[vault.YouTubeID(rs[i].URL)] = &rs[i]
	}
	if len(ids) > 0 {
		var det struct {
			Items []struct {
				ID      string `json:"id"`
				Content struct {
					Duration string `json:"duration"`
				} `json:"contentDetails"`
			} `json:"items"`
		}
		v := url.Values{"part": {"contentDetails"}, "id": {strings.Join(ids, ",")}, "key": {c.YouTubeKey}}
		if c.getJSON(ctx, c.YouTubeAPI+"/videos?"+v.Encode(), nil, &det) == nil {
			for _, it := range det.Items {
				if r := byID[it.ID]; r != nil {
					r.Duration = isoDuration(it.Content.Duration)
				}
			}
		}
	}
	return rs, nil
}

func (c *Client) brave(ctx context.Context, q Query) ([]vault.Resource, error) {
	var out struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	v := url.Values{"q": {q.Text}, "count": {strconv.Itoa(q.Limit)}, "search_lang": {q.Lang}, "safesearch": {"strict"}}
	if err := c.getJSON(ctx, c.Brave+"/res/v1/web/search?"+v.Encode(), map[string]string{"X-Subscription-Token": c.BraveKey}, &out); err != nil {
		return nil, err
	}
	var rs []vault.Resource
	for _, r := range out.Web.Results {
		if r.URL == "" || r.Title == "" {
			continue
		}
		typ := "web"
		if vault.YouTubeID(r.URL) != "" {
			typ = "video"
		}
		rs = append(rs, vault.Resource{Type: typ, Title: stripTags(r.Title), URL: r.URL, Summary: stripTags(r.Description)})
	}
	return rs, nil
}

// Verify checks a resource the AI proposed and completes it from its catalogue: a YouTube video
// through oEmbed (it must exist; its real title and channel), a paper through Crossref by its DOI.
// ok is false only when the catalogue says for sure it does not exist; a network failure keeps it.
func (c *Client) Verify(ctx context.Context, r vault.Resource) (vault.Resource, bool) {
	if id := vault.YouTubeID(r.URL); id != "" {
		var out struct {
			Title  string `json:"title"`
			Author string `json:"author_name"`
		}
		err := c.getJSON(ctx, c.YouTubeOEmbed+"?"+url.Values{"url": {"https://www.youtube.com/watch?v=" + id}, "format": {"json"}}.Encode(), nil, &out)
		if st, ok := err.(errStatus); ok && (st == http.StatusNotFound || st == http.StatusBadRequest || st == http.StatusUnauthorized) {
			return r, false // gone, private or never existed
		}
		if err == nil {
			r.Type = "video"
			if out.Title != "" {
				r.Title = out.Title
			}
			if out.Author != "" {
				r.Author = out.Author
			}
		}
	} else if d := vault.DOIOf(r.DOI + " " + r.URL); d != "" {
		var out struct {
			Message struct {
				Title     []string `json:"title"`
				Container []string `json:"container-title"`
				Issued    struct {
					Parts [][]int `json:"date-parts"`
				} `json:"issued"`
				Author []struct {
					Given  string `json:"given"`
					Family string `json:"family"`
				} `json:"author"`
				Cited int `json:"is-referenced-by-count"`
			} `json:"message"`
		}
		err := c.getJSON(ctx, c.Crossref+"/works/"+url.PathEscape(d), nil, &out)
		if st, ok := err.(errStatus); ok && st == http.StatusNotFound {
			return r, false
		}
		if err == nil {
			m := out.Message
			r.DOI = d
			if r.Type == "" || r.Type == "web" {
				r.Type = "paper"
			}
			if len(m.Title) > 0 && m.Title[0] != "" {
				r.Title = m.Title[0]
			}
			if len(m.Issued.Parts) > 0 && len(m.Issued.Parts[0]) > 0 {
				r.Year = m.Issued.Parts[0][0]
			}
			r.Author = authors(len(m.Author), func(i int) string { return strings.TrimSpace(m.Author[i].Given + " " + m.Author[i].Family) })
			r.Citations = max(r.Citations, m.Cited)
			if r.Summary == "" && len(m.Container) > 0 {
				r.Summary = m.Container[0]
			}
		}
	}
	r.Reliability = Reliability(r)
	return r, true
}

// Hosts and channels known for careful, teaching-minded material.
var (
	trustedHosts = []string{
		"wikipedia.org", "khanacademy.org", "ocw.mit.edu", "mit.edu", "openstax.org", "britannica.com", "plato.stanford.edu",
		"nature.com", "science.org", "sciencedirect.com", "springer.com", "link.springer.com", "wiley.com", "tandfonline.com",
		"jstor.org", "cambridge.org", "oup.com", "academic.oup.com", "ieee.org", "acm.org", "aps.org", "royalsocietypublishing.org",
		"nih.gov", "ncbi.nlm.nih.gov", "who.int", "nasa.gov", "arxiv.org", "doi.org", "openlibrary.org", "coursera.org", "edx.org",
		"brilliant.org", "betterexplained.com", "mathworld.wolfram.com", "phet.colorado.edu", "desmos.com", "geogebra.org",
	}
	weakHosts       = []string{"medium.com", "blogspot.com", "wordpress.com", "quora.com", "reddit.com", "tiktok.com", "facebook.com", "pinterest.com"}
	trustedChannels = []string{
		"3blue1brown", "khan academy", "crashcourse", "crash course", "mit opencourseware", "numberphile", "computerphile", "veritasium",
		"minutephysics", "pbs space time", "sixty symbols", "stanford", "harvard", "yale courses", "the royal institution", "ted-ed",
		"statquest", "kurzgesagt", "smarter every day", "standupmaths", "mathologer", "physics girl", "nptel", "derivando", "quantumfracture",
		"date un voltio", "unicoos", "julioprofe", "math et tiques", "e-penser", "science étonnante", "nerdologia", "matemática rio",
	}
	eduHost = regexp.MustCompile(`(?i)(^|\.)(edu|gov|ac\.[a-z]{2}|edu\.[a-z]{2}|gob\.[a-z]{2}|gov\.[a-z]{2})$`)
)

// Reliability rates a source: alta (peer-reviewed, academic or institutional, well-known teaching
// channels), baja (open blogs and social networks), media otherwise.
func Reliability(r vault.Resource) string {
	host := ""
	if u, err := url.Parse(r.URL); err == nil {
		host = strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	}
	has := func(list []string) bool {
		for _, h := range list {
			if host == h || strings.HasSuffix(host, "."+h) {
				return true
			}
		}
		return false
	}
	switch {
	case r.Type == "paper" && (r.DOI != "" || r.Citations >= 10):
		return "alta"
	case eduHost.MatchString(host), has(trustedHosts):
		return "alta"
	case r.Type == "video":
		ch := strings.ToLower(r.Author)
		for _, c := range trustedChannels {
			if ch != "" && strings.Contains(ch, c) {
				return "alta"
			}
		}
		return "media"
	case has(weakHosts):
		return "baja"
	}
	return "media"
}

// authors lists up to three names, then "et al.".
func authors(n int, name func(int) string) string {
	var names []string
	for i := 0; i < n && len(names) < 3; i++ {
		if s := strings.TrimSpace(name(i)); s != "" {
			names = append(names, s)
		}
	}
	out := strings.Join(names, ", ")
	if n > 3 && out != "" {
		out += " et al."
	}
	return out
}

var isoDurRe = regexp.MustCompile(`^PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?$`)

// isoDuration turns YouTube's PT1H2M3S into 1:02:03 (and PT12M4S into 12:04).
func isoDuration(s string) string {
	m := isoDurRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	n := func(x string) int { v, _ := strconv.Atoi(x); return v }
	h, mi, se := n(m[1]), n(m[2]), n(m[3])
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, mi, se)
	}
	return fmt.Sprintf("%d:%02d", mi, se)
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

func stripTags(s string) string { return strings.TrimSpace(html(tagRe.ReplaceAllString(s, ""))) }

var entities = strings.NewReplacer("&amp;", "&", "&quot;", `"`, "&#39;", "'", "&lt;", "<", "&gt;", ">", "&nbsp;", " ")

func html(s string) string { return entities.Replace(s) }

// lang3 maps Open Library's three-letter languages to the app's codes.
func lang3(s string) string {
	switch s {
	case "spa":
		return "es"
	case "eng":
		return "en"
	case "fre", "fra":
		return "fr"
	case "chi", "zho":
		return "zh"
	case "por":
		return "pt"
	}
	return s
}
