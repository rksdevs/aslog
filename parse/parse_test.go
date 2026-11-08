package parse

import (
	"strings"
	"testing"
)

const pfx = "11/5 22:05:52.888  "

func TestParseShapes(t *testing.T) {
	tests := []struct {
		name string
		text string
		want Event // TS/HasTS filled in by the test
	}{
		{"spell hit school", "Your Fireball hits Kobold Vermin for 70 Fire damage.",
			Event{Kind: DamageOutSpell, Spell: "Fireball", Target: "Kobold Vermin", School: "Fire", Amount: 70}},
		{"spell crit school", "Your Fireball crits Kobold Vermin for 141 Fire damage.",
			Event{Kind: DamageOutSpell, Spell: "Fireball", Target: "Kobold Vermin", School: "Fire", Amount: 141, Crit: true}},
		{"spell hit physical", "Your Auto Shot hits Kobold Vermin for 48.",
			Event{Kind: DamageOutSpell, Spell: "Auto Shot", Target: "Kobold Vermin", Amount: 48}},
		{"spell crit physical", "Your Auto Shot crits Kobold Vermin for 96.",
			Event{Kind: DamageOutSpell, Spell: "Auto Shot", Target: "Kobold Vermin", Amount: 96, Crit: true}},
		{"melee hit", "You hit Kobold Vermin for 12.",
			Event{Kind: DamageOutMelee, Target: "Kobold Vermin", Amount: 12}},
		{"melee crit", "You crit Kobold Vermin for 24.",
			Event{Kind: DamageOutMelee, Target: "Kobold Vermin", Amount: 24, Crit: true}},
		{"periodic", "Murloc Streamrunner suffers 206 Shadow damage from your Corruption.",
			Event{Kind: DamageOutPeriodic, Target: "Murloc Streamrunner", Spell: "Corruption", School: "Shadow", Amount: 206}},
		{"periodic no school", "Young Wolf suffers 20 damage from your Rend.",
			Event{Kind: DamageOutPeriodic, Target: "Young Wolf", Spell: "Rend", Amount: 20}},
		{"melee taken hit", "Kobold Vermin hits you for 3.",
			Event{Kind: DamageInMelee, Source: "Kobold Vermin", Amount: 3}},
		{"melee taken crit", "Gruff Swiftbite crits you for 56.",
			Event{Kind: DamageInMelee, Source: "Gruff Swiftbite", Amount: 56, Crit: true}},
		{"spell taken", "Rhahk'Zor's Shadow Bolt hits you for 40 Shadow damage.",
			Event{Kind: DamageInSpell, Source: "Rhahk'Zor", Spell: "Shadow Bolt", School: "Shadow", Amount: 40}},
		{"spell taken crit", "Rhahk'Zor's Shadow Bolt crits you for 80 Shadow damage.",
			Event{Kind: DamageInSpell, Source: "Rhahk'Zor", Spell: "Shadow Bolt", School: "Shadow", Amount: 80, Crit: true}},
		{"afflicted", "Kobold Vermin is afflicted by Corruption.",
			Event{Kind: AuraAfflict, Target: "Kobold Vermin", Spell: "Corruption"}},
		{"afflicted stacks", "Kobold Vermin is afflicted by Sunder Armor (2).",
			Event{Kind: AuraAfflict, Target: "Kobold Vermin", Spell: "Sunder Armor", Stacks: 2}},
		{"afflicted rank is part of name", "Kobold Vermin is afflicted by Frost Armor (Rank 1).",
			Event{Kind: AuraAfflict, Target: "Kobold Vermin", Spell: "Frost Armor (Rank 1)"}},
		{"fades from mob", "Corruption fades from Kobold Vermin.",
			Event{Kind: AuraFade, Target: "Kobold Vermin", Spell: "Corruption"}},
		{"fades from you", "Perception fades from you.",
			Event{Kind: AuraFade, Spell: "Perception"}},
		{"you gain", "You gain Divine Shield.",
			Event{Kind: AuraGain, Spell: "Divine Shield"}},
		{"you gain stacks", "You gain Flurry (3).",
			Event{Kind: AuraGain, Spell: "Flurry", Stacks: 3}},
		{"x gains", "Kobold Vermin gains Enrage.",
			Event{Kind: AuraGain, Target: "Kobold Vermin", Spell: "Enrage"}},
		{"x dies", "Kobold Vermin dies.", Event{Kind: Died, Target: "Kobold Vermin"}},
		{"you die", "You die.", Event{Kind: PlayerDied}},

		// Pets and summons.
		{"summon hit", "Felhunter hits Skeletal Raider for 120.",
			Event{Kind: DamageOutPet, Source: "Felhunter", Target: "Skeletal Raider", Amount: 120}},
		{"summon crit", "Felhunter crits Nightbane Worgen Alpha for 240.",
			Event{Kind: DamageOutPet, Source: "Felhunter", Target: "Nightbane Worgen Alpha", Amount: 240, Crit: true}},
		{"summon spell", "Felhunter's Shadow Bite hits Kobold Vermin for 30 Shadow damage.",
			Event{Kind: DamageOutPet, Source: "Felhunter", Spell: "Shadow Bite", Target: "Kobold Vermin", School: "Shadow", Amount: 30}},
		{"summon school no spell", "Felhunter hits Kobold Vermin for 30 Shadow damage.",
			Event{Kind: DamageOutPet, Source: "Felhunter", Target: "Kobold Vermin", School: "Shadow", Amount: 30}},
		{"possessive summon name", "Elder Mottled Boar's Mate hits Kobold Vermin for 7.",
			Event{Kind: DamageOutPet, Source: "Elder Mottled Boar's Mate", Target: "Kobold Vermin", Amount: 7}},
		{"summon trailer", "Felhunter hits Kobold Vermin for 7. (glancing)",
			Event{Kind: DamageOutPet, Source: "Felhunter", Target: "Kobold Vermin", Amount: 7, Trailer: "(glancing)"}},

		// Trailers.
		{"blocked trailer", "Kobold Vermin hits you for 3. (12 blocked)",
			Event{Kind: DamageInMelee, Source: "Kobold Vermin", Amount: 3, Trailer: "(12 blocked)"}},
		{"glancing trailer", "Kobold Vermin hits you for 3. (glancing)",
			Event{Kind: DamageInMelee, Source: "Kobold Vermin", Amount: 3, Trailer: "(glancing)"}},
		{"crushing trailer", "Boss crits you for 300. (crushing)",
			Event{Kind: DamageInMelee, Source: "Boss", Amount: 300, Crit: true, Trailer: "(crushing)"}},
		{"two trailers", "Your Fireball hits Kobold Vermin for 50 Fire damage. (5 resisted) (3 absorbed)",
			Event{Kind: DamageOutSpell, Spell: "Fireball", Target: "Kobold Vermin", School: "Fire", Amount: 50, Trailer: "(5 resisted) (3 absorbed)"}},
		{"periodic absorbed", "Kobold Vermin suffers 10 Shadow damage from your Corruption. (2 absorbed)",
			Event{Kind: DamageOutPeriodic, Target: "Kobold Vermin", Spell: "Corruption", School: "Shadow", Amount: 10, Trailer: "(2 absorbed)"}},
		{"melee out glancing", "You hit Kobold Vermin for 12. (glancing)",
			Event{Kind: DamageOutMelee, Target: "Kobold Vermin", Amount: 12, Trailer: "(glancing)"}},

		// Names.
		{"apostrophe name", "Rhahk'Zor hits you for 7.", Event{Kind: DamageInMelee, Source: "Rhahk'Zor", Amount: 7}},
		{"period name", "Mr. Smite hits you for 7.", Event{Kind: DamageInMelee, Source: "Mr. Smite", Amount: 7}},
		{"period name target", "Your Fireball hits Mr. Smite for 7 Fire damage.",
			Event{Kind: DamageOutSpell, Spell: "Fireball", Target: "Mr. Smite", School: "Fire", Amount: 7}},
		{"colon spell", "Kobold Vermin suffers 5 Shadow damage from your Pendant of the Plague: Corruption.",
			Event{Kind: DamageOutPeriodic, Target: "Kobold Vermin", Spell: "Pendant of the Plague: Corruption", School: "Shadow", Amount: 5}},
		{"colon aura", "Kobold Vermin is afflicted by Pendant of the Plague: Corruption.",
			Event{Kind: AuraAfflict, Target: "Kobold Vermin", Spell: "Pendant of the Plague: Corruption"}},
		{"multi-word dies", "Hogger the Mighty One dies.", Event{Kind: Died, Target: "Hogger the Mighty One"}},
		{"possessive mob melee", "Elder Mottled Boar's Mate hits you for 7.", Event{Kind: DamageInMelee, Source: "Elder Mottled Boar's Mate", Amount: 7}},

		// Anchoring: words that look like verbs or separators inside names.
		{"source contains hits", "Gnoll who hits things hits you for 9.", Event{Kind: DamageInMelee, Source: "Gnoll who hits things", Amount: 9}},
		{"source contains hits crit", "Gnoll who hits things crits you for 9.", Event{Kind: DamageInMelee, Source: "Gnoll who hits things", Amount: 9, Crit: true}},
		{"target contains hits", "Your Fireball hits Gnoll who hits hard for 9 Fire damage.",
			Event{Kind: DamageOutSpell, Spell: "Fireball", Target: "Gnoll who hits hard", School: "Fire", Amount: 9}},
		{"target contains for", "Your Fireball hits Warden for Hire for 9 Fire damage.",
			Event{Kind: DamageOutSpell, Spell: "Fireball", Target: "Warden for Hire", School: "Fire", Amount: 9}},
		{"target contains for melee", "You hit Warden for Hire for 9.", Event{Kind: DamageOutMelee, Target: "Warden for Hire", Amount: 9}},
		{"target contains digits", "Your Fireball hits Gnoll 12 for 9 Fire damage.",
			Event{Kind: DamageOutSpell, Spell: "Fireball", Target: "Gnoll 12", School: "Fire", Amount: 9}},
		{"target contains suffers", "Gnoll suffers greatly suffers 5 Shadow damage from your Corruption.",
			Event{Kind: DamageOutPeriodic, Target: "Gnoll suffers greatly", Spell: "Corruption", School: "Shadow", Amount: 5}},
		{"target contains dies word", "Gnoll dies hard dies.", Event{Kind: Died, Target: "Gnoll dies hard"}},
		{"target contains afflicted", "A is afflicted by B is afflicted by Weakness.",
			Event{Kind: AuraAfflict, Target: "A", Spell: "B is afflicted by Weakness"}},
		{"spell taken multiword source", "Defias Pillager's Shadow Bolt hits you for 40 Shadow damage.",
			Event{Kind: DamageInSpell, Source: "Defias Pillager", Spell: "Shadow Bolt", School: "Shadow", Amount: 40}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.want
			want.TS = Timestamp{Month: 11, Day: 5, Hour: 22, Min: 5, Sec: 52, Milli: 888}
			want.HasTS = true
			got := Parse(pfx + tc.text)
			if got != want {
				t.Fatalf("Parse(%q)\n got %+v\nwant %+v", tc.text, got, want)
			}
		})
	}
}

func TestParseUnknown(t *testing.T) {
	lines := []string{
		"",
		"garbage",
		"11/5 22:05:52.888",            // no text
		"11/5 22:05:52.888 ",           // one space
		"11/5 22:05:52.888   You die.", // three spaces: text starts with a space
		"11/5 22:05:52.8  You die.",    // short millis
		"13/5 22:05:52.888  You die.",  // bad month
		"11/32 22:05:52.888  You die.", // bad day
		"11/5 24:05:52.888  You die.",  // bad hour
		"11/5 22:05:52.888  Your Fireball was resisted by Kobold Vermin.",
		"11/5 22:05:52.888  Your Fireball hits you for 5 Fire damage.", // self target is not guessed
		"11/5 22:05:52.888  Kobold Vermin hits you for lots.",
		"11/5 22:05:52.888  Fireball hits you for 5 Fire damage.", // spell damage without a caster
		"11/5 22:05:52.888  Kobold Vermin gains 10 Mana from Bob's Mana Spring.",
		"11/5 22:05:52.888  You gain 10 Mana from Bob's Mana Spring.",
		"11/5 22:05:52.888  Your Fireball hits Kobold Vermin for 5 Fire.",
		"11/5 22:05:52.888  Your Fireball hits Kobold Vermin for 99999999999.",
		"11/5 22:05:52.888  You suffer 5 Fire damage from Bob's Fireball.",
	}
	for _, l := range lines {
		ev := Parse(l)
		if ev.Kind != Unknown {
			t.Errorf("Parse(%q) = %+v, want Unknown", l, ev)
		}
		if ev.Amount != 0 || ev.Spell != "" || ev.Target != "" || ev.Source != "" || ev.Trailer != "" {
			t.Errorf("Parse(%q) left fields set on Unknown: %+v", l, ev)
		}
	}
	// Unknown text still carries its timestamp.
	ev := Parse("11/5 22:05:52.888  Something new happened.")
	if ev.Kind != Unknown || !ev.HasTS || ev.TS.Milli != 888 {
		t.Fatalf("unknown line lost its timestamp: %+v", ev)
	}
	if Parse("nope").HasTS {
		t.Fatal("garbage must not report a timestamp")
	}
}

func TestParseCRLF(t *testing.T) {
	base := Parse(pfx + "Kobold Vermin hits you for 3.")
	for _, eol := range []string{"\n", "\r\n", "\r", "\r\n\r\n"} {
		if got := Parse(pfx + "Kobold Vermin hits you for 3." + eol); got != base {
			t.Errorf("eol %q: got %+v want %+v", eol, got, base)
		}
	}
	if Parse(pfx+"You die.\r\n").Kind != PlayerDied {
		t.Error("CRLF broke You die.")
	}
	if Parse(pfx+"Kobold Vermin hits you for 3. (glancing)\r\n").Trailer != "(glancing)" {
		t.Error("CRLF broke trailer")
	}
}

func TestParseTimestampSingleDigits(t *testing.T) {
	ev := Parse("1/2 3:04:05.006  You die.")
	want := Timestamp{Month: 1, Day: 2, Hour: 3, Min: 4, Sec: 5, Milli: 6}
	if ev.Kind != PlayerDied || ev.TS != want {
		t.Fatalf("got %+v", ev)
	}
}

func TestMonoRollover(t *testing.T) {
	ts := func(mo, d, h, mi, s, ms int) Timestamp {
		return Timestamp{uint8(mo), uint8(d), uint8(h), uint8(mi), uint8(s), uint16(ms)}
	}
	a := ts(11, 5, 23, 59, 59, 900)
	b := ts(11, 6, 0, 0, 0, 100)
	ma := a.Mono(0, false)
	mb := b.Mono(ma, true)
	if mb-ma != 200 {
		t.Errorf("midnight rollover step = %d, want 200", mb-ma)
	}

	// Year rollover.
	c := ts(12, 31, 23, 59, 59, 0)
	d := ts(1, 1, 0, 0, 1, 0)
	mc := c.Mono(0, false)
	md := d.Mono(mc, true)
	if md-mc != 2000 {
		t.Errorf("year rollover step = %d, want 2000", md-mc)
	}

	// Feb 29 exists in the imaginary year, so leap logs are contiguous.
	e := ts(2, 28, 23, 59, 59, 0)
	f := ts(2, 29, 0, 0, 0, 0)
	g := ts(3, 1, 0, 0, 0, 0)
	me := e.Mono(0, false)
	mf := f.Mono(me, true)
	mg := g.Mono(mf, true)
	if mf-me != 1000 || mg-mf != msPerDay {
		t.Errorf("leap steps = %d, %d", mf-me, mg-mf)
	}

	// A long multi-day stretch accumulates.
	m := ts(11, 5, 12, 0, 0, 0).Mono(0, false)
	m = ts(11, 7, 12, 0, 0, 0).Mono(m, true)
	if m-ts(11, 5, 12, 0, 0, 0).Raw() != 2*msPerDay {
		t.Errorf("two day step wrong")
	}

	// Backwards jitter is clamped, never decreases.
	x := ts(11, 5, 10, 0, 0, 500)
	mx := x.Mono(0, false)
	my := ts(11, 5, 10, 0, 0, 400).Mono(mx, true)
	if my != mx {
		t.Errorf("backward step not clamped: %d vs %d", my, mx)
	}
	// ...and the stream continues correctly afterwards.
	mz := ts(11, 5, 10, 0, 1, 0).Mono(my, true)
	if mz-mx != 500 {
		t.Errorf("step after jitter = %d, want 500", mz-mx)
	}
}

func TestKindString(t *testing.T) {
	for k := Unknown; k <= DamageOutPet; k++ {
		if k.String() == "" || k.String() == "invalid" {
			t.Errorf("kind %d has no name", k)
		}
	}
	if Kind(200).String() != "invalid" {
		t.Error("out of range kind should be invalid")
	}
}

func FuzzParse(f *testing.F) {
	seeds := []string{
		"",
		"11/5 22:05:51.763  You gain Perception.",
		"11/5 22:05:52.888  Your Fireball hits Kobold Vermin for 70 Fire damage.",
		"11/5 22:06:06.998  Your Fireball crits Kobold Vermin for 141 Fire damage.",
		"11/6 01:54:58.750  Your Auto Shot hits Kobold Vermin for 48.",
		"11/5 22:05:55.797  Kobold Vermin dies.",
		"11/5 22:05:56.686  Kobold Vermin hits you for 3.",
		"11/5 22:19:44.022  You die.\r\n",
		"11/5 22:05:55.797  Murloc Streamrunner suffers 206 Shadow damage from your Corruption.",
		"11/5 22:05:55.797  Kobold Vermin is afflicted by Corruption.",
		"11/5 22:05:55.797  Kobold Vermin is afflicted by Sunder Armor (2).",
		"11/5 22:05:55.797  Corruption fades from Kobold Vermin.",
		"11/5 22:05:55.797  Rhahk'Zor's Shadow Bolt crits you for 80 Shadow damage. (5 resisted) (glancing)",
		"11/5 22:05:55.797  Mr. Smite hits you for 7. (12 blocked)",
		"11/5 22:05:55.797  Kobold Vermin suffers 5 Shadow damage from your Pendant of the Plague: Corruption.",
		"11/5 22:05:55.797  You gain Flurry (3).",
		"11/5 22:05:55.797  Kobold Vermin gains Enrage.",
		"11/5 22:05:55.797  You hit Kobold Vermin for 12.",
		"11/5 22:05:55.797  Felhunter hits Skeletal Raider for 120.",
		"11/5 22:05:55.797  Felhunter's Shadow Bite crits Kobold Vermin for 30 Shadow damage. (5 resisted)",
		"99/99 99:99:99.999  ",
		"1/1 0:00:00.000  . (",
		"11/5 22:05:55.797  Your  hits  for 1.",
		"11/5 22:05:55.797  ) ) ) ( (",
		"11/5 22:05:55.797  x suffers 1 damage from your .",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		ev := Parse(line)
		if ev.Kind > DamageOutPet {
			t.Fatalf("invalid kind %d", ev.Kind)
		}
		if ev.Kind == Unknown {
			if ev.Amount != 0 || ev.Source != "" || ev.Target != "" || ev.Spell != "" || ev.School != "" || ev.Trailer != "" || ev.Crit || ev.Stacks != 0 {
				t.Fatalf("Unknown event carries data: %+v", ev)
			}
			return
		}
		if !ev.HasTS {
			t.Fatalf("typed event without timestamp: %+v", ev)
		}
		if ev.Amount < 0 || ev.Stacks < 0 {
			t.Fatalf("negative number: %+v", ev)
		}
		if ev.Kind.IsDamageOut() || ev.Kind.IsDamageIn() || ev.Kind == DamageOutPet {
			if ev.Amount == 0 && !strings.Contains(line, "0") {
				t.Fatalf("zero amount with no zero in line: %+v", ev)
			}
		}
		// Line endings never change the result.
		if again := Parse(line + "\r\n"); again != ev {
			t.Fatalf("CRLF changed result: %+v vs %+v", again, ev)
		}
	})
}
