package segment

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/rksdevs/aslog/parse"
)

// at builds a line stamped on 11/5 at h:m:s.ms.
func at(h, m, s, ms int, text string) string {
	return fmt.Sprintf("11/5 %02d:%02d:%02d.%03d  %s", h, m, s, ms, text)
}

func atDay(day, h, m, s int, text string) string {
	return fmt.Sprintf("11/%d %02d:%02d:%02d.000  %s", day, h, m, s, text)
}

const (
	hit    = "Your Fireball hits Kobold Vermin for 70 Fire damage."
	crit   = "Your Fireball crits Kobold Vermin for 140 Fire damage."
	tick   = "Kobold Vermin suffers 10 Shadow damage from your Corruption."
	taken  = "Kobold Vermin hits you for 3."
	dies   = "Kobold Vermin dies."
	youDie = "You die."
)

// run feeds lines one at a time and returns every ended run, plus the raw
// result stream.
func run(cfg Config, lines []string) ([]*Run, []Result) {
	s := New(cfg)
	var all []Result
	for _, l := range lines {
		all = append(all, s.Push(l)...)
	}
	all = append(all, s.Flush()...)
	return ended(all), all
}

func ended(rs []Result) []*Run {
	var out []*Run
	for _, r := range rs {
		if r.Kind == RunEnded {
			out = append(out, r.Run)
		}
	}
	return out
}

func TestDeathEndsRun(t *testing.T) {
	runs, res := run(Config{}, []string{
		at(22, 0, 0, 0, hit),
		at(22, 0, 1, 0, taken),
		at(22, 0, 2, 0, youDie),
	})
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
	r := runs[0]
	if r.EndReason != EndDeath || r.Inferred || !r.Ended || r.Lines != 3 {
		t.Fatalf("bad run: %+v", r)
	}
	if res[0].Kind != RunStarted || res[1].Kind != RunEnded || len(res) != 2 {
		t.Fatalf("result order: %+v", res)
	}
	if r.Last.Sec != 2 {
		t.Fatalf("death line must be the last line: %+v", r.Last)
	}
}

func TestEOFFlush(t *testing.T) {
	runs, _ := run(Config{}, []string{at(22, 0, 0, 0, hit)})
	if len(runs) != 1 || runs[0].EndReason != EndEOF || !runs[0].Inferred {
		t.Fatalf("%+v", runs)
	}
	// Flush with nothing active emits nothing, and twice is harmless.
	s := New(Config{})
	if len(s.Flush()) != 0 || len(s.Flush()) != 0 {
		t.Fatal("empty flush emitted results")
	}
}

func TestNoPhantomRunAfterDeath(t *testing.T) {
	lines := []string{
		at(22, 0, 0, 0, hit),
		at(22, 0, 1, 0, youDie),
		// quiet, non-combat lines only
		at(22, 0, 30, 0, "Corruption fades from Kobold Vermin."),
		at(22, 0, 31, 0, "Perception fades from you."),
		at(22, 0, 32, 0, "You gain Divine Shield."),
		at(22, 0, 33, 0, youDie),
		at(22, 0, 34, 0, "totally unknown line"),
	}
	runs, res := run(Config{}, lines)
	if len(runs) != 1 || len(res) != 2 {
		t.Fatalf("phantom run: %d runs, %d results", len(runs), len(res))
	}
}

func TestPostDeathStragglersDoNotStartRun(t *testing.T) {
	lines := []string{
		at(22, 0, 0, 0, hit),
		at(22, 0, 1, 0, youDie),
		at(22, 0, 1, 500, tick), // DoT keeps ticking on the corpse
		at(22, 0, 2, 100, taken),
		at(22, 0, 2, 900, dies),
		at(22, 0, 10, 0, "You gain Perception."),
		at(22, 0, 11, 0, hit), // fresh combat
	}
	s := New(Config{})
	var all []Result
	for _, l := range lines {
		all = append(all, s.Push(l)...)
	}
	all = append(all, s.Flush()...)
	runs := ended(all)
	if len(runs) != 2 {
		t.Fatalf("runs = %d", len(runs))
	}
	if s.Stragglers() != 3 {
		t.Fatalf("stragglers = %d, want 3", s.Stragglers())
	}
	if runs[1].Lines != 2 || runs[1].First.Sec != 10 {
		t.Fatalf("second run should own the pre-combat buff: %+v", runs[1])
	}
	// With the quiet window disabled the straggling tick does start a run.
	runs, _ = run(Config{PostDeathQuiet: -1}, lines[:4])
	if len(runs) != 2 {
		t.Fatalf("quiet disabled: runs = %d, want 2", len(runs))
	}
}

func TestAggregates(t *testing.T) {
	lines := []string{
		at(22, 0, 0, 0, "You gain Perception."),
		at(22, 0, 1, 0, hit),
		at(22, 0, 1, 1, crit),
		at(22, 0, 1, 2, tick),
		at(22, 0, 1, 3, "Your Auto Shot hits Kobold Vermin for 48."),
		at(22, 0, 1, 4, "You hit Kobold Vermin for 12."),
		at(22, 0, 1, 5, "You crit Kobold Vermin for 24."),
		at(22, 0, 2, 0, taken),
		at(22, 0, 2, 1, "Gruff Swiftbite crits you for 50. (crushing)"),
		at(22, 0, 2, 2, "Rhahk'Zor's Shadow Bolt hits you for 7 Shadow damage."),
		at(22, 0, 3, 0, dies),
		at(22, 0, 3, 1, dies),
		at(22, 0, 3, 2, "Gnoll dies."),
		at(22, 0, 4, 0, "Kobold Vermin is afflicted by Corruption."),
		at(22, 0, 4, 1, "Corruption fades from Kobold Vermin."),
		at(22, 0, 4, 2, "Something new happened."),
		"not a log line at all",
	}
	runs, _ := run(Config{}, lines)
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
	r := runs[0]
	if r.Lines != len(lines) {
		t.Errorf("Lines = %d, want %d", r.Lines, len(lines))
	}
	if r.Hits != 6 || r.Crits != 2 || r.Ticks != 1 {
		t.Errorf("hits/crits/ticks = %d/%d/%d, want 6/2/1", r.Hits, r.Crits, r.Ticks)
	}
	if r.DamageOut != 70+140+10+48+12+24 {
		t.Errorf("DamageOut = %d", r.DamageOut)
	}
	wantSpell := map[string]int64{"Fireball": 210, "Corruption": 10, "Auto Shot": 48, MeleeSpell: 36}
	if !reflect.DeepEqual(r.DamageBySpell, wantSpell) {
		t.Errorf("DamageBySpell = %v", r.DamageBySpell)
	}
	if r.DamageTaken != 3+50+7 {
		t.Errorf("DamageTaken = %d", r.DamageTaken)
	}
	wantTaken := map[string]int64{"Kobold Vermin": 3, "Gruff Swiftbite": 50, "Rhahk'Zor": 7}
	if !reflect.DeepEqual(r.TakenByAttacker, wantTaken) {
		t.Errorf("TakenByAttacker = %v", r.TakenByAttacker)
	}
	if !reflect.DeepEqual(r.Kills, map[string]int{"Kobold Vermin": 2, "Gnoll": 1}) || r.KillCount() != 3 {
		t.Errorf("Kills = %v", r.Kills)
	}
	if r.Auras["Corruption"] != (AuraStat{Afflicts: 1, Fades: 1}) || r.Auras["Perception"] != (AuraStat{Gains: 1}) {
		t.Errorf("Auras = %v", r.Auras)
	}
	if r.Unknown != 2 {
		t.Errorf("Unknown = %d, want 2", r.Unknown)
	}
}

func TestNoteGaps(t *testing.T) {
	runs, _ := run(Config{}, []string{
		at(22, 0, 0, 0, hit),
		at(22, 0, 29, 999, hit), // under 30 s
		at(22, 1, 0, 0, hit),    // 30.001 s
		at(22, 1, 1, 0, hit),
	})
	if len(runs) != 1 || len(runs[0].Gaps) != 1 {
		t.Fatalf("gaps: %+v", runs[0].Gaps)
	}
	g := runs[0].Gaps[0]
	if g.MS != 30001 || g.AfterLine != 2 || g.From.Sec != 29 || g.To.Min != 1 {
		t.Fatalf("gap = %+v", g)
	}
	runs, _ = run(Config{NoteGap: -1}, []string{at(22, 0, 0, 0, hit), at(22, 1, 0, 0, hit)})
	if len(runs[0].Gaps) != 0 {
		t.Fatal("NoteGap<0 should disable gap notes")
	}
}

func TestBlankLinesAndCRLF(t *testing.T) {
	lines := []string{at(22, 0, 0, 0, hit) + "\r\n", "", "\r\n", at(22, 0, 1, 0, youDie) + "\r\n"}
	runs, _ := run(Config{}, lines)
	if len(runs) != 1 || runs[0].Lines != 2 || runs[0].Unknown != 0 || runs[0].EndReason != EndDeath {
		t.Fatalf("%+v", runs[0])
	}
	plain, _ := run(Config{}, []string{at(22, 0, 0, 0, hit), at(22, 0, 1, 0, youDie)})
	if runs[0].Fingerprint != plain[0].Fingerprint {
		t.Fatal("CRLF changed the fingerprint")
	}
}

func TestFingerprint(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, at(22, 0, i, 0, hit))
	}
	runs, _ := run(Config{}, lines)
	canon := strings.Join(lines[:20], "\n") + "\n"
	sum := sha256.Sum256([]byte(canon))
	if runs[0].Fingerprint != hex.EncodeToString(sum[:]) {
		t.Fatal("fingerprint is not SHA-256 over the first 20 canonical lines")
	}
	// Lines after the 20th do not matter; a short run hashes what it has.
	other, _ := run(Config{}, append(append([]string{}, lines[:20]...), at(22, 0, 40, 0, taken)))
	if other[0].Fingerprint != runs[0].Fingerprint {
		t.Fatal("fingerprint depends on lines beyond the first 20")
	}
	short, _ := run(Config{}, lines[:3])
	sum = sha256.Sum256([]byte(strings.Join(lines[:3], "\n") + "\n"))
	if short[0].Fingerprint != hex.EncodeToString(sum[:]) {
		t.Fatal("short run fingerprint wrong")
	}
	changed := append([]string{}, lines[:20]...)
	changed[7] = at(22, 0, 7, 0, crit)
	c, _ := run(Config{}, changed)
	if c[0].Fingerprint == runs[0].Fingerprint {
		t.Fatal("fingerprint ignored a changed line")
	}
}

func TestHints(t *testing.T) {
	lines := []string{
		at(22, 0, 0, 0, hit),
		at(22, 0, 10, 0, hit),
		at(22, 0, 11, 0, hit),
		at(22, 0, 20, 0, hit),
	}
	s := New(Config{})
	s.Hint(parse.Timestamp{Month: 11, Day: 5, Hour: 22, Min: 0, Sec: 10, Milli: 500})
	var all []Result
	for _, l := range lines {
		all = append(all, s.Push(l)...)
	}
	all = append(all, s.Flush()...)
	runs := ended(all)
	if len(runs) != 2 {
		t.Fatalf("runs = %d", len(runs))
	}
	if runs[0].EndReason != EndHint || runs[0].Inferred || runs[0].Lines != 2 {
		t.Fatalf("first: %+v", runs[0])
	}
	if runs[1].First.Sec != 11 || runs[1].Lines != 2 {
		t.Fatalf("second: %+v", runs[1])
	}

	// A hint at or before the current position is stale and ignored.
	s = New(Config{})
	all = nil
	all = append(all, s.Push(at(22, 0, 0, 0, hit))...)
	all = append(all, s.Push(at(22, 0, 5, 0, hit))...)
	s.Hint(parse.Timestamp{Month: 11, Day: 5, Hour: 22, Min: 0, Sec: 5, Milli: 0})
	all = append(all, s.Push(at(22, 0, 6, 0, hit))...)
	all = append(all, s.Flush()...)
	if got := ended(all); len(got) != 1 || got[0].Lines != 3 {
		t.Fatalf("stale hint split the run: %d runs", len(got))
	}

	// A hint landing exactly on a line splits before that line.
	s = New(Config{})
	s.Hint(parse.Timestamp{Month: 11, Day: 5, Hour: 22, Min: 0, Sec: 5})
	all = nil
	all = append(all, s.Push(at(22, 0, 0, 0, hit))...)
	all = append(all, s.Push(at(22, 0, 5, 0, hit))...)
	all = append(all, s.Flush()...)
	if got := ended(all); len(got) != 2 || got[0].EndReason != EndHint {
		t.Fatalf("exact hint: %d runs", len(got))
	}

	// A hint before the first line is ignored; one after it is kept for later.
	s = New(Config{})
	s.Hint(parse.Timestamp{Month: 11, Day: 5, Hour: 21})
	s.Hint(parse.Timestamp{Month: 11, Day: 5, Hour: 22, Min: 0, Sec: 3})
	all = nil
	for _, l := range lines[:2] {
		all = append(all, s.Push(l)...)
	}
	all = append(all, s.Flush()...)
	if got := ended(all); len(got) != 2 || got[0].Lines != 1 {
		t.Fatalf("hint ordering: %d runs", len(got))
	}
}

func TestPetDamageKeptApart(t *testing.T) {
	runs, _ := run(Config{}, []string{
		at(22, 0, 0, 0, hit),
		at(22, 0, 1, 0, "Felhunter hits Skeletal Raider for 120."),
		at(22, 0, 1, 500, "Felhunter crits Skeletal Raider for 240."),
		at(22, 0, 2, 0, youDie),
	})
	r := runs[0]
	if r.PetHits != 2 || r.PetDamage != 360 || r.DamageByPet["Felhunter"] != 360 {
		t.Errorf("pet figures: hits %d damage %d by %v", r.PetHits, r.PetDamage, r.DamageByPet)
	}
	if r.Hits != 1 || r.DamageOut != 70 || r.Unknown != 0 {
		t.Errorf("own figures changed: hits %d damage %d unknown %d", r.Hits, r.DamageOut, r.Unknown)
	}
}

func TestPetHitCanStartRun(t *testing.T) {
	runs, _ := run(Config{}, []string{
		at(22, 0, 0, 0, "Felhunter hits Skeletal Raider for 120."),
		at(22, 0, 1, 0, youDie),
	})
	if len(runs) != 1 || runs[0].PetDamage != 120 {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestShieldedHitsLeftOutOfDamageTaken(t *testing.T) {
	runs, _ := run(Config{}, []string{
		at(22, 0, 0, 0, taken),
		at(22, 0, 1, 0, "You gain Divine Shield."),
		at(22, 0, 1, 100, taken),
		at(22, 0, 1, 200, "Rhahk'Zor's Shadow Bolt hits you for 40 Shadow damage."),
		at(22, 0, 1, 300, "Divine Shield fades from Kobold Vermin."), // not yours: still shielded
		at(22, 0, 1, 400, "Kobold Vermin gains Divine Shield."),
		at(22, 0, 1, 500, taken),
		at(22, 0, 2, 0, "Divine Shield fades from you."),
		at(22, 0, 2, 100, taken),
		at(22, 0, 3, 0, youDie),
	})
	r := runs[0]
	if r.DamageTaken != 6 || r.TakenByAttacker["Kobold Vermin"] != 6 || r.TakenByAttacker["Rhahk'Zor"] != 0 {
		t.Errorf("taken %d by %v", r.DamageTaken, r.TakenByAttacker)
	}
	if r.ShieldedHits != 3 || r.ShieldedTaken != 46 {
		t.Errorf("shielded %d hits, %d damage", r.ShieldedHits, r.ShieldedTaken)
	}
}

func TestShieldEndsWithTheRun(t *testing.T) {
	runs, _ := run(Config{}, []string{
		at(22, 0, 0, 0, hit),
		at(22, 0, 1, 0, "You gain Divine Shield."),
		at(22, 0, 2, 0, youDie),
		at(22, 5, 0, 0, hit),
		at(22, 5, 1, 0, taken),
		at(22, 5, 2, 0, youDie),
	})
	if len(runs) != 2 || runs[1].DamageTaken != 3 || runs[1].ShieldedHits != 0 {
		t.Fatalf("second run taken %d shielded %d", runs[1].DamageTaken, runs[1].ShieldedHits)
	}
}
