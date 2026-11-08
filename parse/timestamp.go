package parse

// Timestamp is the "M/D HH:MM:SS.mmm" prefix of a combat log line. The log
// carries no year, so only month, day and time of day are known.
type Timestamp struct {
	Month uint8
	Day   uint8
	Hour  uint8
	Min   uint8
	Sec   uint8
	Milli uint16
}

const (
	msPerDay = 86_400_000
	yearDays = 366 // Feb is always 29 days so a leap-year log never collides
	yearMS   = int64(yearDays) * msPerDay
	halfYear = yearMS / 2
)

// cumDays[m-1] is the number of days before month m in a leap year.
var cumDays = [12]int{0, 31, 60, 91, 121, 152, 182, 213, 244, 274, 305, 335}

// DayMS is the millisecond offset within the day (0..86399999).
func (t Timestamp) DayMS() int64 {
	return ((int64(t.Hour)*60+int64(t.Min))*60+int64(t.Sec))*1000 + int64(t.Milli)
}

// Raw is the millisecond offset from 1 Jan 00:00:00.000 of an imaginary leap
// year. It repeats every year, so it is only comparable within a year; use Mono
// for ordering across a day or year rollover.
func (t Timestamp) Raw() int64 {
	m := int(t.Month)
	if m < 1 || m > 12 {
		m = 1
	}
	return int64(cumDays[m-1]+int(t.Day)-1)*msPerDay + t.DayMS()
}

// Mono returns a millisecond value that never decreases along a stream of
// lines. prev is the value Mono returned for the previous timestamped line;
// havePrev is false for the first one (the result is then simply Raw).
//
// The step from prev is taken as the forward distance on the one-year circle,
// so a midnight rollover (23:59 -> 00:00 on the next date) and a year rollover
// (12/31 -> 1/1) both give a small positive step. A step that would be more than
// half a year forward is read as the clock going backwards (a few ms of
// out-of-order lines, or a clock correction) and is clamped to zero.
func (t Timestamp) Mono(prev int64, havePrev bool) int64 {
	raw := t.Raw()
	if !havePrev {
		return raw
	}
	d := (raw - prev) % yearMS
	if d < 0 {
		d += yearMS
	}
	if d > halfYear {
		return prev
	}
	return prev + d
}
