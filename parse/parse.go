// Package parse turns one line of a WoW 1.12 (English) combat log into a typed
// Event. It is pure string work: no I/O, no clock, no allocation beyond
// substrings of the input line.
//
// Unrecognised lines are never errors and never dropped: Parse returns an Event
// with Kind Unknown (and the timestamp, when the line has one).
package parse

import "strings"

// ParseRev identifies the behaviour of this parser. Bump it whenever a change
// could alter the Event produced for any line, so stored results can be
// re-derived.
const ParseRev = 2

// Kind classifies a parsed line.
type Kind uint8

const (
	Unknown Kind = iota
	// DamageOutSpell: "Your S hits|crits T for N[ School damage]." (no school means physical).
	DamageOutSpell
	// DamageOutMelee: "You hit|crit T for N."
	DamageOutMelee
	// DamageOutPeriodic: "T suffers N School damage from your S."
	DamageOutPeriodic
	// DamageInMelee: "X hits|crits you for N."
	DamageInMelee
	// DamageInSpell: "X's S hits|crits you for N School damage."
	DamageInSpell
	// AuraGain: "You gain Y." / "X gains Y."
	AuraGain
	// AuraFade: "Y fades from X."
	AuraFade
	// AuraAfflict: "X is afflicted by Y[ (n)]."
	AuraAfflict
	// Died: "X dies."
	Died
	// PlayerDied: "You die."
	PlayerDied
	// DamageOutPet: "X hits|crits T for N[ School damage]." or "X's S hits|crits T ...", where X is
	// neither you nor "Your S". A summon writes these (a Felhunter does); the Imp and totems are
	// written as "Your S" instead. The log names no owner, so in co-op X may be anyone's.
	DamageOutPet
)

var kindNames = [...]string{
	Unknown:           "unknown",
	DamageOutSpell:    "damage_out_spell",
	DamageOutMelee:    "damage_out_melee",
	DamageOutPeriodic: "damage_out_periodic",
	DamageInMelee:     "damage_in_melee",
	DamageInSpell:     "damage_in_spell",
	AuraGain:          "aura_gain",
	AuraFade:          "aura_fade",
	AuraAfflict:       "aura_afflict",
	Died:              "died",
	PlayerDied:        "player_died",
	DamageOutPet:      "damage_out_pet",
}

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "invalid"
}

// IsDamageOut reports whether the event is damage the log attributes to "you". That includes the
// Imp and totems, which it writes as "Your S". DamageOutPet is kept apart.
func (k Kind) IsDamageOut() bool {
	return k == DamageOutSpell || k == DamageOutMelee || k == DamageOutPeriodic
}

// IsDamageIn reports whether the event is damage taken by the player.
func (k Kind) IsDamageIn() bool { return k == DamageInMelee || k == DamageInSpell }

// Event is one parsed line. Strings are substrings of the input line.
//
// Naming convention: an empty Source or Target means "the player" ("you")
// where the shape allows it (AuraGain/AuraFade Target); damage and death shapes
// always carry the other party's name. Spell holds the spell name for damage
// events and the aura name for aura events.
type Event struct {
	Kind    Kind
	TS      Timestamp
	HasTS   bool // the line began with a valid timestamp
	Source  string
	Target  string
	Spell   string
	School  string // empty when the line carries none
	Amount  int
	Crit    bool
	Stacks  int    // "(n)" suffix on AuraAfflict / AuraGain, 0 when absent
	Trailer string // raw trailing "(12 blocked) (glancing)" groups, "" when absent
}

// Parse parses one log line. A trailing CR and/or LF is ignored.
func Parse(line string) Event {
	line = trimEOL(line)
	ts, text, ok := parseTimestamp(line)
	if !ok {
		return Event{}
	}
	ev := Event{TS: ts, HasTS: true}
	body, trailer := splitTrailer(text)
	if !parseBody(&ev, body) {
		return Event{TS: ts, HasTS: true}
	}
	ev.Trailer = trailer
	return ev
}

// TrimEOL removes any trailing CR / LF characters.
func TrimEOL(line string) string { return trimEOL(line) }

func trimEOL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// digits reads between minN and maxN ASCII digits at s[i:].
func digits(s string, i, minN, maxN int) (v, next int, ok bool) {
	j := i
	for j < len(s) && j-i < maxN && s[j] >= '0' && s[j] <= '9' {
		v = v*10 + int(s[j]-'0')
		j++
	}
	if j-i < minN {
		return 0, i, false
	}
	return v, j, true
}

// parseTimestamp reads "M/D HH:MM:SS.mmm" plus the two separating spaces.
func parseTimestamp(s string) (ts Timestamp, rest string, ok bool) {
	var mo, d, h, mi, se, ms, i int
	if mo, i, ok = digits(s, 0, 1, 2); !ok || i >= len(s) || s[i] != '/' {
		return ts, "", false
	}
	if d, i, ok = digits(s, i+1, 1, 2); !ok || i >= len(s) || s[i] != ' ' {
		return ts, "", false
	}
	if h, i, ok = digits(s, i+1, 1, 2); !ok || i >= len(s) || s[i] != ':' {
		return ts, "", false
	}
	if mi, i, ok = digits(s, i+1, 2, 2); !ok || i >= len(s) || s[i] != ':' {
		return ts, "", false
	}
	if se, i, ok = digits(s, i+1, 2, 2); !ok || i >= len(s) || s[i] != '.' {
		return ts, "", false
	}
	if ms, i, ok = digits(s, i+1, 3, 3); !ok {
		return ts, "", false
	}
	if mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || se > 59 {
		return ts, "", false
	}
	if len(s) < i+2 || s[i] != ' ' || s[i+1] != ' ' {
		return ts, "", false
	}
	ts = Timestamp{Month: uint8(mo), Day: uint8(d), Hour: uint8(h), Min: uint8(mi), Sec: uint8(se), Milli: uint16(ms)}
	return ts, s[i+2:], true
}

// splitTrailer peels trailing " (...)" groups ("(12 blocked)", "(glancing)")
// that follow the final period. Only groups that come after the sentence's
// closing "." are peeled, so "Frost Armor (Rank 1)." and "Y (2)." are untouched.
func splitTrailer(s string) (body, trailer string) {
	end := len(s)
	for end > 0 && s[end-1] == ')' {
		i := strings.LastIndexByte(s[:end], '(')
		if i < 2 || s[i-1] != ' ' || s[i-2] != '.' && s[i-2] != ')' {
			break
		}
		end = i - 1
	}
	if end == len(s) {
		return s, ""
	}
	return s[:end], s[end+1:]
}

func parseBody(ev *Event, b string) bool {
	if b == "You die." {
		ev.Kind = PlayerDied
		return true
	}
	return parseYour(ev, b) ||
		parseYouHit(ev, b) ||
		parseYouGain(ev, b) ||
		parseDies(ev, b) ||
		parseHitsYou(ev, b) ||
		parsePetHits(ev, b) ||
		parseSuffers(ev, b) ||
		parseAfflicted(ev, b) ||
		parseFades(ev, b) ||
		parseGains(ev, b)
}

// atoi parses 1..9 ASCII digits.
func atoi(s string) (int, bool) {
	if len(s) == 0 || len(s) > 9 {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}

func isWord(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

// splitDamage anchors on the END of a sentence (without its final period):
//
//	<head> for N
//	<head> for N School damage
//
// Reading from the right means names in head may contain any words, including
// "for", "hits" or digits.
func splitDamage(b string) (head string, amt int, school string, ok bool) {
	hasDmg := false
	if s, found := strings.CutSuffix(b, " damage"); found {
		b, hasDmg = s, true
	}
	i := strings.LastIndexByte(b, ' ')
	if i < 0 {
		return
	}
	if n, isNum := atoi(b[i+1:]); isNum {
		amt, b = n, b[:i]
	} else if hasDmg && isWord(b[i+1:]) {
		school, b = b[i+1:], b[:i]
		j := strings.LastIndexByte(b, ' ')
		if j < 0 {
			return
		}
		n, isNum := atoi(b[j+1:])
		if !isNum {
			return
		}
		amt, b = n, b[:j]
	} else {
		return
	}
	head, found := strings.CutSuffix(b, " for")
	if !found || head == "" {
		return "", 0, "", false
	}
	return head, amt, school, true
}

// verbSplit splits "S hits T" / "S crits T" at the first verb, so the spell
// (which comes first and is short) wins any ambiguity with the target's name.
func verbSplit(head string) (left, right string, crit, ok bool) {
	ih := strings.Index(head, " hits ")
	ic := strings.Index(head, " crits ")
	switch {
	case ih >= 0 && (ic < 0 || ih < ic):
		left, right = head[:ih], head[ih+6:]
	case ic >= 0:
		left, right, crit = head[:ic], head[ic+7:], true
	default:
		return
	}
	return left, right, crit, left != "" && right != ""
}

// Your S hits|crits T for N[ School damage].
func parseYour(ev *Event, b string) bool {
	rest, ok := strings.CutPrefix(b, "Your ")
	if !ok {
		return false
	}
	rest, ok = strings.CutSuffix(rest, ".")
	if !ok {
		return false
	}
	head, amt, school, ok := splitDamage(rest)
	if !ok {
		return false
	}
	spell, target, crit, ok := verbSplit(head)
	if !ok || target == "you" {
		return false
	}
	*ev = Event{Kind: DamageOutSpell, TS: ev.TS, HasTS: true, Spell: spell, Target: target, School: school, Amount: amt, Crit: crit}
	return true
}

// You hit|crit T for N.
func parseYouHit(ev *Event, b string) bool {
	crit := false
	rest, ok := strings.CutPrefix(b, "You hit ")
	if !ok {
		if rest, ok = strings.CutPrefix(b, "You crit "); !ok {
			return false
		}
		crit = true
	}
	rest, ok = strings.CutSuffix(rest, ".")
	if !ok {
		return false
	}
	// splitDamage wants "<head> for N"; the verb prefix is already gone.
	target, amt, school, ok := splitDamage(rest)
	if !ok {
		return false
	}
	*ev = Event{Kind: DamageOutMelee, TS: ev.TS, HasTS: true, Target: target, School: school, Amount: amt, Crit: crit}
	return true
}

// auraName splits an optional "(n)" stack suffix from an aura name and rejects
// shapes that are not aura names (for example "gains 10 Mana from ...").
func auraName(s string) (name string, stacks int, ok bool) {
	if s == "" {
		return
	}
	if i := strings.IndexByte(s, ' '); i > 0 {
		if _, num := atoi(s[:i]); num {
			return "", 0, false
		}
	}
	if strings.HasSuffix(s, ")") {
		if i := strings.LastIndex(s, " ("); i > 0 {
			if n, num := atoi(s[i+2 : len(s)-1]); num {
				return s[:i], n, true
			}
		}
	}
	return s, 0, true
}

// You gain Y.
func parseYouGain(ev *Event, b string) bool {
	rest, ok := strings.CutPrefix(b, "You gain ")
	if !ok {
		return false
	}
	rest, ok = strings.CutSuffix(rest, ".")
	if !ok {
		return false
	}
	name, stacks, ok := auraName(rest)
	if !ok {
		return false
	}
	*ev = Event{Kind: AuraGain, TS: ev.TS, HasTS: true, Spell: name, Stacks: stacks}
	return true
}

// X dies.
func parseDies(ev *Event, b string) bool {
	x, ok := strings.CutSuffix(b, " dies.")
	if !ok || x == "" {
		return false
	}
	*ev = Event{Kind: Died, TS: ev.TS, HasTS: true, Target: x}
	return true
}

// X hits|crits you for N.            (melee)
// X's S hits|crits you for N School damage.   (spell)
func parseHitsYou(ev *Event, b string) bool {
	rest, ok := strings.CutSuffix(b, ".")
	if !ok {
		return false
	}
	head, amt, school, ok := splitDamage(rest)
	if !ok {
		return false
	}
	crit := false
	src, ok := strings.CutSuffix(head, " hits you")
	if !ok {
		if src, ok = strings.CutSuffix(head, " crits you"); !ok {
			return false
		}
		crit = true
	}
	if src == "" || strings.HasPrefix(src, "Your ") {
		return false
	}
	if school == "" {
		*ev = Event{Kind: DamageInMelee, TS: ev.TS, HasTS: true, Source: src, Amount: amt, Crit: crit}
		return true
	}
	i := strings.Index(src, "'s ")
	if i <= 0 || i+3 >= len(src) {
		return false
	}
	*ev = Event{Kind: DamageInSpell, TS: ev.TS, HasTS: true, Source: src[:i], Spell: src[i+3:], School: school, Amount: amt, Crit: crit}
	return true
}

// X hits|crits T for N[ School damage].
// X's S hits|crits T for N School damage.
//
// Runs after the "you" shapes, so what reaches it names neither you nor your spell.
func parsePetHits(ev *Event, b string) bool {
	rest, ok := strings.CutSuffix(b, ".")
	if !ok {
		return false
	}
	head, amt, school, ok := splitDamage(rest)
	if !ok {
		return false
	}
	src, target, crit, ok := verbSplit(head)
	if !ok || target == "you" || src == "You" || strings.HasPrefix(src, "Your ") {
		return false
	}
	spell := ""
	// Only a school proves "'s" is a spell: "Elder Mottled Boar's Mate hits T for 7." is a name.
	if i := strings.Index(src, "'s "); school != "" && i > 0 && i+3 < len(src) {
		src, spell = src[:i], src[i+3:]
	}
	*ev = Event{Kind: DamageOutPet, TS: ev.TS, HasTS: true, Source: src, Target: target, Spell: spell, School: school, Amount: amt, Crit: crit}
	return true
}

// T suffers N[ School] damage from your S.
func parseSuffers(ev *Event, b string) bool {
	rest, ok := strings.CutSuffix(b, ".")
	if !ok {
		return false
	}
	const sep = " suffers "
	for from := 0; ; {
		i := strings.Index(rest[from:], sep)
		if i < 0 {
			return false
		}
		i += from
		from = i + 1
		if i == 0 {
			continue
		}
		tail := rest[i+len(sep):]
		n, j, ok := digits(tail, 0, 1, 9)
		if !ok || j >= len(tail) || tail[j] != ' ' {
			continue
		}
		tail = tail[j+1:]
		school := ""
		if k := strings.IndexByte(tail, ' '); k > 0 && isWord(tail[:k]) && tail[:k] != "damage" {
			school, tail = tail[:k], tail[k+1:]
		}
		spell, ok := strings.CutPrefix(tail, "damage from your ")
		if !ok || spell == "" {
			continue
		}
		*ev = Event{Kind: DamageOutPeriodic, TS: ev.TS, HasTS: true, Target: rest[:i], Spell: spell, School: school, Amount: n}
		return true
	}
}

// X is afflicted by Y[ (n)].
func parseAfflicted(ev *Event, b string) bool {
	rest, ok := strings.CutSuffix(b, ".")
	if !ok {
		return false
	}
	const sep = " is afflicted by "
	i := strings.Index(rest, sep)
	if i <= 0 {
		return false
	}
	name, stacks, ok := auraName(rest[i+len(sep):])
	if !ok {
		return false
	}
	*ev = Event{Kind: AuraAfflict, TS: ev.TS, HasTS: true, Target: rest[:i], Spell: name, Stacks: stacks}
	return true
}

// Y fades from X.   (X == "you" means the player)
func parseFades(ev *Event, b string) bool {
	rest, ok := strings.CutSuffix(b, ".")
	if !ok {
		return false
	}
	const sep = " fades from "
	i := strings.Index(rest, sep)
	if i <= 0 || i+len(sep) >= len(rest) {
		return false
	}
	target := rest[i+len(sep):]
	if target == "you" {
		target = ""
	}
	*ev = Event{Kind: AuraFade, TS: ev.TS, HasTS: true, Target: target, Spell: rest[:i]}
	return true
}

// X gains Y[ (n)].
func parseGains(ev *Event, b string) bool {
	rest, ok := strings.CutSuffix(b, ".")
	if !ok {
		return false
	}
	const sep = " gains "
	i := strings.Index(rest, sep)
	if i <= 0 {
		return false
	}
	name, stacks, ok := auraName(rest[i+len(sep):])
	if !ok {
		return false
	}
	*ev = Event{Kind: AuraGain, TS: ev.TS, HasTS: true, Target: rest[:i], Spell: name, Stacks: stacks}
	return true
}
