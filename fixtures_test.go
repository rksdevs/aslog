package aslog_test

import (
	"bufio"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rksdevs/aslog/parse"
	"github.com/rksdevs/aslog/segment"
)

func readLines(t testing.TB, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

func segmentAll(cfg segment.Config, lines []string) []*segment.Run {
	s := segment.New(cfg)
	var runs []*segment.Run
	for _, l := range lines {
		for _, r := range s.Push(l) {
			if r.Kind == segment.RunEnded {
				runs = append(runs, r.Run)
			}
		}
	}
	for _, r := range s.Flush() {
		if r.Kind == segment.RunEnded {
			runs = append(runs, r.Run)
		}
	}
	return runs
}

func fixture(t testing.TB, name string) []string {
	return readLines(t, filepath.Join("testdata", name))
}

func TestFixtureAllLinesKnown(t *testing.T) {
	for _, name := range []string{"mage_death_restart.log", "day_rollover.log", "gap_in_run.log"} {
		for i, l := range fixture(t, name) {
			if parse.Parse(l).Kind == parse.Unknown {
				t.Errorf("%s:%d unknown line %q", name, i+1, l)
			}
		}
	}
}

func TestFixtureDeathThenRestart(t *testing.T) {
	runs := segmentAll(segment.Config{}, fixture(t, "mage_death_restart.log"))
	if len(runs) != 2 {
		t.Fatalf("runs = %d", len(runs))
	}
	a, b := runs[0], runs[1]
	if a.EndReason != segment.EndDeath || a.Inferred || a.Lines != 71 || a.Last.Hour != 22 || a.Last.Min != 19 || a.Last.Sec != 44 || a.Last.Milli != 22 {
		t.Errorf("run 1: %+v", a)
	}
	// The post-death "You gain Perception." 31 s later opens the next run.
	if b.EndReason != segment.EndEOF || !b.Inferred || b.Lines != 229 || b.First.Min != 20 || b.First.Sec != 15 || b.First.Milli != 61 {
		t.Errorf("run 2: %+v", b)
	}
	if b.Auras["Perception"].Gains < 1 {
		t.Errorf("run 2 should own its opening buff: %v", b.Auras)
	}
	if a.Fingerprint == b.Fingerprint || len(a.Fingerprint) != 64 {
		t.Errorf("fingerprints: %s %s", a.Fingerprint, b.Fingerprint)
	}
}

func TestFixtureDayRolloverGap(t *testing.T) {
	runs := segmentAll(segment.Config{}, fixture(t, "day_rollover.log"))
	if len(runs) != 2 {
		t.Fatalf("runs = %d", len(runs))
	}
	if runs[0].EndReason != segment.EndDeath || runs[0].Lines != 29 {
		t.Errorf("run 1: %+v", runs[0])
	}
	if runs[1].First.Day != 6 || runs[1].First.Hour != 0 || runs[1].First.Min != 4 || runs[1].Lines != 42 {
		t.Errorf("run 2: %+v", runs[1])
	}
	// The stream clock keeps counting across the 11/5 -> 11/6 jump.
	span := runs[1].FirstMono - runs[0].LastMono
	want := int64(90*60+40)*1000 + 730 // 22:33:55.229 -> 00:04:35.959
	if span != want {
		t.Errorf("mono step across midnight = %d, want %d", span, want)
	}
}

// The 57 s pause between the End Run run and the Quit run: the build is lost,
// so it is a reset at the first line after the pause. The fixture ends before
// the look-ahead horizon, so Flush resolves the candidate.
func TestFixtureResetAfterPause(t *testing.T) {
	runs := segmentAll(segment.Config{}, fixture(t, "gap_in_run.log"))
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(runs))
	}
	a, b := runs[0], runs[1]
	if a.EndReason != segment.EndReset || !a.Inferred || a.Lines != 56 || a.Last.Min != 13 || a.Last.Sec != 15 || a.Last.Milli != 879 {
		t.Errorf("run 1: %+v", a)
	}
	if b.EndReason != segment.EndEOF || b.Lines != 45 || b.First.Min != 14 || b.First.Sec != 12 || b.First.Milli != 976 {
		t.Errorf("run 2: %+v", b)
	}
	if a.Lines+b.Lines != 101 {
		t.Errorf("lines lost or doubled: %d", a.Lines+b.Lines)
	}
}

// Whole file, line by line, CRLF-converted, and random batch splits must agree.
func TestFixtureStreamingEquivalence(t *testing.T) {
	for _, name := range []string{"mage_death_restart.log", "day_rollover.log", "gap_in_run.log"} {
		lines := fixture(t, name)
		want := segmentAll(segment.Config{}, lines)

		crlf := make([]string, len(lines))
		for i, l := range lines {
			crlf[i] = l + "\r\n"
		}
		if got := segmentAll(segment.Config{}, crlf); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: CRLF differs", name)
		}

		for seed := int64(0); seed < 20; seed++ {
			rng := rand.New(rand.NewSource(seed))
			s := segment.New(segment.Config{})
			var res []segment.Result
			for i := 0; i < len(lines); {
				n := 1 + rng.Intn(80)
				if i+n > len(lines) {
					n = len(lines) - i
				}
				res = append(res, s.PushLines(lines[i:i+n])...)
				i += n
			}
			res = append(res, s.Flush()...)
			var got []*segment.Run
			for _, r := range res {
				if r.Kind == segment.RunEnded {
					got = append(got, r.Run)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s seed %d: batch split differs", name, seed)
			}
		}

		// Whole-text split on newlines, as a finished-file reader would.
		text := strings.Join(lines, "\n") + "\n"
		if got := segmentAll(segment.Config{}, strings.Split(strings.TrimSuffix(text, "\n"), "\n")); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: whole-text split differs", name)
		}
	}
}
