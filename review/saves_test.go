package review

import (
	"strings"
	"testing"

	"github.com/rksdevs/aslog/saves"
)

// ourRun is a WoW Logs AS run whose saved log lines come from spell's run.
func ourRun(id, spell string) map[string]any {
	line := map[string]any{"text": "Your " + spell + " hits Kobold for 40."}
	return map[string]any{
		"schema": float64(saves.OurSchema), "gameRunId": id,
		"chat": map[string]any{"sync": []any{line, line, line, map[string]any{"text": "Kobold dies."}}},
	}
}

func statsRun(id string, kills int, activeMs int64) map[string]any {
	return map[string]any{
		"id":      id,
		"summary": map[string]any{"kills": float64(kills), "t": float64(activeMs) / 1000},
		"coop":    map[string]any{"peers": map[string]any{"1": map[string]any{"name": "Companion"}}},
	}
}

// An upload carries only the chosen runs' addon data: no other runs, no Azeroth Stats backups and no
// co-op names, and the copies read back as the server reads them.
func TestChosenSavesKeepOnlyChosenRuns(t *testing.T) {
	s, err := Open([]byte(run(0, 70, "Fireball") + run(5, 40, "Frostbolt") + run(10, 100, "Shadow Bolt")))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Runs) != 3 {
		t.Fatalf("runs = %+v", s.Runs)
	}
	ours, _ := saves.Write(saves.OurVariable, map[string]any{"runs": []any{
		ourRun("a", "Fireball"), ourRun("b", "Frostbolt"), ourRun("c", "Shadow Bolt"),
	}})
	var stats []any
	for i, r := range s.Runs {
		stats = append(stats, statsRun(string(rune('a'+i)), r.Kills, r.ActiveMs))
	}
	statsDB, _ := saves.Write(saves.StatsVariable, map[string]any{"version": "2.0.2", "runs": stats, "settings": map[string]any{"scale": 1.0}})
	backup, _ := saves.Write("AzerothStatsBackup", map[string]any{"runs": stats})

	got := s.ChosenSaves([]int{3, 1, 99}, string(ours), string(statsDB)+"\n"+string(backup))
	if got.AddonErr != nil || got.StatsErr != nil || got.AddonRuns != 2 || got.StatsRuns != 2 {
		t.Fatalf("got %+v", got)
	}
	addonRuns, err := saves.OurRuns(string(got.Addon))
	if err != nil || len(addonRuns) != 2 || addonRuns[0]["gameRunId"] != "a" || addonRuns[1]["gameRunId"] != "c" {
		t.Errorf("addon copy: %v %v", addonRuns, err)
	}
	statsRuns, version, err := saves.StatsRuns(string(got.Stats))
	if err != nil || version != "2.0.2" || len(statsRuns) != 2 || statsRuns[0]["id"] != "a" || statsRuns[1]["id"] != "c" {
		t.Errorf("stats copy: %v %q %v", statsRuns, version, err)
	}
	for _, leaked := range []string{"AzerothStatsBackup", "coop", "Companion", "settings", "Frostbolt"} {
		if strings.Contains(string(got.Stats)+string(got.Addon), leaked) {
			t.Errorf("copies hold %q", leaked)
		}
	}
	// The server matches the copies to the same runs as the full files.
	if saves.MatchStats(statsRuns, s.Runs[2].Kills, s.Runs[2].ActiveMs)["id"] != "c" {
		t.Error("the stats copy no longer matches its run")
	}
}

func TestChosenSavesWithoutMatchesOrFiles(t *testing.T) {
	s, err := Open([]byte(run(0, 70, "Fireball")))
	if err != nil {
		t.Fatal(err)
	}
	ours, _ := saves.Write(saves.OurVariable, map[string]any{"runs": []any{ourRun("x", "Frostbolt")}})
	got := s.ChosenSaves([]int{1}, string(ours), "")
	if got.Addon != nil || got.Stats != nil || got.AddonErr != nil || got.StatsErr != nil {
		t.Errorf("nothing matched, want no files: %+v", got)
	}
	got = s.ChosenSaves([]int{1}, "WowLogsASDB = os.exit()", "not a save")
	if got.AddonErr == nil || got.StatsErr == nil || got.Addon != nil || got.Stats != nil {
		t.Errorf("unreadable saves should report errors and give no files: %+v", got)
	}
}
