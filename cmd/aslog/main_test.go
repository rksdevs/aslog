package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

const sample = `1/2 10:00:00.000  Your Fireball hits Kobold for 40.
not a log line
1/2 10:00:01.000  You die.
`

func TestReadCountsLinesAndRuns(t *testing.T) {
	rep, err := read(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Lines != 3 || rep.Unknown != 1 || len(rep.Runs) != 1 {
		t.Fatalf("lines=%d unknown=%d runs=%d", rep.Lines, rep.Unknown, len(rep.Runs))
	}
}

func TestPrintMarksShortRuns(t *testing.T) {
	rep, err := read(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	printReport(&out, rep, true, time.Minute)
	for _, want := range []string{"3 lines, 1 unrecognised, 1 runs", "death, short", "Fireball 40"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}
