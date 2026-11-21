// Package stats tracks per-vault study events, XP, daily streaks with streak freezes,
// activity calendar, and progress broken down by knowledge area. Everything lives in
// vault/.studyhomie/stats.json (one learner per vault).
package stats

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/krilinxito/studyhomie/internal/vault"
)

// Event kinds recorded by the backend.
const (
	KindGuide     = "guide"     // generated a study guide
	KindRead      = "read"      // opened/read a guide (once per topic per day)
	KindQuiz      = "quiz"      // completed a quiz
	KindFeynman   = "feynman"   // completed a Feynman explanation
	KindConcept   = "concept"   // explained a concept note
	KindDoubt     = "doubt"     // asked and saved a doubt on a guide
	KindFlashcard = "flashcard" // reviewed flashcards (Phase 8)
	KindActivity  = "activity"  // completed a practical activity (Phase 9)
)

// Defaults and streak freeze rules.
const (
	DefaultDailyGoal = 50
	MinDailyGoal     = 10
	MaxDailyGoal     = 500

	FreezeEvery = 7 // streak days needed to earn one freeze
	MaxFreezes  = 2 // maximum freezes held at once
	AtRiskHour  = 18

	CalendarWeeks = 18
	CalendarDays  = CalendarWeeks * 7

	maxDoubtXPPerDay = 3
)

// Event is one recorded study action.
type Event struct {
	At      time.Time `json:"at"`
	Date    string    `json:"date"` // local YYYY-MM-DD
	Kind    string    `json:"kind"`
	Topic   string    `json:"topic,omitempty"`
	Title   string    `json:"title,omitempty"`
	Area    string    `json:"area,omitempty"`
	XP      int       `json:"xp"`
	Score   int       `json:"score,omitempty"`
	Seconds int       `json:"seconds,omitempty"`
	// Partial marks a practice closed before the end: graded on what was answered, it does not
	// change the topic's mastery.
	Partial  bool   `json:"partial,omitempty"`
	Practice string `json:"practice,omitempty"` // id of the practice session it came from
}

// Data is what gets persisted to vault/.studyhomie/stats.json.
type Data struct {
	Events       []Event `json:"events"`
	TutorialSeen bool    `json:"tutorialSeen,omitempty"`
}

// AreaStat summarizes progress in one canonical knowledge area.
type AreaStat struct {
	Area           string `json:"area"`
	XP             int    `json:"xp"`
	Guides         int    `json:"guides"`
	MasteredGuides int    `json:"masteredGuides"`
	AvgMastery     int    `json:"avgMastery"`
	Concepts       int    `json:"concepts"`
	Minutes        int    `json:"minutes"`
}

// CalendarDay is one cell in the activity heatmap.
type CalendarDay struct {
	Date   string `json:"date"`
	XP     int    `json:"xp"`
	Level  int    `json:"level"` // 0..4
	Frozen bool   `json:"frozen,omitempty"`
	Events int    `json:"events"`
}

// Summary is the full gamification snapshot served by GET /api/stats.
type Summary struct {
	TotalXP      int           `json:"totalXP"`
	TodayXP      int           `json:"todayXP"`
	DailyGoal    int           `json:"dailyGoal"`
	GoalMet      bool          `json:"goalMet"`
	Streak       int           `json:"streak"`
	Longest      int           `json:"longest"`
	Freezes      int           `json:"freezes"`
	MaxFreezes   int           `json:"maxFreezes"`
	NextFreezeIn int           `json:"nextFreezeIn"`
	AtRisk       bool          `json:"atRisk"`
	LostStreak   bool          `json:"lostStreak"`
	TotalMinutes int           `json:"totalMinutes"`
	Areas        []AreaStat    `json:"areas"`
	Calendar     []CalendarDay `json:"calendar"`
	Recent       []Event       `json:"recent"`
	TutorialSeen bool          `json:"tutorialSeen"`
}

// XPFor computes the XP awarded for an event kind and optional score (0..100).
func XPFor(kind string, score int) int {
	score = max(0, min(score, 100))
	switch kind {
	case KindGuide:
		return 25
	case KindRead:
		return 10
	case KindConcept:
		return 15
	case KindQuiz:
		return 20 + score/5 // 20..40
	case KindFeynman:
		return 25 + score/4 // 25..50
	case KindDoubt:
		return 5
	case KindFlashcard:
		return 5
	case KindActivity:
		return 20
	default:
		return 10
	}
}

// DefaultSeconds estimates study time when an event has no measured duration.
func DefaultSeconds(kind string) int {
	switch kind {
	case KindGuide:
		return 5 * 60
	case KindRead:
		return 4 * 60
	case KindQuiz:
		return 4 * 60
	case KindFeynman:
		return 5 * 60
	case KindConcept:
		return 2 * 60
	case KindDoubt:
		return 2 * 60
	case KindFlashcard:
		return 60
	case KindActivity:
		return 8 * 60
	default:
		return 2 * 60
	}
}

// NormalizeGoal clamps or defaults the daily XP goal.
func NormalizeGoal(goal int) int {
	if goal <= 0 {
		return DefaultDailyGoal
	}
	if goal < MinDailyGoal {
		return MinDailyGoal
	}
	if goal > MaxDailyGoal {
		return MaxDailyGoal
	}
	return goal
}

// FilePath returns the stats JSON location inside a vault.
func FilePath(vaultRoot string) string {
	return filepath.Join(vaultRoot, ".studyhomie", "stats.json")
}

// fileMu serializes read-modify-write updates per process.
var fileMu sync.Mutex

// Load reads vault/.studyhomie/stats.json. A missing file returns empty Data without error.
func Load(vaultRoot string) (Data, error) {
	fileMu.Lock()
	defer fileMu.Unlock()
	return loadLocked(vaultRoot)
}

func loadLocked(vaultRoot string) (Data, error) {
	raw, err := os.ReadFile(FilePath(vaultRoot))
	if errors.Is(err, os.ErrNotExist) {
		return Data{}, nil
	}
	if err != nil {
		return Data{}, err
	}
	var d Data
	if err := json.Unmarshal(raw, &d); err != nil {
		return Data{}, err
	}
	return d, nil
}

func saveLocked(vaultRoot string, d Data) error {
	p := FilePath(vaultRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Record appends an event to the vault's stats log, filling defaults for Date, Area, XP and Seconds.
// For KindRead, it records at most once per topic per calendar day.
// For KindDoubt, it caps daily XP events at maxDoubtXPPerDay.
// It returns true if the event was recorded.
func Record(vaultRoot string, ev Event) (bool, error) {
	fileMu.Lock()
	defer fileMu.Unlock()

	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	if ev.Date == "" {
		ev.Date = ev.At.Format("2006-01-02")
	}
	if ev.Area != "" {
		ev.Area = vault.NormalizeArea(ev.Area)
	} else {
		ev.Area = "general"
	}
	if ev.XP <= 0 {
		ev.XP = XPFor(ev.Kind, ev.Score)
	}
	if ev.Seconds <= 0 {
		ev.Seconds = DefaultSeconds(ev.Kind)
	}

	d, err := loadLocked(vaultRoot)
	if err != nil {
		return false, err
	}

	if ev.Kind == KindRead {
		for _, prev := range d.Events {
			if prev.Kind == KindRead && prev.Date == ev.Date && prev.Topic == ev.Topic {
				return false, nil
			}
		}
	}
	if ev.Kind == KindDoubt {
		count := 0
		for _, prev := range d.Events {
			if prev.Kind == KindDoubt && prev.Date == ev.Date {
				count++
			}
		}
		if count >= maxDoubtXPPerDay {
			return false, nil
		}
	}

	d.Events = append(d.Events, ev)
	if err := saveLocked(vaultRoot, d); err != nil {
		return false, err
	}
	return true, nil
}

// MarkTutorialSeen records that the learner has seen (or skipped) the onboarding tutorial for this vault.
func MarkTutorialSeen(vaultRoot string) error {
	fileMu.Lock()
	defer fileMu.Unlock()
	d, err := loadLocked(vaultRoot)
	if err != nil {
		return err
	}
	if d.TutorialSeen {
		return nil
	}
	d.TutorialSeen = true
	return saveLocked(vaultRoot, d)
}

// Compute builds the full gamification Summary from recorded events and current vault notes/concepts.
func Compute(d Data, notes []vault.Note, concepts []vault.Concept, dailyGoal, passScore int, now time.Time) Summary {
	goal := NormalizeGoal(dailyGoal)
	if passScore <= 0 || passScore > 100 {
		passScore = 70
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	todayStr := today.Format("2006-01-02")
	yesterdayStr := today.AddDate(0, 0, -1).Format("2006-01-02")

	// Bucket events by date and accumulate totals & area metrics.
	dayXP := map[string]int{}
	dayEvents := map[string]int{}
	type areaAcc struct {
		xp           int
		seconds      int
		guides       int
		mastered     int
		masterySum   int
		masteryCount int
		concepts     int
	}
	areaMap := map[string]*areaAcc{}
	getArea := func(a string) *areaAcc {
		a = vault.NormalizeArea(a)
		if a == "" {
			a = "general"
		}
		if areaMap[a] == nil {
			areaMap[a] = &areaAcc{}
		}
		return areaMap[a]
	}

	totalXP := 0
	totalSeconds := 0
	var firstDate time.Time
	for _, ev := range d.Events {
		date := ev.Date
		if date == "" && !ev.At.IsZero() {
			date = ev.At.Format("2006-01-02")
		}
		if date == "" {
			continue
		}
		dayXP[date] += ev.XP
		dayEvents[date]++
		totalXP += ev.XP
		sec := ev.Seconds
		if sec <= 0 {
			sec = DefaultSeconds(ev.Kind)
		}
		totalSeconds += sec

		acc := getArea(ev.Area)
		acc.xp += ev.XP
		acc.seconds += sec

		if parsed, err := time.ParseInLocation("2006-01-02", date, now.Location()); err == nil {
			if firstDate.IsZero() || parsed.Before(firstDate) {
				firstDate = parsed
			}
		}
	}

	// Fold vault guides and concepts into area stats so existing vault content is reflected too.
	guideArea := map[string]string{}
	for _, n := range notes {
		a := n.Meta.Area
		if a == "" && n.Meta.Field != "" {
			a = vault.NormalizeArea(n.Meta.Field)
		}
		if a == "" {
			a = "general"
		}
		guideArea[n.Slug] = a
		if n.Meta.Status == "" || n.Meta.Status == "new" {
			continue
		}
		acc := getArea(a)
		acc.guides++
		if n.Meta.Status == "tested" || n.Meta.Status == "explained" || n.Meta.Mastery > 0 {
			acc.masterySum += n.Meta.Mastery
			acc.masteryCount++
			if n.Meta.Mastery >= passScore {
				acc.mastered++
			}
		}
	}
	for _, c := range concepts {
		a := ""
		for _, g := range c.Meta.Guides {
			if ga := guideArea[g]; ga != "" && ga != "general" {
				a = ga
				break
			}
		}
		if a == "" && c.Meta.Category != "" {
			a = vault.NormalizeArea(c.Meta.Category)
		}
		if a == "" {
			a = "general"
		}
		getArea(a).concepts++
	}

	// Chronological streak + freeze simulation from first recorded day through today.
	streak := 0
	longest := 0
	freezes := 0
	daysSinceFreeze := 0
	frozenDays := map[string]bool{}
	lostStreak := false

	if !firstDate.IsZero() && !firstDate.After(today) {
		for cur := firstDate; !cur.After(today); cur = cur.AddDate(0, 0, 1) {
			ds := cur.Format("2006-01-02")
			xp := dayXP[ds]
			if xp >= goal {
				streak++
				daysSinceFreeze++
				if daysSinceFreeze >= FreezeEvery {
					if freezes < MaxFreezes {
						freezes++
					}
					daysSinceFreeze = 0
				}
				if streak > longest {
					longest = streak
				}
			} else if ds == todayStr {
				// Today is still open: keep active streak intact while waiting for today's XP.
			} else {
				if streak > 0 && freezes > 0 {
					freezes--
					frozenDays[ds] = true
				} else {
					if ds == yesterdayStr && streak > 0 {
						lostStreak = true
					}
					streak = 0
					daysSinceFreeze = 0
				}
			}
		}
	}

	todayXP := dayXP[todayStr]
	goalMet := todayXP >= goal
	atRisk := streak > 0 && !goalMet && now.Hour() >= AtRiskHour
	nextFreezeIn := 0
	if freezes < MaxFreezes {
		nextFreezeIn = FreezeEvery - daysSinceFreeze
		if nextFreezeIn <= 0 {
			nextFreezeIn = FreezeEvery
		}
	}

	// Build 18-week calendar ending today.
	start := today.AddDate(0, 0, -(CalendarDays - 1))
	cal := make([]CalendarDay, 0, CalendarDays)
	for cur := start; !cur.After(today); cur = cur.AddDate(0, 0, 1) {
		ds := cur.Format("2006-01-02")
		xp := dayXP[ds]
		cal = append(cal, CalendarDay{
			Date:   ds,
			XP:     xp,
			Level:  heatLevel(xp, goal),
			Frozen: frozenDays[ds],
			Events: dayEvents[ds],
		})
	}

	// Sort areas by XP desc, then guides desc, then name.
	areas := make([]AreaStat, 0, len(areaMap))
	for a, acc := range areaMap {
		if acc.xp == 0 && acc.guides == 0 && acc.concepts == 0 {
			continue
		}
		avg := 0
		if acc.masteryCount > 0 {
			avg = acc.masterySum / acc.masteryCount
		}
		mins := (acc.seconds + 30) / 60
		if acc.seconds > 0 && mins == 0 {
			mins = 1
		}
		areas = append(areas, AreaStat{
			Area:           a,
			XP:             acc.xp,
			Guides:         acc.guides,
			MasteredGuides: acc.mastered,
			AvgMastery:     avg,
			Concepts:       acc.concepts,
			Minutes:        mins,
		})
	}
	sort.Slice(areas, func(i, j int) bool {
		if areas[i].XP != areas[j].XP {
			return areas[i].XP > areas[j].XP
		}
		if areas[i].Guides != areas[j].Guides {
			return areas[i].Guides > areas[j].Guides
		}
		return strings.Compare(areas[i].Area, areas[j].Area) < 0
	})

	// Latest 12 events, newest first.
	recentN := min(12, len(d.Events))
	recent := make([]Event, 0, recentN)
	for i := len(d.Events) - 1; i >= len(d.Events)-recentN; i-- {
		recent = append(recent, d.Events[i])
	}

	totalMins := (totalSeconds + 30) / 60
	if totalSeconds > 0 && totalMins == 0 {
		totalMins = 1
	}

	return Summary{
		TotalXP:      totalXP,
		TodayXP:      todayXP,
		DailyGoal:    goal,
		GoalMet:      goalMet,
		Streak:       streak,
		Longest:      longest,
		Freezes:      freezes,
		MaxFreezes:   MaxFreezes,
		NextFreezeIn: nextFreezeIn,
		AtRisk:       atRisk,
		LostStreak:   lostStreak,
		TotalMinutes: totalMins,
		Areas:        areas,
		Calendar:     cal,
		Recent:       recent,
		TutorialSeen: d.TutorialSeen,
	}
}

func heatLevel(xp, goal int) int {
	switch {
	case xp <= 0:
		return 0
	case xp < goal/2:
		return 1
	case xp < goal:
		return 2
	case xp < goal*2:
		return 3
	default:
		return 4
	}
}
