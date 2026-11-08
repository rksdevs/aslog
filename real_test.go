package aslog_test

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rksdevs/aslog/parse"
	"github.com/rksdevs/aslog/segment"
)

// TestRealLog is the acceptance test against a real 1.12 log. It is skipped
// unless ASLOG_REAL_LOG points at a log file, and asserts the figures
// established for D:\AzerothSurvivorsWindowsNew\benilla-config\Logs\WoWCombatLog.txt
// (177,829 lines, 11/5 22:05 to 11/6 12:35, 10 runs).
func TestRealLog(t *testing.T) {
	path := os.Getenv("ASLOG_REAL_LOG")
	if path == "" {
		t.Skip("ASLOG_REAL_LOG not set")
	}
	lines := readLines(t, path)
	t.Logf("%d lines", len(lines))

	// Parse throughput (parse only, lines already in memory).
	var bytesTotal int
	for _, l := range lines {
		bytesTotal += len(l) + 1
	}
	best := time.Duration(1 << 62)
	unknown := 0
	for pass := 0; pass < 5; pass++ {
		unknown = 0
		start := time.Now()
		for _, l := range lines {
			if parse.Parse(l).Kind == parse.Unknown {
				unknown++
			}
		}
		if d := time.Since(start); d < best {
			best = d
		}
	}
	t.Logf("parse: %v best of 5, %.0f lines/s, %.1f MB/s, unknown lines: %d",
		best, float64(len(lines))/best.Seconds(), float64(bytesTotal)/best.Seconds()/1e6, unknown)

	start := time.Now()
	runs := segmentAll(segment.Config{}, lines)
	t.Logf("parse+segment: %v", time.Since(start))

	t.Logf("%-3s %-17s %-17s %7s %7s %6s %9s %7s %9s %-8s", "#", "start", "end", "lines", "hits", "kills", "gross", "taken", "activeMs", "end")
	unkRuns := 0
	for _, r := range runs {
		unkRuns += r.Unknown
		if got := r.LastLine - r.FirstLine + 1; got != r.Lines {
			t.Errorf("run %d: lines %d..%d span %d, want %d", r.Index, r.FirstLine, r.LastLine, got, r.Lines)
		}
		t.Logf("%-3d %-17s %-17s %7d %7d %6d %9d %7d %9d %-8s inferred=%v unknown=%d gaps>=30s=%d",
			r.Index, stamp(r.First), stamp(r.Last), r.Lines, r.Hits, r.KillCount(), r.DamageOut, r.DamageTaken, r.ActiveMs, r.EndReason, r.Inferred, r.Unknown, len(r.Gaps))
		for _, g := range r.Gaps {
			t.Logf("      gap inside run %d: %s -> %s = %.3fs (after run line %d)", r.Index, stamp(g.From), stamp(g.To), float64(g.MS)/1000, g.AfterLine)
		}
	}
	// Arbitrary batch splits must give the very same runs.
	rng := rand.New(rand.NewSource(7))
	bs := segment.New(segment.Config{})
	var res []segment.Result
	for i := 0; i < len(lines); {
		n := 1 + rng.Intn(5000)
		if i+n > len(lines) {
			n = len(lines) - i
		}
		res = append(res, bs.PushLines(lines[i:i+n])...)
		i += n
	}
	res = append(res, bs.Flush()...)
	var batched []*segment.Run
	for _, r := range res {
		if r.Kind == segment.RunEnded {
			batched = append(batched, r.Run)
		}
	}
	if !reflect.DeepEqual(batched, runs) {
		t.Error("batch splits changed the runs")
	}
	if unknown != 0 || unkRuns != 0 {
		t.Errorf("unknown lines: parse %d, in runs %d, want 0", unknown, unkRuns)
	}
	total := 0
	for _, r := range runs {
		total += r.Lines
	}
	if total != len(lines) {
		t.Errorf("runs own %d of %d lines", total, len(lines))
	}

	if len(runs) != 10 {
		t.Fatalf("runs = %d, want 10", len(runs))
	}
	type want struct {
		start              string
		lines, hits, kills int
		gross, taken       int64
		end                string
		inferred           bool
	}
	// lines/hits/gross/taken are 0 where the established truth gives none.
	exp := []want{
		{"11/5 22:05:51.763", 29671, 0, 1624, 1047122, 8283, segment.EndDeath, false},
		{"11/5 22:20:15.061", 22447, 0, 2365, 1801618, 9709, segment.EndDeath, false},
		{"11/6 00:04:35.959", 0, 85340, 3429, 7638573, 33918, segment.EndMaxGap, true}, // 67 min silence before run 4,
		{"11/6 01:36:33.045", 0, 0, 1087, 0, 0, segment.EndDeath, false},
		{"11/6 01:47:16.043", 0, 0, 18, 0, 0, segment.EndDeath, false},
		{"11/6 01:54:58.750", 0, 0, 159, 0, 0, segment.EndDeath, false},
		{"11/6 02:07:51.080", 0, 0, 9, 0, 0, segment.EndDeath, false},
		// End Run (76 kills), then the Quit game run (10 kills) after a 57 s
		// pause: the build is lost, so it is a reset at the first line after.
		{"11/6 02:11:40.231", 0, 0, 76, 0, 0, segment.EndReset, true},
		// The Quit run (10 kills) and the next run share their opening build, so
		// the look-ahead alone would keep them together; the 10 h 17 min
		// silence between them is over MaxSilence (30 min), a hard boundary.
		{"11/6 02:14:12.976", 0, 0, 10, 0, 0, segment.EndMaxGap, true},
		{"11/6 12:31:27.009", 0, 0, 465, 0, 0, segment.EndDeath, false},
	}
	for i, w := range exp {
		r := runs[i]
		if got := stamp(r.First); got != w.start {
			t.Errorf("run %d: start %s, want %s", i+1, got, w.start)
		}
		if r.KillCount() != w.kills || r.Inferred != w.inferred || (w.end != "" && r.EndReason != w.end) {
			t.Errorf("run %d: kills=%d end=%s inferred=%v, want %d %s %v", i+1, r.KillCount(), r.EndReason, r.Inferred, w.kills, w.end, w.inferred)
		}
		if w.lines != 0 && r.Lines != w.lines {
			t.Errorf("run %d: lines=%d want %d", i+1, r.Lines, w.lines)
		}
		if w.hits != 0 && r.Hits != w.hits {
			t.Errorf("run %d: hits=%d want %d", i+1, r.Hits, w.hits)
		}
		if w.gross != 0 && r.DamageOut != w.gross {
			t.Errorf("run %d: gross=%d want %d", i+1, r.DamageOut, w.gross)
		}
		if w.taken != 0 && r.DamageTaken != w.taken {
			t.Errorf("run %d: taken=%d want %d", i+1, r.DamageTaken, w.taken)
		}
	}

	r1, r2, r3, r9 := runs[0], runs[1], runs[2], runs[9]
	if got := stamp(r1.First); got != "11/5 22:05:51.763" {
		t.Errorf("run 1 start %s", got)
	}
	if got := stamp(r1.Last); got != "11/5 22:19:44.022" {
		t.Errorf("run 1 end %s", got)
	}
	if got := stamp(r2.Last); got != "11/5 22:33:55.229" {
		t.Errorf("run 2 end %s", got)
	}
	if r3.First.Month != 11 || r3.First.Day != 6 || r3.First.Hour != 0 {
		t.Errorf("run 3 start %s", stamp(r3.First))
	}
	if got := stamp(r9.Last); got != "11/6 12:35:45.672" {
		t.Errorf("run 9 end %s", got)
	}
	wantSpells := map[string]int64{
		"Curse of Agony": 2660639, "Shadow Bolt": 1815774, "Corruption": 1725119,
		"Rain of Fire": 675056, "Fireball": 430331, "Hellfire": 331654,
	}
	for name, dmg := range wantSpells {
		if r3.DamageBySpell[name] != dmg {
			t.Errorf("run 3 %s = %d, want %d", name, r3.DamageBySpell[name], dmg)
		}
	}
	if top := topSpells(r3, 6); strings.Join(top, ",") != "Curse of Agony,Shadow Bolt,Corruption,Rain of Fire,Fireball,Hellfire" {
		t.Errorf("run 3 top spells = %v", top)
	}
}

func stamp(ts parse.Timestamp) string {
	return fmt.Sprintf("%d/%d %02d:%02d:%02d.%03d", ts.Month, ts.Day, ts.Hour, ts.Min, ts.Sec, ts.Milli)
}

func topSpells(r *segment.Run, n int) []string {
	names := make([]string, 0, len(r.DamageBySpell))
	for k := range r.DamageBySpell {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool { return r.DamageBySpell[names[i]] > r.DamageBySpell[names[j]] })
	if len(names) > n {
		names = names[:n]
	}
	return names
}

// TestRealLogInno: the second real log is one run with a death-ended rule
// unchanged and no splits. Skipped unless ASLOG_REAL_LOG_INNO is set.
func TestRealLogInno(t *testing.T) {
	path := os.Getenv("ASLOG_REAL_LOG_INNO")
	if path == "" {
		t.Skip("ASLOG_REAL_LOG_INNO not set")
	}
	lines := readLines(t, path)
	runs := segmentAll(segment.Config{}, lines)
	for _, r := range runs {
		t.Logf("run %d %s -> %s lines=%d hits=%d kills=%d activeMs=%d end=%s inferred=%v", r.Index, stamp(r.First), stamp(r.Last), r.Lines, r.Hits, r.KillCount(), r.ActiveMs, r.EndReason, r.Inferred)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want exactly 1", len(runs))
	}
	if runs[0].Lines != len(lines) {
		t.Errorf("run owns %d of %d lines", runs[0].Lines, len(lines))
	}
}
