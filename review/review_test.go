package review

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

func run(minute, seconds int, spell string) string {
	var b strings.Builder
	for i := 0; i < seconds; i++ {
		fmt.Fprintf(&b, "1/2 10:%02d:%02d.000  Your %s hits Kobold for 40.\r\n", minute+i/60, i%60, spell)
		fmt.Fprintf(&b, "1/2 10:%02d:%02d.500  Felhunter hits Kobold for 5.\r\n", minute+i/60, i%60)
	}
	fmt.Fprintf(&b, "1/2 10:%02d:%02d.000  Kobold dies.\r\n\r\n", minute+seconds/60, seconds%60)
	fmt.Fprintf(&b, "1/2 10:%02d:%02d.100  You die.\r\n", minute+seconds/60, seconds%60)
	return b.String()
}

func gunzip(t *testing.T, data []byte) string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func clean(text string) string {
	lines := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestScanFindsRuns(t *testing.T) {
	s, err := Open([]byte(run(0, 70, "Fireball") + run(5, 10, "Frostbolt")))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Runs) != 2 || s.Unknown != 0 {
		t.Fatalf("runs = %+v, unknown = %d", s.Runs, s.Unknown)
	}
	first := s.Runs[0]
	if first.Start != "01/02 10:00:00" || first.Kills != 1 || first.Hits != 70 || first.Damage != 2800 || first.PetDamage != 350 {
		t.Errorf("first run: %+v", first)
	}
	if first.TopSpells[0].Name != "Fireball" || first.EndReason != "death" || first.DPS <= 0 {
		t.Errorf("first run: %+v", first)
	}
}

func TestExtractIsExactAndInLogOrder(t *testing.T) {
	a, b, c := run(0, 70, "Fireball"), run(5, 70, "Frostbolt"), run(10, 70, "Shadow Bolt")
	s, err := Open([]byte(a + b + c))
	if err != nil {
		t.Fatal(err)
	}
	gz, err := s.Extract([]int{3, 1, 99})
	if err != nil {
		t.Fatal(err)
	}
	got := gunzip(t, gz)
	if got != clean(a+c) {
		t.Fatalf("extract differs: %d bytes, want %d", len(got), len(clean(a+c)))
	}

	// What is sent must split into the same runs on the server.
	again, err := Open([]byte(got))
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Runs) != 2 || again.Runs[0].Damage != s.Runs[0].Damage || again.Runs[1].Damage != s.Runs[2].Damage {
		t.Errorf("runs after extract: %+v", again.Runs)
	}
}

// The browser shows a run as already uploaded by its fingerprint, so a run cut out and sent must keep
// the fingerprint the server stores, and different runs must not share one.
func TestFingerprintSurvivesExtract(t *testing.T) {
	a, b, c := run(0, 70, "Fireball"), run(5, 70, "Frostbolt"), run(10, 70, "Shadow Bolt")
	s, err := Open([]byte(a + b + c))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range s.Runs {
		if len(r.Fingerprint) != 64 || seen[r.Fingerprint] {
			t.Fatalf("fingerprints must be 64 hex and distinct: %+v", s.Runs)
		}
		seen[r.Fingerprint] = true
	}
	gz, err := s.Extract([]int{2})
	if err != nil {
		t.Fatal(err)
	}
	again, err := Open([]byte(gunzip(t, gz)))
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Runs) != 1 || again.Runs[0].Fingerprint != s.Runs[1].Fingerprint {
		t.Errorf("fingerprint after extract: %q, want %q", again.Runs[0].Fingerprint, s.Runs[1].Fingerprint)
	}
}

func TestTextAndRunLines(t *testing.T) {
	a, b := run(0, 70, "Fireball"), run(5, 70, "Frostbolt")
	s, err := Open([]byte(a + b))
	if err != nil {
		t.Fatal(err)
	}
	text, err := s.Text([]int{2})
	if err != nil {
		t.Fatal(err)
	}
	gz, _ := s.Extract([]int{2})
	if string(text) != clean(b) || gunzip(t, gz) != string(text) {
		t.Error("Text must equal the unzipped Extract")
	}
	lines := s.RunLines(2)
	if strings.Join(lines, "\n")+"\n" != clean(b) {
		t.Errorf("run lines: %d", len(lines))
	}
	if s.RunLines(9) != nil {
		t.Error("an unknown run has no lines")
	}
	if _, err := s.Text(nil); err == nil {
		t.Error("no runs chosen should fail")
	}
}

func TestExtractNeedsARun(t *testing.T) {
	s, _ := Open([]byte(run(0, 70, "Fireball")))
	if _, err := s.Extract(nil); err == nil {
		t.Error("no runs chosen should fail")
	}
	if _, err := s.Extract([]int{7}); err == nil {
		t.Error("an unknown run alone should fail")
	}
}

func TestOpenReadsGzipAndZip(t *testing.T) {
	text := run(0, 70, "Fireball")

	var gzBuf bytes.Buffer
	gz := gzip.NewWriter(&gzBuf)
	gz.Write([]byte(text))
	gz.Close()
	if s, err := Open(gzBuf.Bytes()); err != nil || len(s.Runs) != 1 {
		t.Errorf("gzip: %v", err)
	}

	var zipBuf bytes.Buffer
	archive := zip.NewWriter(&zipBuf)
	notes, _ := archive.Create("a-much-longer-notes-file.txt")
	notes.Write([]byte(strings.Repeat("notes\n", 100000)))
	logFile, _ := archive.Create("Logs/WoWCombatLog-inno.txt")
	logFile.Write([]byte(text))
	archive.Close()
	s, err := Open(zipBuf.Bytes())
	if err != nil || len(s.Runs) != 1 {
		t.Fatalf("zip: %v", err)
	}
}

func TestOpenRefusesWhatIsNotALog(t *testing.T) {
	var zipBuf bytes.Buffer
	archive := zip.NewWriter(&zipBuf)
	f, _ := archive.Create("picture.png")
	f.Write([]byte("x"))
	archive.Close()
	for name, data := range map[string][]byte{
		"empty":    nil,
		"text":     []byte("hello\nworld\n"),
		"bad gzip": {0x1f, 0x8b, 0, 0},
		"bad zip":  []byte("PK\x03\x04broken"),
		"no log":   zipBuf.Bytes(),
	} {
		if _, err := Open(data); err == nil {
			t.Errorf("%s: should fail", name)
		}
	}
}

// Set ASLOG_REAL_LOG to check the real file: any choice of runs must split the same way again.
func TestRealLogRoundTrip(t *testing.T) {
	path := os.Getenv("ASLOG_REAL_LOG")
	if path == "" {
		t.Skip("ASLOG_REAL_LOG not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	var picks [][]int
	every := []int{}
	alternate := []int{}
	for i, r := range s.Runs {
		every = append(every, r.Index)
		if i%2 == 0 {
			alternate = append(alternate, r.Index)
		}
		picks = append(picks, []int{r.Index})
	}
	picks = append(picks, every, alternate)
	for _, pick := range picks {
		gz, err := s.Extract(pick)
		if err != nil {
			t.Fatal(err)
		}
		again, err := Open(gz)
		if err != nil {
			t.Fatal(err)
		}
		if len(again.Runs) != len(pick) {
			t.Errorf("chose %v, server would find %d runs", pick, len(again.Runs))
			continue
		}
		for i, index := range pick {
			want, got := s.Runs[index-1], again.Runs[i]
			if want.Damage != got.Damage || want.Hits != got.Hits || want.Lines != got.Lines {
				t.Errorf("run %d changed: %+v vs %+v", index, want, got)
			}
		}
	}
	t.Logf("%d runs, %d choices checked", len(s.Runs), len(picks))
}
