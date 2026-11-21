package stats

import (
	"testing"
	"time"

	"github.com/krilinxito/studyhomie/internal/vault"
)

func day(year, month, d, hour int) time.Time {
	return time.Date(year, time.Month(month), d, hour, 0, 0, 0, time.Local)
}

func TestStreakFreezeEarnConsumeAndLoss(t *testing.T) {
	// 7 consecutive days hitting goal=50 earns 1 freeze; day 8 missed consumes it;
	// day 9 hits goal -> streak continues to 8!
	start := day(2026, 11, 1, 10)
	var evs []Event
	for i := 0; i < 7; i++ {
		ts := start.AddDate(0, 0, i)
		evs = append(evs, Event{At: ts, Date: ts.Format("2006-01-02"), Kind: KindGuide, Area: "musica", XP: 50, Seconds: 300})
	}
	// Day 8 (Nov 8) missed; Day 9 (Nov 9) hits goal.
	day9 := start.AddDate(0, 0, 8)
	evs = append(evs, Event{At: day9, Date: day9.Format("2006-01-02"), Kind: KindQuiz, Area: "musica", XP: 60, Seconds: 240})

	sum := Compute(Data{Events: evs}, nil, nil, 50, 70, day(2026, 11, 9, 15))
	if sum.Streak != 8 || sum.Longest != 8 {
		t.Fatalf("streak=%d longest=%d, want 8/8", sum.Streak, sum.Longest)
	}
	if sum.Freezes != 0 {
		t.Errorf("freeze should have been consumed on Nov 8, got %d", sum.Freezes)
	}
	if !sum.GoalMet || sum.AtRisk || sum.LostStreak {
		t.Errorf("unexpected flags: goalMet=%v atRisk=%v lost=%v", sum.GoalMet, sum.AtRisk, sum.LostStreak)
	}
	// Verify Nov 8 is marked frozen in the calendar.
	foundFrozen := false
	for _, c := range sum.Calendar {
		if c.Date == "2026-11-08" && c.Frozen {
			foundFrozen = true
		}
	}
	if !foundFrozen {
		t.Error("2026-11-08 should be marked frozen in calendar")
	}

	// Next day (Nov 10) at 19:00 without XP yet -> streak stays 8, AtRisk = true!
	sumRisk := Compute(Data{Events: evs}, nil, nil, 50, 70, day(2026, 11, 10, 19))
	if sumRisk.Streak != 8 || !sumRisk.AtRisk || sumRisk.LostStreak {
		t.Errorf("atRisk check failed: streak=%d atRisk=%v lost=%v", sumRisk.Streak, sumRisk.AtRisk, sumRisk.LostStreak)
	}

	// Day after (Nov 11) with no freeze left -> streak lost on Nov 10 -> LostStreak = true, Streak = 0!
	sumLost := Compute(Data{Events: evs}, nil, nil, 50, 70, day(2026, 11, 11, 10))
	if sumLost.Streak != 0 || !sumLost.LostStreak || sumLost.Longest != 8 {
		t.Errorf("lost streak check failed: streak=%d lost=%v longest=%d", sumLost.Streak, sumLost.LostStreak, sumLost.Longest)
	}
}

func TestFreezeCapsAtMaxFreezes(t *testing.T) {
	start := day(2026, 10, 1, 10)
	var evs []Event
	// 25 consecutive days -> would earn 3 freezes, must cap at MaxFreezes (2).
	for i := 0; i < 25; i++ {
		ts := start.AddDate(0, 0, i)
		evs = append(evs, Event{At: ts, Date: ts.Format("2006-01-02"), Kind: KindGuide, Area: "fisica", XP: 50})
	}
	sum := Compute(Data{Events: evs}, nil, nil, 50, 70, start.AddDate(0, 0, 24))
	if sum.Freezes != MaxFreezes || sum.NextFreezeIn != 0 {
		t.Errorf("freezes=%d nextFreezeIn=%d, want %d/0", sum.Freezes, sum.NextFreezeIn, MaxFreezes)
	}
}

func TestRecordDedupAndPersistence(t *testing.T) {
	root := t.TempDir()
	now := day(2026, 11, 20, 14)

	ok, err := Record(root, Event{At: now, Kind: KindRead, Topic: "bayes", Title: "Bayes", Area: "matemáticas"})
	if err != nil || !ok {
		t.Fatalf("first read: ok=%v err=%v", ok, err)
	}
	// Duplicate read same day must be ignored.
	ok, err = Record(root, Event{At: now.Add(time.Minute), Kind: KindRead, Topic: "bayes", Title: "Bayes", Area: "matematicas"})
	if err != nil || ok {
		t.Fatalf("duplicate read same day should be skipped: ok=%v err=%v", ok, err)
	}
	// Doubts capped at 3 per day.
	for i := 0; i < 4; i++ {
		ok, err := Record(root, Event{At: now.Add(time.Duration(i) * time.Minute), Kind: KindDoubt, Topic: "bayes", Area: "matematicas"})
		if err != nil {
			t.Fatal(err)
		}
		if (i < 3 && !ok) || (i == 3 && ok) {
			t.Errorf("doubt %d: ok=%v", i, ok)
		}
	}

	data, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Events) != 4 {
		t.Fatalf("events = %d, want 4", len(data.Events))
	}
	if data.Events[0].Area != "matematicas" || data.Events[0].XP != XPFor(KindRead, 0) {
		t.Errorf("first event normalized badly: %+v", data.Events[0])
	}
}

func TestAreaStatsCombineEventsAndVault(t *testing.T) {
	now := day(2026, 11, 20, 12)
	notes := []vault.Note{
		{Slug: "escalas", Meta: vault.Meta{Title: "Escalas", Status: "explained", Mastery: 90, Area: "musica"}},
		{Slug: "acordes", Meta: vault.Meta{Title: "Acordes", Status: "tested", Mastery: 60, Area: "musica"}},
		{Slug: "ohm", Meta: vault.Meta{Title: "Ley de Ohm", Status: "prepared", Area: "electronica"}},
	}
	concepts := []vault.Concept{
		{Slug: "semitono", Meta: vault.ConceptMeta{Title: "Semitono", Guides: []string{"escalas"}}},
		{Slug: "resistencia", Meta: vault.ConceptMeta{Title: "Resistencia", Category: "electronica"}},
	}
	d := Data{Events: []Event{
		{At: now, Date: "2026-11-20", Kind: KindFeynman, Topic: "escalas", Area: "musica", XP: 45, Score: 80, Seconds: 360},
	}}
	sum := Compute(d, notes, concepts, 50, 70, now)
	if len(sum.Areas) != 2 {
		t.Fatalf("areas = %+v", sum.Areas)
	}
	if sum.Areas[0].Area != "musica" || sum.Areas[0].XP != 45 || sum.Areas[0].Guides != 2 || sum.Areas[0].MasteredGuides != 1 || sum.Areas[0].AvgMastery != 75 || sum.Areas[0].Concepts != 1 || sum.Areas[0].Minutes != 6 {
		t.Errorf("musica area stat = %+v", sum.Areas[0])
	}
	if sum.Areas[1].Area != "electronica" || sum.Areas[1].Guides != 1 || sum.Areas[1].Concepts != 1 {
		t.Errorf("electronica area stat = %+v", sum.Areas[1])
	}
}
