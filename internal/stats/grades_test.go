package stats

import (
	"testing"
	"time"
)

func TestGradesFiltersSummarizesAndJudgesTopics(t *testing.T) {
	now := day(2026, 11, 21, 12)
	at := func(daysAgo int) time.Time { return now.AddDate(0, 0, -daysAgo) }
	ev := func(daysAgo int, kind, topic string, score int, partial bool) Event {
		return Event{At: at(daysAgo), Date: at(daysAgo).Format("2006-01-02"), Kind: kind, Topic: topic, Title: "T " + topic, Area: "matematicas", Score: score, Partial: partial}
	}
	d := Data{Events: []Event{
		ev(40, KindQuiz, "bayes", 50, false),
		{At: at(35), Date: at(35).Format("2006-01-02"), Kind: KindRead, Topic: "bayes"}, // not a grade
		ev(10, KindQuiz, "bayes", 80, false),
		ev(5, KindFeynman, "bayes", 60, false),
		ev(2, KindQuiz, "bayes", 95, true), // incomplete: never decides passing
		ev(1, KindQuiz, "cumple", 90, false),
		{At: at(1), Date: at(1).Format("2006-01-02"), Kind: KindQuiz, Topic: "fourier", Title: "Fourier", Area: "fisica", Score: 40},
	}}

	all := Grades(d, GradeFilter{}, 70, now)
	if all.Count != 6 || all.Best != 95 || all.Average != (50+80+60+95+90+40+3)/6 {
		t.Fatalf("summary: count %d best %d avg %d", all.Count, all.Best, all.Average)
	}
	if all.Attempts[0].Topic != "fourier" && all.Attempts[0].Topic != "cumple" {
		t.Errorf("newest first: %+v", all.Attempts[0])
	}
	if !all.HasTrend || all.Prev30 != 50 || all.Last30 != (80+60+95+90+40+2)/5 || all.Trend != all.Last30-50 {
		t.Errorf("trend: %+v", all)
	}
	topics := map[string]TopicGrade{}
	for _, tg := range all.Topics {
		topics[tg.Topic] = tg
	}
	if b := topics["bayes"]; b.Attempts != 4 || b.Last != 95 || b.Best != 95 || b.Passed {
		t.Errorf("bayes: last complete was 60 < 70, so not passed: %+v", b)
	}
	if c := topics["cumple"]; !c.Passed || c.Last != 90 {
		t.Errorf("cumple: %+v", c)
	}
	if len(all.TopicOptions) != 3 || len(all.AreaOptions) != 2 {
		t.Errorf("options: %+v %+v", all.TopicOptions, all.AreaOptions)
	}

	f := Grades(d, GradeFilter{Topic: "bayes", Kind: KindQuiz, From: at(20).Format("2006-01-02")}, 70, now)
	if f.Count != 2 || f.HasTrend || len(f.TopicOptions) != 3 {
		t.Errorf("filtered: %+v", f)
	}
	if a := Grades(d, GradeFilter{Area: "fisica"}, 70, now); a.Count != 1 || a.Attempts[0].Title != "Fourier" {
		t.Errorf("area filter: %+v", a)
	}

	// the streak calendar lists each day's grades
	sum := Compute(d, nil, nil, 50, 70, now)
	var dayOne []DayGrade
	for _, c := range sum.Calendar {
		if c.Date == at(1).Format("2006-01-02") {
			dayOne = c.Grades
		}
	}
	if len(dayOne) != 2 {
		t.Errorf("calendar grades: %+v", dayOne)
	}
}
