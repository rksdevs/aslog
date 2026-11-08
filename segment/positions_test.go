package segment

import "testing"

func TestRunLinePositionsCoverExactlyTheRun(t *testing.T) {
	s := New(Config{})
	lines := []string{
		"1/2 10:00:00.000  You gain Frost Armor.",
		"",
		"1/2 10:00:01.000  Your Fireball hits Kobold for 40.",
		"1/2 10:00:02.000  You die.",
		"1/2 10:00:02.500  Kobold dies.",
		"1/2 10:00:30.000  Your Fireball hits Kobold for 12.",
		"1/2 10:00:31.000  You die.",
	}
	var ended []*Run
	for _, l := range lines {
		for _, r := range s.Push(l) {
			if r.Kind == RunEnded {
				ended = append(ended, r.Run)
			}
		}
	}
	if len(ended) != 2 {
		t.Fatalf("runs = %d, want 2", len(ended))
	}
	// Positions skip the empty line, and the straggler at position 4 belongs to no run.
	want := [][2]int{{1, 3}, {5, 6}}
	for i, r := range ended {
		if r.FirstLine != want[i][0] || r.LastLine != want[i][1] {
			t.Errorf("run %d: lines %d..%d, want %d..%d", i+1, r.FirstLine, r.LastLine, want[i][0], want[i][1])
		}
		if got := r.LastLine - r.FirstLine + 1; got != r.Lines {
			t.Errorf("run %d: span %d, Lines %d", i+1, got, r.Lines)
		}
	}
}
