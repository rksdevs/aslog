package saves

import (
	"errors"
	"math"
	"strings"
)

const (
	// OurSchema is the WoW Logs AS 0.2.x save shape. Older test builds saved a different one.
	OurSchema = 2
	// OurVariable and StatsVariable are the files' top-level names.
	OurVariable   = "WowLogsASDB"
	StatsVariable = "AzerothStatsDB"
	// StatsMatchSlackMs is how far an Azeroth Stats run's clock may sit from the log's active time.
	StatsMatchSlackMs = 20_000
	// minSyncLines and the 60% share make an addon match: a few dozen exact lines in order only fit
	// the run they came from.
	minSyncLines = 3
)

// OurRuns returns the WoW Logs AS runs a report can be built from.
func OurRuns(src string) ([]map[string]any, error) {
	saved, err := Parse(src)
	if err != nil {
		return nil, err
	}
	db := Map(saved[OurVariable])
	if db == nil {
		return nil, errors.New("no WowLogsASDB in the file")
	}
	var runs []map[string]any
	for _, item := range List(db["runs"]) {
		if run := Map(item); run != nil && int(Float(run["schema"])) == OurSchema {
			runs = append(runs, run)
		}
	}
	return runs, nil
}

// StatsRuns returns the Azeroth Stats runs that have the game's end-of-run summary, and the addon's
// version. A run closed by Quit game has no summary.
func StatsRuns(src string) ([]map[string]any, string, error) {
	saved, err := Parse(src)
	if err != nil {
		return nil, "", err
	}
	db := Map(saved[StatsVariable])
	if db == nil {
		return nil, "", errors.New("no AzerothStatsDB in the file")
	}
	var runs []map[string]any
	for _, item := range List(db["runs"]) {
		if run := Map(item); run != nil && Map(run["summary"]) != nil {
			runs = append(runs, run)
		}
	}
	return runs, String(db["version"]), nil
}

// LineText drops a log line's timestamp.
func LineText(line string) string {
	if i := strings.Index(line, "  "); i >= 0 {
		return line[i+2:]
	}
	return line
}

// SyncMatches counts the addon's saved log lines found, in order, in a run's lines. The log has no
// run ID and its clock has no time zone, so the words themselves tie the two records together.
func SyncMatches(run map[string]any, lines []string) (matched, total int) {
	sync := List(Map(run["chat"])["sync"])
	total = len(sync)
	at := 0
	for _, item := range sync {
		text := String(Map(item)["text"])
		for i := at; i < len(lines); i++ {
			if LineText(lines[i]) == text {
				matched++
				at = i + 1
				break
			}
		}
	}
	return matched, total
}

// MatchOurs picks the WoW Logs AS run that belongs to a log run's lines, if any.
func MatchOurs(runs []map[string]any, lines []string) map[string]any {
	var best map[string]any
	bestMatched := 0
	for _, run := range runs {
		matched, total := SyncMatches(run, lines)
		if matched >= minSyncLines && matched*10 >= total*6 && matched > bestMatched {
			best, bestMatched = run, matched
		}
	}
	return best
}

// MatchStats ties an Azeroth Stats run to a log run by the game's own numbers. Its file has no game
// run ID yet, and kills are exact between the two, so kills must be equal and the run clock close
// to the log's active time.
func MatchStats(runs []map[string]any, kills int, activeMs int64) map[string]any {
	var best map[string]any
	bestGap := math.MaxFloat64
	for _, run := range runs {
		summary := Map(run["summary"])
		if int(Float(summary["kills"])) != kills {
			continue
		}
		gap := math.Abs(Float(summary["t"])*1000 - float64(activeMs))
		if gap <= StatsMatchSlackMs && gap < bestGap {
			best, bestGap = run, gap
		}
	}
	return best
}

// OurCopy is a WoW Logs AS save holding only the given runs. The original file is never changed.
func OurCopy(runs []map[string]any) ([]byte, error) {
	list := make([]any, len(runs))
	for i, run := range runs {
		list[i] = run
	}
	return Write(OurVariable, map[string]any{"runs": list})
}

// StatsCopy is an Azeroth Stats save holding only the given runs and the addon's version, which is
// all a WoW Logs report reads. The player's own file, with its history and settings, is never changed.
func StatsCopy(version string, runs []map[string]any) ([]byte, error) {
	list := make([]any, len(runs))
	for i, run := range runs {
		list[i] = run
	}
	db := map[string]any{"runs": list}
	if version != "" {
		db["version"] = version
	}
	return Write(StatsVariable, db)
}
