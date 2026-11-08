package segment

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/rksdevs/aslog/parse"
)

// stamp renders ms after 11/5 22:00:00.000 as a log timestamp.
func stamp(ms int) string {
	day := 5
	rem := ms + 22*3_600_000
	for rem >= 86_400_000 {
		rem -= 86_400_000
		day++
	}
	return fmt.Sprintf("11/%d %02d:%02d:%02d.%03d", day, rem/3_600_000, rem/60_000%60, rem/1000%60, rem%1000)
}

func spellLine(ms int, spell string, dmg int) string {
	return fmt.Sprintf("%s  Your %s hits Kobold Vermin for %d Fire damage.", stamp(ms), spell, dmg)
}

// volley emits n hits, one every 400 ms from startMS, cycling the spells at a
// fixed size. It returns the lines and the time of the last one.
func volley(startMS, n int, spells []string, dmg int) ([]string, int) {
	var out []string
	ms := startMS
	for i := 0; i < n; i++ {
		out = append(out, spellLine(ms, spells[i%len(spells)], dmg))
		ms += 400
	}
	return out, ms - 400
}

var (
	buildA = []string{"Fireball", "Shadow Bolt", "Corruption", "Hellfire"}
	buildB = []string{"Frostbolt", "Arcane Missiles", "Fire Blast"}
)

func cat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestPauseWithOneSpellReturningLateStaysOneRun(t *testing.T) {
	a, end := volley(0, 40, buildA, 100)
	// After a 20 s pause three of the four spells are back at once; Hellfire
	// only returns much later. 1/4 lost is under the 50% share.
	b, end2 := volley(end+20_000, 30, buildA[:3], 110)
	c, _ := volley(end2+60_000, 8, buildA, 120)
	runs, _ := run(Config{}, cat(a, b, c))
	if len(runs) != 1 || runs[0].Lines != 78 {
		t.Fatalf("split on a pause: %d runs", len(runs))
	}
}

func TestTwoClosePausesStayOneRun(t *testing.T) {
	a, end := volley(0, 40, buildA, 100)
	b, end2 := volley(end+6_000, 3, buildA, 100) // too few hits to judge on their own
	c, _ := volley(end2+6_000, 40, buildA, 100)
	runs, _ := run(Config{}, cat(a, b, c))
	if len(runs) != 1 || runs[0].Lines != 83 {
		t.Fatalf("two pauses split the run: %d runs", len(runs))
	}
}

func TestBuildLossSplits(t *testing.T) {
	a, end := volley(0, 40, buildA, 100)
	b, _ := volley(end+5_000, 40, buildB, 100)
	runs, res := run(Config{}, cat(a, b))
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(runs))
	}
	r1, r2 := runs[0], runs[1]
	if r1.EndReason != EndReset || !r1.Inferred || r1.Lines != 40 || r1.Last.Sec != 15 || r1.Last.Milli != 600 {
		t.Fatalf("run 1: %+v", r1)
	}
	// The boundary is the first line after the silence.
	if r2.Lines != 40 || r2.First.Sec != 20 || r2.First.Milli != 600 || r2.EndReason != EndEOF {
		t.Fatalf("run 2: %+v", r2)
	}
	// Results arrive in order: start, reset end (after its look-ahead
	// resolved), start of the next run, eof end.
	if len(res) != 4 || res[0].Kind != RunStarted || res[1].Kind != RunEnded || res[2].Kind != RunStarted || res[3].Kind != RunEnded {
		t.Fatalf("results: %+v", res)
	}
	if _, ok := r2.DamageBySpell["Fireball"]; ok {
		t.Fatal("run 2 inherited run 1's damage")
	}
}

func TestHitCollapseSplits(t *testing.T) {
	a, end := volley(0, 40, buildA, 100)
	b, _ := volley(end+5_000, 40, buildA, 20) // same spells, hits at 20%
	runs, _ := run(Config{}, cat(a, b))
	if len(runs) != 2 || runs[0].EndReason != EndReset {
		t.Fatalf("collapse did not split: %d runs", len(runs))
	}
	// Hits at 60% are not a collapse.
	b, _ = volley(end+5_000, 40, buildA, 60)
	runs, _ = run(Config{}, cat(a, b))
	if len(runs) != 1 {
		t.Fatalf("60%% hits split the run: %d runs", len(runs))
	}
	// Crits do not feed the medians: huge crits before, normal hits after.
	var crits []string
	for i := 0; i < 40; i++ {
		crits = append(crits, fmt.Sprintf("%s  Your %s crits Kobold Vermin for 900 Fire damage.", stamp(i*400), buildA[i%4]))
		if i%4 == 0 {
			crits = append(crits, spellLine(i*400+1, buildA[i%4], 100))
		}
	}
	b, _ = volley(40*400+5_000, 40, buildA, 100)
	runs, _ = run(Config{}, cat(crits, b))
	if len(runs) != 1 {
		t.Fatalf("crit sizes leaked into the medians: %d runs", len(runs))
	}
}

func TestLongAFKWithBuildReturningStaysOneRun(t *testing.T) {
	a, end := volley(0, 40, buildA, 100)
	b, _ := volley(end+29*60_000, 40, buildA, 100)
	runs, _ := run(Config{}, cat(a, b))
	if len(runs) != 1 || runs[0].Lines != 80 {
		t.Fatalf("AFK split the run: %d runs", len(runs))
	}
	if len(runs[0].Gaps) != 1 || runs[0].Gaps[0].MS < 29*60_000 {
		t.Fatalf("the pause should be noted: %+v", runs[0].Gaps)
	}
}

func TestSmallBuildLongGapSplits(t *testing.T) {
	small := buildA[:2]
	a, end := volley(0, 30, small, 100)
	// 9 minutes: not long enough, and 2 sources never make a candidate.
	b, _ := volley(end+9*60_000, 30, small, 100)
	runs, _ := run(Config{}, cat(a, b))
	if len(runs) != 1 {
		t.Fatalf("9 min pause split a small build: %d runs", len(runs))
	}
	// 10 minutes: new run, immediately, with reason longgap.
	b, _ = volley(end+10*60_000, 30, small, 100)
	runs, res := run(Config{}, cat(a, b))
	if len(runs) != 2 || runs[0].EndReason != EndLongGap || !runs[0].Inferred || runs[0].Lines != 30 || runs[1].Lines != 30 {
		t.Fatalf("long gap: %d runs, %+v", len(runs), runs[0])
	}
	if len(res) != 4 {
		t.Fatalf("results: %d", len(res))
	}
	// A big build with the same pause is judged by its look-ahead instead.
	a2, end2 := volley(0, 40, buildA, 100)
	b2, _ := volley(end2+10*60_000, 40, buildA, 100)
	runs, _ = run(Config{}, cat(a2, b2))
	if len(runs) != 1 {
		t.Fatalf("big build split by time alone: %d runs", len(runs))
	}
}

func TestFlushResolvesPendingCandidate(t *testing.T) {
	a, end := volley(0, 40, buildA, 100)
	b, _ := volley(end+5_000, 10, buildB, 100) // exactly the 10-hit minimum, far short of the horizon
	s := New(Config{})
	var res []Result
	for _, l := range cat(a, b) {
		res = append(res, s.Push(l)...)
	}
	if s.Pending() != 10 {
		t.Fatalf("Pending = %d, want 10", s.Pending())
	}
	if len(ended(res)) != 0 {
		t.Fatal("run ended before its candidate resolved")
	}
	res = append(res, s.Flush()...)
	runs := ended(res)
	if len(runs) != 2 || runs[0].EndReason != EndReset || runs[1].Lines != 10 || runs[0].Lines != 40 || s.Pending() != 0 {
		t.Fatalf("flush: %d runs", len(runs))
	}

	// Nine hits are not enough to decide: one run, every line kept.
	b, _ = volley(end+5_000, 9, buildB, 100)
	runs, _ = run(Config{}, cat(a, b))
	if len(runs) != 1 || runs[0].Lines != 49 || runs[0].EndReason != EndEOF {
		t.Fatalf("9-hit look-ahead decided: %d runs", len(runs))
	}
}

func TestDeathInsideLookaheadEndsIt(t *testing.T) {
	a, end := volley(0, 40, buildA, 100)
	b, end2 := volley(end+5_000, 5, buildB, 100)
	die := []string{fmt.Sprintf("%s  You die.", stamp(end2+500))}
	runs, _ := run(Config{}, cat(a, b, die))
	if len(runs) != 1 || runs[0].EndReason != EndDeath || runs[0].Lines != 46 {
		t.Fatalf("death in look-ahead: %d runs %+v", len(runs), runs[0])
	}
}

func TestActiveMsExcludesPauses(t *testing.T) {
	t3 := 2_500 + 2_999
	lines := []string{
		spellLine(0, "Fireball", 10),
		spellLine(1_000, "Fireball", 10),            // +1000 active
		spellLine(2_500, "Fireball", 10),            // +1500 active
		spellLine(t3, "Fireball", 10),               // +2999 active, just under 3 s
		spellLine(t3+3_000, "Fireball", 10),         // exactly 3 s: a pause
		spellLine(t3+3_500, "Fireball", 10),         // +500
		spellLine(t3+3_500+540_000, "Fireball", 10), // nine-minute pause (a small build only splits at ten)
	}
	runs, _ := run(Config{}, lines)
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
	if got := runs[0].ActiveMs; got != 1000+1500+2999+500 {
		t.Fatalf("ActiveMs = %d, want %d", got, 1000+1500+2999+500)
	}
	if runs[0].LastMono-runs[0].FirstMono <= runs[0].ActiveMs {
		t.Fatal("active time should be less than the wall span")
	}
}

func TestShortRunsAreNotFiltered(t *testing.T) {
	runs, _ := run(Config{}, []string{spellLine(0, "Fireball", 10)})
	if len(runs) != 1 || runs[0].Lines != 1 || runs[0].ActiveMs != 0 {
		t.Fatalf("a one-line run must still be reported: %+v", runs)
	}
}

func TestLookaheadIsBounded(t *testing.T) {
	a, end := volley(0, 40, buildA, 100)
	s := New(Config{MaxLookahead: 50})
	var res []Result
	for _, l := range a {
		res = append(res, s.Push(l)...)
	}
	// A candidate, then lines that never advance the clock (no active time).
	for i := 0; i < 500; i++ {
		res = append(res, s.Push(spellLine(end+5_000, "Frostbolt", 100))...)
		if s.Pending() > 50 {
			t.Fatalf("pending grew to %d", s.Pending())
		}
	}
	runs := ended(append(res, s.Flush()...))
	total := 0
	for _, r := range runs {
		total += r.Lines
	}
	if total != 540 {
		t.Fatalf("lines in runs = %d, want 540", total)
	}
}

// synthetic builds a stream of episodes: build A, B or C at normal or
// collapsed hit sizes, separated by short pauses, long pauses, deaths with quiet
// lines, with unknown lines, blanks, CRLF and crits sprinkled in.
func synthetic(seed int64) []string {
	rng := rand.New(rand.NewSource(seed))
	builds := [][]string{buildA, buildB, {"Mind Blast", "Smite", "Holy Fire", "Renew"}}
	var out []string
	ms := 0
	emit := func(ms int, text string) {
		eol := ""
		if rng.Intn(5) == 0 {
			eol = "\r\n"
		}
		out = append(out, fmt.Sprintf("%s  %s%s", stamp(ms), text, eol))
		if rng.Intn(60) == 0 {
			out = append(out, eol, fmt.Sprintf("%s  something odd", stamp(ms)))
		}
	}
	for ep := 0; ep < 60; ep++ {
		b := builds[rng.Intn(len(builds))]
		dmg := []int{100, 100, 100, 15}[rng.Intn(4)]
		n := 5 + rng.Intn(80)
		for i := 0; i < n; i++ {
			sp := b[rng.Intn(len(b))]
			if rng.Intn(8) == 0 {
				emit(ms, fmt.Sprintf("Your %s crits Kobold Vermin for %d Fire damage.", sp, dmg*2))
			} else {
				emit(ms, fmt.Sprintf("Your %s hits Kobold Vermin for %d Fire damage.", sp, dmg))
			}
			if rng.Intn(10) == 0 {
				emit(ms, "Kobold Vermin dies.")
			}
			if rng.Intn(15) == 0 {
				emit(ms, "Kobold Vermin hits you for 3.")
			}
			ms += rng.Intn(900)
		}
		switch r := rng.Intn(10); {
		case r < 3:
			emit(ms+400, "You die.")
			emit(ms+900, "Corruption fades from Kobold Vermin.")
			ms += 20_000 + rng.Intn(60_000)
			emit(ms, "You gain Perception.")
			ms += 500
		case r < 4:
			ms += 11*60_000 + rng.Intn(120_000)
		case r < 8:
			ms += 3_000 + rng.Intn(30_000)
		default:
			ms += 4_000
			emit(ms, "You gain Divine Shield.")
			ms += 4_000
		}
	}
	return out
}

func TestStreamingEqualsBatches(t *testing.T) {
	resets := 0
	for seed := int64(1); seed <= 12; seed++ {
		lines := synthetic(seed)
		whole, _ := run(Config{}, lines)
		nonBlank := 0
		for _, l := range lines {
			if parse.TrimEOL(l) != "" {
				nonBlank++
			}
		}
		total := 0
		for _, r := range whole {
			total += r.Lines
			if r.EndReason == EndReset {
				resets++
			}
		}
		// Nothing counted twice: runs plus ignored stragglers and discarded
		// pre-combat lines never exceed the input.
		if total > nonBlank {
			t.Fatalf("seed %d: runs own %d lines of %d", seed, total, nonBlank)
		}

		rng := rand.New(rand.NewSource(seed * 77))
		s := New(Config{})
		var res []Result
		for i := 0; i < len(lines); {
			n := 1 + rng.Intn(60)
			if i+n > len(lines) {
				n = len(lines) - i
			}
			res = append(res, s.PushLines(lines[i:i+n])...)
			i += n
		}
		res = append(res, s.Flush()...)
		if got := ended(res); !reflect.DeepEqual(got, whole) {
			t.Fatalf("seed %d: batch split changed the runs", seed)
		}

		s = New(Config{})
		res = nil
		for _, l := range lines {
			l = parse.TrimEOL(l)
			if l == "" {
				continue
			}
			res = append(res, s.PushEvent(l, parse.Parse(l))...)
		}
		res = append(res, s.Flush()...)
		if got := ended(res); !reflect.DeepEqual(got, whole) {
			t.Fatalf("seed %d: PushEvent differs from Push", seed)
		}
	}
	if resets < 5 {
		t.Fatalf("synthetic streams produced only %d resets; the equivalence test is too tame", resets)
	}
}

func TestStartedBeforeEndedAndIndexes(t *testing.T) {
	_, res := run(Config{}, synthetic(3))
	open, started := 0, 0
	for _, r := range res {
		switch r.Kind {
		case RunStarted:
			open++
			started++
			if open != 1 || r.Run.Index != started {
				t.Fatalf("bad start: open=%d index=%d", open, r.Run.Index)
			}
		case RunEnded:
			open--
			if open != 0 || !r.Run.Ended || r.Run.EndReason == "" {
				t.Fatal("bad end result")
			}
		}
	}
	if open != 0 {
		t.Fatal("run left open after Flush")
	}
}

func TestMaxSilenceSplitsEvenIfBuildReturns(t *testing.T) {
	a, end := volley(0, 40, buildA, 100)
	b, _ := volley(end+30*60_000, 40, buildA, 100) // same build, exactly 30 min later
	runs, res := run(Config{}, cat(a, b))
	if len(runs) != 2 || runs[0].EndReason != EndMaxGap || !runs[0].Inferred || runs[0].Lines != 40 || runs[1].Lines != 40 {
		t.Fatalf("30 min pause: %d runs", len(runs))
	}
	// Decided at the boundary line, with no look-ahead held.
	s := New(Config{})
	for _, l := range a {
		s.Push(l)
	}
	if got := ended(s.Push(b[0])); len(got) != 1 || s.Pending() != 0 {
		t.Fatalf("maxgap should resolve immediately, pending=%d", s.Pending())
	}
	if len(res) != 4 {
		t.Fatalf("results: %d", len(res))
	}
	// One millisecond short of 30 minutes is still the old behaviour.
	b, _ = volley(end+30*60_000-1, 40, buildA, 100)
	runs, _ = run(Config{}, cat(a, b))
	if len(runs) != 1 {
		t.Fatalf("just under 30 min split: %d runs", len(runs))
	}
	// Configurable, and applies to a small build too.
	b, _ = volley(end+60_000, 40, buildA, 100)
	runs, _ = run(Config{MaxSilence: time.Minute}, cat(a, b))
	if len(runs) != 2 || runs[0].EndReason != EndMaxGap {
		t.Fatalf("custom MaxSilence: %d runs", len(runs))
	}
}

func TestMaxSilenceEndsLookahead(t *testing.T) {
	// Candidate after 5 s with only 4 hits, then 40 min of silence, then a
	// full build: the look-ahead must not borrow hits from after the hard split.
	a, end := volley(0, 40, buildA, 100)
	b, end2 := volley(end+5_000, 4, buildA, 100)
	c, _ := volley(end2+40*60_000, 40, buildA, 100)
	runs, _ := run(Config{}, cat(a, b, c))
	if len(runs) != 2 || runs[0].EndReason != EndMaxGap || runs[0].Lines != 44 || runs[1].Lines != 40 {
		t.Fatalf("%d runs", len(runs))
	}
}
