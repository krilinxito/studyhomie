package stats

import (
	"sort"
	"time"
)

// GradeFilter narrows the grade history. Empty fields match everything; From and To are
// inclusive local dates (YYYY-MM-DD).
type GradeFilter struct {
	Topic string
	Area  string
	Kind  string // quiz | feynman
	From  string
	To    string
}

// Grade is one graded attempt: a quiz or a Feynman explanation.
type Grade struct {
	At         time.Time `json:"at"`
	Date       string    `json:"date"`
	Kind       string    `json:"kind"`
	Topic      string    `json:"topic"`
	Title      string    `json:"title"`
	Area       string    `json:"area"`
	Score      int       `json:"score"`
	Difficulty string    `json:"difficulty,omitempty"`
	Correct    int       `json:"correct,omitempty"`
	Questions  int       `json:"questions,omitempty"`
	Partial    bool      `json:"partial,omitempty"`
	Practice   string    `json:"practice,omitempty"`
}

// TopicGrade is where a topic stands: its last and best grade against the pass score.
type TopicGrade struct {
	Topic    string    `json:"topic"`
	Title    string    `json:"title"`
	Area     string    `json:"area"`
	Last     int       `json:"last"`
	LastAt   time.Time `json:"lastAt"`
	Best     int       `json:"best"`
	Attempts int       `json:"attempts"`
	// Passed: the last complete attempt reached the pass score (an incomplete one never counts,
	// as it does not change mastery).
	Passed bool `json:"passed"`
}

// GradeOption is a topic or an area the filters can offer, with how many attempts it has.
type GradeOption struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Count int    `json:"count"`
}

// GradeReport is the grade history served by GET /api/stats/grades.
type GradeReport struct {
	Attempts  []Grade `json:"attempts"` // newest first
	Count     int     `json:"count"`
	Average   int     `json:"average"`
	Best      int     `json:"best"`
	PassScore int     `json:"passScore"`
	// Trend compares the average of the last 30 days with the 30 before them; HasTrend is false
	// when either window has no attempts.
	Last30   int          `json:"last30"`
	Prev30   int          `json:"prev30"`
	Trend    int          `json:"trend"`
	HasTrend bool         `json:"hasTrend"`
	Topics   []TopicGrade `json:"topics"`
	// Options for the filters, from the whole history (not the filtered slice).
	TopicOptions []GradeOption `json:"topicOptions"`
	AreaOptions  []GradeOption `json:"areaOptions"`
}

// IsGrade reports whether an event is a graded attempt.
func IsGrade(ev Event) bool { return ev.Kind == KindQuiz || ev.Kind == KindFeynman }

func eventDate(ev Event) string {
	if ev.Date != "" {
		return ev.Date
	}
	if !ev.At.IsZero() {
		return ev.At.Format("2006-01-02")
	}
	return ""
}

func gradeOf(ev Event) Grade {
	title := ev.Title
	if title == "" {
		title = ev.Topic
	}
	area := ev.Area
	if area == "" {
		area = "general"
	}
	return Grade{
		At: ev.At, Date: eventDate(ev), Kind: ev.Kind, Topic: ev.Topic, Title: title, Area: area,
		Score: max(0, min(ev.Score, 100)), Difficulty: ev.Difficulty, Correct: ev.Correct, Questions: ev.Questions,
		Partial: ev.Partial, Practice: ev.Practice,
	}
}

func (f GradeFilter) match(g Grade) bool {
	switch {
	case f.Topic != "" && g.Topic != f.Topic,
		f.Area != "" && g.Area != f.Area,
		f.Kind != "" && g.Kind != f.Kind,
		f.From != "" && g.Date < f.From,
		f.To != "" && g.Date > f.To:
		return false
	}
	return true
}

// Grades builds the grade history from the recorded events.
func Grades(d Data, f GradeFilter, passScore int, now time.Time) GradeReport {
	if passScore <= 0 || passScore > 100 {
		passScore = 70
	}
	rep := GradeReport{PassScore: passScore, Attempts: []Grade{}, Topics: []TopicGrade{}, TopicOptions: []GradeOption{}, AreaOptions: []GradeOption{}}
	topicOpt := map[string]*GradeOption{}
	areaOpt := map[string]*GradeOption{}
	var all []Grade
	for _, ev := range d.Events {
		if !IsGrade(ev) {
			continue
		}
		g := gradeOf(ev)
		if t := topicOpt[g.Topic]; t != nil {
			t.Count++
			t.Title = g.Title // the latest title wins
		} else if g.Topic != "" {
			topicOpt[g.Topic] = &GradeOption{ID: g.Topic, Title: g.Title, Count: 1}
		}
		if a := areaOpt[g.Area]; a != nil {
			a.Count++
		} else {
			areaOpt[g.Area] = &GradeOption{ID: g.Area, Title: g.Area, Count: 1}
		}
		if f.match(g) {
			all = append(all, g)
		}
	}
	for _, o := range topicOpt {
		rep.TopicOptions = append(rep.TopicOptions, *o)
	}
	sort.Slice(rep.TopicOptions, func(i, j int) bool { return rep.TopicOptions[i].Title < rep.TopicOptions[j].Title })
	for _, o := range areaOpt {
		rep.AreaOptions = append(rep.AreaOptions, *o)
	}
	sort.Slice(rep.AreaOptions, func(i, j int) bool { return rep.AreaOptions[i].ID < rep.AreaOptions[j].ID })

	// oldest first, so "last" per topic is the latest one seen
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.Before(all[j].At) })
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	from30 := today.AddDate(0, 0, -29).Format("2006-01-02")
	from60 := today.AddDate(0, 0, -59).Format("2006-01-02")
	sum, s30, n30, s60, n60 := 0, 0, 0, 0, 0
	topics := map[string]*TopicGrade{}
	var order []string
	for _, g := range all {
		sum += g.Score
		rep.Best = max(rep.Best, g.Score)
		switch {
		case g.Date >= from30:
			s30 += g.Score
			n30++
		case g.Date >= from60:
			s60 += g.Score
			n60++
		}
		t := topics[g.Topic]
		if t == nil {
			t = &TopicGrade{Topic: g.Topic}
			topics[g.Topic] = t
			order = append(order, g.Topic)
		}
		t.Title, t.Area = g.Title, g.Area
		t.Attempts++
		t.Last, t.LastAt = g.Score, g.At
		t.Best = max(t.Best, g.Score)
		if !g.Partial {
			t.Passed = g.Score >= passScore
		}
	}
	rep.Count = len(all)
	if rep.Count > 0 {
		rep.Average = (sum + rep.Count/2) / rep.Count
	}
	if n30 > 0 {
		rep.Last30 = (s30 + n30/2) / n30
	}
	if n60 > 0 {
		rep.Prev30 = (s60 + n60/2) / n60
	}
	if n30 > 0 && n60 > 0 {
		rep.HasTrend = true
		rep.Trend = rep.Last30 - rep.Prev30
	}
	for i := len(all) - 1; i >= 0; i-- {
		rep.Attempts = append(rep.Attempts, all[i])
	}
	for _, id := range order {
		rep.Topics = append(rep.Topics, *topics[id])
	}
	// most recently practiced first
	sort.SliceStable(rep.Topics, func(i, j int) bool { return rep.Topics[i].LastAt.After(rep.Topics[j].LastAt) })
	return rep
}

// DayGrade is a grade of one day, for the tooltip of the streak calendar.
type DayGrade struct {
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Score   int    `json:"score"`
	Partial bool   `json:"partial,omitempty"`
}
