package review

import (
	"reflect"
	"sort"

	"github.com/rksdevs/aslog/saves"
)

// Saves holds the two addon save files cut down to the chosen runs, as the server matches them. A nil
// file means no chosen run had that addon's data, or the file couldn't be read (its Err says which).
// The player's own files are never changed.
type Saves struct {
	Addon     []byte
	Stats     []byte
	AddonRuns int
	StatsRuns int
	AddonErr  error
	StatsErr  error
}

// ChosenSaves matches each chosen run to its WoW Logs AS and Azeroth Stats runs and writes copies
// holding only those, so an upload never carries the rest of a player's history, backups or settings.
// An empty source is skipped. indexes are Run.Index values; unknown ones are ignored.
func (s *Scan) ChosenSaves(indexes []int, addonSrc, statsSrc string) Saves {
	var out Saves
	var ours, stats []map[string]any
	var statsVersion string
	if addonSrc != "" {
		ours, out.AddonErr = saves.OurRuns(addonSrc)
	}
	if statsSrc != "" {
		stats, statsVersion, out.StatsErr = saves.StatsRuns(statsSrc)
	}
	sorted := append([]int(nil), indexes...)
	sort.Ints(sorted)
	var pickedOurs, pickedStats []map[string]any
	seen := map[uintptr]bool{}
	pick := func(list *[]map[string]any, run map[string]any) {
		// Two log runs can match the same saved run; it goes in once.
		if key := reflect.ValueOf(run).Pointer(); run != nil && !seen[key] {
			seen[key] = true
			*list = append(*list, run)
		}
	}
	for _, index := range sorted {
		run := s.run(index)
		if run == nil {
			continue
		}
		if len(ours) > 0 {
			pick(&pickedOurs, saves.MatchOurs(ours, s.RunLines(index)))
		}
		if len(stats) > 0 {
			pick(&pickedStats, saves.MatchStats(stats, run.Kills, run.ActiveMs))
		}
	}
	if len(pickedOurs) > 0 {
		if out.Addon, out.AddonErr = saves.OurCopy(pickedOurs); out.AddonErr == nil {
			out.AddonRuns = len(pickedOurs)
		}
	}
	if len(pickedStats) > 0 {
		if out.Stats, out.StatsErr = saves.StatsCopy(statsVersion, pickedStats); out.StatsErr == nil {
			out.StatsRuns = len(pickedStats)
		}
	}
	return out
}

func (s *Scan) run(index int) *Run {
	for i := range s.Runs {
		if s.Runs[i].Index == index {
			return &s.Runs[i]
		}
	}
	return nil
}
