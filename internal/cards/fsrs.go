package cards

import (
	"math"
	"time"
)

// Ratings follow FSRS / Anki conventions.
const (
	RatingAgain = 1
	RatingHard  = 2
	RatingGood  = 3
	RatingEasy  = 4
)

// Card scheduling states.
const (
	StateNew        = "new"
	StateLearning   = "learning"
	StateReview     = "review"
	StateRelearning = "relearning"
)

// Default FSRS-4.5 parameters (17 weights) and 90% target retention.
var fsrsW = [17]float64{
	0.4072, 1.1829, 3.1262, 15.4722,
	7.2102, 0.5316, 1.0651, 0.0234,
	1.616, 0.1544, 1.0824, 1.9813,
	0.0953, 0.2975, 2.2042, 0.2407,
	2.9466,
}

const (
	targetRetention = 0.9
	maxIntervalDays = 3650
)

// ScheduleResult is the new FSRS state and interval after a review rating.
type ScheduleResult struct {
	State      string
	Stability  float64
	Difficulty float64
	Interval   time.Duration
	Due        time.Time
	Reps       int
	Lapses     int
}

func clampD(d float64) float64 {
	return math.Max(1.0, math.Min(10.0, d))
}

func initStability(rating int) float64 {
	return math.Max(0.1, fsrsW[rating-1])
}

func initDifficulty(rating int) float64 {
	return clampD(fsrsW[4] - math.Exp(fsrsW[5]*float64(rating-1)) + 1)
}

func retrievability(elapsedDays, stability float64) float64 {
	if stability <= 0 {
		return 0
	}
	return math.Pow(1.0+elapsedDays/(9.0*stability), -1.0)
}

func nextDifficulty(d float64, rating int) float64 {
	next := d - fsrsW[6]*float64(rating-3)
	// mean reversion toward D0(3)
	return clampD(fsrsW[7]*initDifficulty(RatingGood) + (1.0-fsrsW[7])*next)
}

func nextRecallStability(d, s, r float64, rating int) float64 {
	hardPenalty := 1.0
	if rating == RatingHard {
		hardPenalty = fsrsW[15]
	}
	easyBonus := 1.0
	if rating == RatingEasy {
		easyBonus = fsrsW[16]
	}
	return s * (1.0 + math.Exp(fsrsW[8])*
		(11.0-d)*
		math.Pow(s, -fsrsW[9])*
		(math.Exp((1.0-r)*fsrsW[10])-1.0)*
		hardPenalty*easyBonus)
}

func nextForgetStability(d, s, r float64) float64 {
	return math.Max(0.1, fsrsW[11]*
		math.Pow(d, -fsrsW[12])*
		(math.Pow(s+1.0, fsrsW[13])-1.0)*
		math.Exp((1.0-r)*fsrsW[14]))
}

func nextIntervalDays(stability float64) int {
	ivl := stability * 9.0 * (1.0/targetRetention - 1.0)
	days := int(math.Round(ivl))
	if days < 1 {
		days = 1
	}
	if days > maxIntervalDays {
		days = maxIntervalDays
	}
	return days
}

// Schedule computes the next FSRS state and due time for a card given rating (1..4).
func Schedule(prev ReviewState, rating int, now time.Time) ScheduleResult {
	if rating < RatingAgain || rating > RatingEasy {
		rating = RatingGood
	}
	res := ScheduleResult{
		State:      prev.State,
		Stability:  prev.Stability,
		Difficulty: prev.Difficulty,
		Reps:       prev.Reps + 1,
		Lapses:     prev.Lapses,
	}

	if prev.State == "" || prev.State == StateNew || prev.Stability <= 0 {
		res.Stability = initStability(rating)
		res.Difficulty = initDifficulty(rating)
		switch rating {
		case RatingAgain:
			res.State = StateLearning
			res.Interval = 10 * time.Minute
		case RatingHard:
			res.State = StateLearning
			res.Interval = 12 * time.Hour
		case RatingGood:
			res.State = StateReview
			res.Interval = time.Duration(nextIntervalDays(res.Stability)) * 24 * time.Hour
		case RatingEasy:
			res.State = StateReview
			days := max(2, nextIntervalDays(res.Stability))
			res.Interval = time.Duration(days) * 24 * time.Hour
		}
		res.Due = now.Add(res.Interval)
		return res
	}

	elapsed := 0.0
	if !prev.LastReview.IsZero() && now.After(prev.LastReview) {
		elapsed = now.Sub(prev.LastReview).Hours() / 24.0
	}
	r := retrievability(elapsed, prev.Stability)
	res.Difficulty = nextDifficulty(prev.Difficulty, rating)

	if rating == RatingAgain {
		res.Lapses++
		res.State = StateRelearning
		res.Stability = nextForgetStability(res.Difficulty, prev.Stability, r)
		res.Interval = 10 * time.Minute
	} else {
		res.State = StateReview
		res.Stability = nextRecallStability(res.Difficulty, prev.Stability, r, rating)
		days := nextIntervalDays(res.Stability)
		if rating == RatingHard && days > 1 {
			days = max(1, int(math.Round(float64(days)*0.8)))
		}
		if rating == RatingEasy {
			days = max(days+1, int(math.Round(float64(days)*1.25)))
		}
		res.Interval = time.Duration(days) * 24 * time.Hour
	}
	res.Due = now.Add(res.Interval)
	return res
}

// PreviewIntervals returns the scheduled durations (in minutes) for ratings 1..4 so the UI can
// display exact next-review labels on each button.
func PreviewIntervals(prev ReviewState, now time.Time) map[int]int {
	out := make(map[int]int, 4)
	for r := RatingAgain; r <= RatingEasy; r++ {
		sr := Schedule(prev, r, now)
		mins := int(math.Round(sr.Interval.Minutes()))
		if mins < 1 {
			mins = 1
		}
		out[r] = mins
	}
	return out
}
