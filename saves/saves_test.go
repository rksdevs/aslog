package saves

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseLiteralOnly(t *testing.T) {
	got, err := Parse("\xef\xbb\xbf-- saved\nDB = { [\"a\"] = 1, b = 'x\\\"y', [3] = true, { 1, 2 }, nope = nil, n = -2.5e3, }\nOther = \"z\"")
	if err != nil {
		t.Fatal(err)
	}
	db := Map(got["DB"])
	if Float(db["a"]) != 1 || String(db["b"]) != `x"y` || !Bool(db["3"]) || Float(db["n"]) != -2500 || String(got["Other"]) != "z" {
		t.Errorf("parsed %#v", got)
	}
	for _, src := range []string{`DB = os.exit()`, `DB = { f = function() end }`, `DB = "open`, `= 1`} {
		if _, err := Parse(src); !errors.Is(err, ErrBadLua) {
			t.Errorf("%q: want ErrBadLua, got %v", src, err)
		}
	}
	if _, err := Parse("DB = " + strings.Repeat("{", 40) + strings.Repeat("}", 40)); !errors.Is(err, ErrBadLua) {
		t.Error("deep nesting should fail")
	}
}

func TestWriteRoundTrips(t *testing.T) {
	value := map[string]any{
		"text":   "quote \" back \\ tab \t line\nbell \a7 end",
		"list":   []any{1.0, 2.5, "x", true, false, map[string]any{"k": -0.125}},
		"gapped": map[string]any{"1": "a", "3": "c"},
		"big":    1e21,
		"empty":  []any{},
		"odd":    map[string]any{"has space": 1.0, "1.5": 2.0},
	}
	src, err := Write("DB", value)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(string(src))
	if err != nil {
		t.Fatalf("%v\n%s", err, src)
	}
	if !reflect.DeepEqual(back["DB"], value) {
		t.Errorf("round trip changed the data:\n%#v\n%#v", back["DB"], value)
	}
	again, _ := Write("DB", back["DB"])
	if string(again) != string(src) {
		t.Error("writing the same data twice should give the same file")
	}
	if _, err := Write("DB", map[string]any{"f": func() {}}); !errors.Is(err, ErrBadLua) {
		t.Error("unwritable values should fail")
	}
}

func syncRun(lines ...string) map[string]any {
	sync := make([]any, len(lines))
	for i, line := range lines {
		sync[i] = map[string]any{"text": line}
	}
	return map[string]any{"schema": 2.0, "chat": map[string]any{"sync": sync}}
}

func TestMatchOurs(t *testing.T) {
	lines := []string{
		"11/7 10:00:01.000  Your Fireball hits Kobold for 40.",
		"11/7 10:00:02.000  Your Fireball crits Kobold for 80.",
		"11/7 10:00:03.000  Kobold dies.",
		"11/7 10:00:04.000  Your Fireball hits Wolf for 41.",
	}
	right := syncRun("Your Fireball hits Kobold for 40.", "Kobold dies.", "Your Fireball hits Wolf for 41.")
	wrongOrder := syncRun("Your Fireball hits Wolf for 41.", "Kobold dies.", "Your Fireball hits Kobold for 40.")
	other := syncRun("Your Frostbolt hits Kobold for 40.", "Murloc dies.", "Your Frostbolt hits Wolf for 41.")
	if got := MatchOurs([]map[string]any{other, wrongOrder, right}, lines); !reflect.DeepEqual(got, right) {
		t.Errorf("picked %v", got)
	}
	if got := MatchOurs([]map[string]any{other, wrongOrder}, lines); got != nil {
		t.Errorf("should not match, got %v", got)
	}
	if got := MatchOurs([]map[string]any{syncRun("Your Fireball hits Kobold for 40.")}, lines); got != nil {
		t.Errorf("one line is not enough, got %v", got)
	}
}

func TestMatchStats(t *testing.T) {
	run := func(kills, seconds float64) map[string]any {
		return map[string]any{"summary": map[string]any{"kills": kills, "t": seconds}}
	}
	right, closer := run(373, 190), run(373, 195)
	if got := MatchStats([]map[string]any{run(373, 900), run(12, 186.9), right}, 373, 198_000); !reflect.DeepEqual(got, right) {
		t.Errorf("picked %v", got)
	}
	if got := MatchStats([]map[string]any{run(373, 150)}, 373, 198_000); got != nil {
		t.Error("more than 20 s apart should not match")
	}
	if got := MatchStats([]map[string]any{right, closer}, 373, 198_000); !reflect.DeepEqual(got, closer) {
		t.Error("the closest clock should win")
	}
}

func TestCopiesReadBack(t *testing.T) {
	ours := []map[string]any{{"schema": 2.0, "gameRunId": "abc-1"}}
	src, err := OurCopy(ours)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := OurRuns(string(src))
	if err != nil || len(runs) != 1 || runs[0]["gameRunId"] != "abc-1" {
		t.Errorf("our copy: %v %v", runs, err)
	}

	stats := []map[string]any{{"summary": map[string]any{"kills": 3.0, "t": 70.0}}}
	src, err = StatsCopy("2.1.0", stats)
	if err != nil {
		t.Fatal(err)
	}
	got, version, err := StatsRuns(string(src))
	if err != nil || len(got) != 1 || version != "2.1.0" {
		t.Errorf("stats copy: %v %q %v", got, version, err)
	}
}

// TestRealSaves round-trips the developer's own save files when they are present.
func TestRealSaves(t *testing.T) {
	dir := os.Getenv("AS_SAVED_DIR")
	if dir == "" {
		dir = `D:\AzerothSurvivorsWindowsNew\benilla-config\saved`
	}
	for _, name := range []string{"WowLogsAS.lua", "AzerothStats.lua"} {
		data, err := os.ReadFile(dir + string(os.PathSeparator) + name)
		if err != nil {
			t.Logf("skip %s: %v", name, err)
			continue
		}
		saved, err := Parse(string(data))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for variable, value := range saved {
			src, err := Write(variable, value)
			if err != nil {
				t.Fatalf("%s %s: %v", name, variable, err)
			}
			back, err := Parse(string(src))
			if err != nil {
				t.Fatalf("%s %s: written file doesn't parse: %v", name, variable, err)
			}
			if !reflect.DeepEqual(back[variable], value) {
				t.Errorf("%s %s: round trip changed the data", name, variable)
			}
		}
	}
}
