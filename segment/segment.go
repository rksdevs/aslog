// Package segment splits a stream of combat log lines into runs.
//
// A Segmenter is a line-by-line state machine. Feeding it a finished file one
// line at a time, in arbitrary batches, or live as lines are tailed gives
// identical runs: nothing depends on how lines are grouped, only on their order.
//
// A run is one attempt from scratch. It starts with the first combat activity
// (damage either way, a kill, or an affliction) and also owns the quiet lines
// immediately before that activity (buffs, for example). It ends at:
//
//   - "You die."                          reason "death"
//   - a detected build reset              reason "reset", Inferred
//   - a long silence with a tiny build    reason "longgap", Inferred
//   - a silence of MaxSilence or more     reason "maxgap", Inferred
//   - Flush (end of input)                reason "eof", Inferred
//   - an external boundary hint (Hint)    reason "hint"
//
// Pauses are normal (level-ups, item drops, AFK), so silence alone never ends
// a run. The rule rests on one fact: inside a run, spells are never lost and
// hit sizes only grow. A new run starts from scratch, so after a pause either
// most of the old "build" (the set of damage sources seen so far) never comes
// back, or the hits collapse.
//
// Build-reset detection. A candidate is the first line after a silence of at
// least Config.Silence while the run has at least Config.MinBuild distinct
// damage sources. The look-ahead covers active combat time only: gaps between
// lines under Silence are summed until Config.Horizon, stopping early at
// "You die.". It needs Config.MinHits damage events. The candidate is a reset
// when at least Config.LostShare of the old sources never appear in the
// look-ahead, or when the median, across sources present on both sides, of
// (look-ahead median hit / earlier median hit) is at most Config.HitRatio.
// Medians use each source's last Config.SizeWindow non-crit hits. A reset
// boundary is the candidate line itself.
//
// Hard limit: a silence of at least Config.MaxSilence always ends the run
// (reason "maxgap", inferred), even if the build returns, and also ends any
// look-ahead in progress.
//
// Fallback for small builds: a silence of at least Config.LongGap with fewer
// than MinBuild sources starts a new run (reason "longgap"). With a bigger
// build, the look-ahead decides, so an AFK pause with the build returning is
// still one run.
//
// Pending candidates. The look-ahead is in the future, so a candidate is held
// until it resolves (Horizon of active time, a death, a hint, the line cap, or
// Flush). While held, the run's aggregate does not include the held lines and
// the run is not yet known to be over; Pending reports how many lines are held.
// When the candidate resolves, the held lines are replayed in their original
// order: into a new run on a reset, or into the same run otherwise (and a held
// line may itself be a later candidate). Every line is counted exactly once and
// in order. A RunEnded for a reset therefore arrives some lines after its
// boundary, never before. Flush resolves a pending candidate with whatever
// look-ahead exists, still requiring MinHits.
package segment

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"time"

	"github.com/rksdevs/aslog/chain"
	"github.com/rksdevs/aslog/parse"
)

// End reasons.
const (
	EndDeath   = "death"
	EndReset   = "reset"
	EndLongGap = "longgap"
	EndMaxGap  = "maxgap"
	EndEOF     = "eof"
	EndHint    = "hint"
)

// FingerprintLines is how many leading lines of a run are hashed into
// Run.Fingerprint.
const FingerprintLines = 20

// MeleeSpell is the key used in Run.DamageBySpell for white (melee) damage.
const MeleeSpell = "Melee"

// Config tunes segmentation. Zero fields take the defaults shown.
type Config struct {
	// Silence: a gap between lines of at least this long makes the next line a
	// reset candidate; gaps under it count as active time in the look-ahead.
	// Default 3s.
	Silence time.Duration
	// MinBuild: distinct damage sources a run needs before candidates are
	// considered, and the build size under which LongGap applies. Default 3.
	MinBuild int
	// Horizon: active combat time the look-ahead covers. Default 45s.
	Horizon time.Duration
	// MinHits: damage events the look-ahead needs to decide. Default 10.
	MinHits int
	// LostShare: reset when at least this share of the old sources never
	// reappear. Default 0.5.
	LostShare float64
	// HitRatio: reset when the median hit-size ratio is at or below this.
	// Default 0.5.
	HitRatio float64
	// SizeWindow: non-crit hit sizes kept per source. Default 40.
	SizeWindow int
	// LongGap: silence that, with a build under MinBuild, starts a new run.
	// Default 10m.
	LongGap time.Duration
	// MaxSilence: a silence at least this long always ends the run, whatever
	// the build, with reason "maxgap". Default 30m.
	MaxSilence time.Duration
	// MaxLookahead caps the lines held for one candidate, bounding memory when
	// the active-time horizon is never reached. The candidate then resolves with
	// what it has. Default 50000.
	MaxLookahead int
	// NoteGap: pauses of at least this length inside a run are recorded in
	// Run.Gaps. Default 30s; negative disables.
	NoteGap time.Duration
	// PostDeathQuiet: after "You die." any line within this long of the death
	// is a straggler and cannot start a run. Default 3s; negative disables.
	PostDeathQuiet time.Duration
}

// DefaultConfig returns the defaults.
func DefaultConfig() Config {
	return Config{
		Silence: 3 * time.Second, MinBuild: 3, Horizon: 45 * time.Second, MinHits: 10,
		LostShare: 0.5, HitRatio: 0.5, SizeWindow: 40, LongGap: 10 * time.Minute,
		MaxSilence: 30 * time.Minute, MaxLookahead: 50000, NoteGap: 30 * time.Second, PostDeathQuiet: 3 * time.Second,
	}
}

// AuraStat counts aura lines for one aura name.
type AuraStat struct {
	Gains    int // "You gain Y." / "X gains Y."
	Afflicts int // "X is afflicted by Y."
	Fades    int // "Y fades from X."
}

// Gap is a pause inside a run that did not end it.
type Gap struct {
	AfterLine int             // lines of the run before the gap
	From, To  parse.Timestamp // last line before, first line after
	MS        int64
}

// ShieldAura is the shield the game puts on you while you choose a card or loot. Hits during it are
// written to the log but cost no health: on a measured run HP never fell across 514 logged damage.
const ShieldAura = "Divine Shield"

// Run is the aggregate of one run. Hits, Crits and DamageOut count events attributed to "you", which
// include the Imp and totems (the log writes them as "Your S"). Other summons are Pet figures. In
// co-op a Pet figure may be another player's: the log names no owner.
type Run struct {
	Index               int // 1-based, in order of start
	First, Last         parse.Timestamp
	FirstMono, LastMono int64 // parse.Timestamp.Mono along the stream
	Lines               int   // every non-empty line, unknown ones included
	FirstLine, LastLine int   // 1-based positions among the non-empty lines pushed: exactly this run's lines, so it can be cut from its source
	ActiveMs            int64 // sum of gaps between consecutive timestamped lines under Config.Silence; pauses excluded. Data only: short runs are never dropped here
	Hits                int   // every outgoing damage event: direct (crits included) and periodic
	Crits               int   // direct crits (periodic ticks never crit in this log format)
	Ticks               int   // of Hits, the periodic ones ("suffers ... from your")
	DamageOut           int64 // gross damage: direct + periodic
	DamageTaken         int64 // whole numbers as logged: the game keeps fractions, about 0.5 more per hit. Shielded hits left out
	ShieldedHits        int   // hits on you during ShieldAura
	ShieldedTaken       int64 // what those hits would have dealt
	PetHits             int
	PetDamage           int64
	DamageByPet         map[string]int64 // by the name the log gives
	DamageBySpell       map[string]int64 // direct + periodic, white damage under MeleeSpell
	Kills               map[string]int   // "X dies." by name
	TakenByAttacker     map[string]int64
	Auras               map[string]AuraStat
	Unknown             int
	Gaps                []Gap
	Fingerprint         string // SHA-256 hex over the first FingerprintLines canonical lines
	EndReason           string
	Inferred            bool // the end was inferred (reset, longgap, eof), not marked by the log
	Ended               bool

	firstLines []string
	shielded   bool
}

// KillCount is the total number of "X dies." lines.
func (r *Run) KillCount() int {
	n := 0
	for _, c := range r.Kills {
		n += c
	}
	return n
}

// ResultKind says what happened.
type ResultKind uint8

const (
	RunStarted ResultKind = iota + 1
	RunEnded
)

// Result reports a run starting or ending. Run points at the live aggregate:
// for RunStarted it keeps growing as lines arrive, for RunEnded it is final.
// Do not read it from another goroutine while still pushing lines.
type Result struct {
	Kind ResultKind
	Run  *Run
}

// item is one line on its way through the state machine. prev is the stream
// clock of the previous timestamped line, fixed at intake so that replaying a
// held line sees the same silence it did the first time.
type item struct {
	raw      string
	ev       parse.Event
	no       int // position among the non-empty lines pushed
	mono     int64
	prev     int64
	havePrev bool
	decided  bool // already judged as a candidate; do not judge again
}

const maxPrelude = 1000

// sizes keeps the last N non-crit hit sizes of one damage source.
type sizes struct {
	v    []int
	head int
}

func (z *sizes) add(d, window int) {
	if len(z.v) < window {
		z.v = append(z.v, d)
	} else {
		z.v[z.head] = d
		z.head = (z.head + 1) % window
	}
}

// sizeMedian is the upper median, sorted[len/2]; 0 when empty.
func sizeMedian(v []int) int {
	if len(v) == 0 {
		return 0
	}
	c := append([]int(nil), v...)
	sort.Ints(c)
	return c[len(c)/2]
}

// lookahead gathers what the held lines show.
type lookahead struct {
	buf      []item
	activeMS int64
	last     int64
	haveLast bool
	hits     int
	after    map[string][]int // source -> non-crit sizes; key present for any hit
}

// Segmenter is the streaming state machine. The zero value is not usable; use New.
type Segmenter struct {
	silenceMS, horizonMS, longMS, maxMS, noteMS, quietMS int64
	minBuild, minHits, window, maxLook                   int
	lostShare, hitRatio                                  float64

	run     *Run
	runs    int
	prelude []item
	build   map[string]*sizes
	pending *lookahead

	work []item
	head int
	out  []Result

	haveIntake bool
	lastIntake int64
	intake     int

	// per-run
	rHave   bool
	rPrev   int64
	rPrevTS parse.Timestamp

	quiet      bool
	quietUntil int64
	stragglers int

	hints []parse.Timestamp
}

// New returns a Segmenter. Zero Config fields take their defaults.
func New(cfg Config) *Segmenter {
	d := DefaultConfig()
	if cfg.Silence <= 0 {
		cfg.Silence = d.Silence
	}
	if cfg.MinBuild <= 0 {
		cfg.MinBuild = d.MinBuild
	}
	if cfg.Horizon <= 0 {
		cfg.Horizon = d.Horizon
	}
	if cfg.MinHits <= 0 {
		cfg.MinHits = d.MinHits
	}
	if cfg.LostShare <= 0 {
		cfg.LostShare = d.LostShare
	}
	if cfg.HitRatio <= 0 {
		cfg.HitRatio = d.HitRatio
	}
	if cfg.SizeWindow <= 0 {
		cfg.SizeWindow = d.SizeWindow
	}
	if cfg.LongGap <= 0 {
		cfg.LongGap = d.LongGap
	}
	if cfg.MaxSilence <= 0 {
		cfg.MaxSilence = d.MaxSilence
	}
	if cfg.MaxLookahead <= 0 {
		cfg.MaxLookahead = d.MaxLookahead
	}
	if cfg.NoteGap == 0 {
		cfg.NoteGap = d.NoteGap
	}
	if cfg.PostDeathQuiet == 0 {
		cfg.PostDeathQuiet = d.PostDeathQuiet
	}
	s := &Segmenter{
		silenceMS: cfg.Silence.Milliseconds(), horizonMS: cfg.Horizon.Milliseconds(), longMS: cfg.LongGap.Milliseconds(), maxMS: cfg.MaxSilence.Milliseconds(),
		minBuild: cfg.MinBuild, minHits: cfg.MinHits, window: cfg.SizeWindow, maxLook: cfg.MaxLookahead,
		lostShare: cfg.LostShare, hitRatio: cfg.HitRatio,
	}
	if cfg.NoteGap > 0 {
		s.noteMS = cfg.NoteGap.Milliseconds()
	}
	if cfg.PostDeathQuiet > 0 {
		s.quietMS = cfg.PostDeathQuiet.Milliseconds()
	}
	return s
}

// Stragglers is how many post-death lines were ignored (see Config.PostDeathQuiet).
func (s *Segmenter) Stragglers() int { return s.stragglers }

// Pending is how many lines are held for an unresolved reset candidate. They
// are not yet part of any run's aggregate; they are never lost or counted twice.
func (s *Segmenter) Pending() int {
	if s.pending == nil {
		return 0
	}
	return len(s.pending.buf)
}

// Hint registers an external run boundary (for example from addon data) at a
// log timestamp. The first line stamped at or after ts starts fresh: the active
// run, if any, ends with reason "hint" (not inferred) at the line before it.
// Hints must be added in chronological order and before the lines they refer to
// are pushed; a hint at or before the latest line already processed is stale
// and ignored. A hint also resolves a pending candidate early.
func (s *Segmenter) Hint(ts parse.Timestamp) { s.hints = append(s.hints, ts) }

// Push feeds one raw line (a trailing CR/LF is ignored; empty lines are
// ignored, they are line-splitting artefacts, not log content).
func (s *Segmenter) Push(line string) []Result {
	line = parse.TrimEOL(line)
	if line == "" {
		return nil
	}
	return s.PushEvent(line, parse.Parse(line))
}

// PushLines feeds a batch of lines, equivalent to calling Push for each.
func (s *Segmenter) PushLines(lines []string) []Result {
	var out []Result
	for _, l := range lines {
		out = append(out, s.Push(l)...)
	}
	return out
}

// PushEvent is Push for a caller that already parsed the line. raw must be the
// line without its line ending and ev must be parse.Parse(raw).
func (s *Segmenter) PushEvent(raw string, ev parse.Event) []Result {
	s.intake++
	it := item{raw: raw, ev: ev, no: s.intake}
	if ev.HasTS {
		it.mono = ev.TS.Mono(s.lastIntake, s.haveIntake)
		it.prev, it.havePrev = s.lastIntake, s.haveIntake
		s.lastIntake, s.haveIntake = it.mono, true
	}
	if s.head == len(s.work) {
		s.work, s.head = s.work[:0], 0
	}
	s.work = append(s.work, it)
	s.drain()
	return s.take()
}

// Flush resolves any pending candidate (with whatever look-ahead exists) and
// ends the active run, if any, with reason "eof" (inferred). The Segmenter may
// be reused afterwards; buffered pre-combat lines are discarded.
func (s *Segmenter) Flush() []Result {
	for {
		s.drain()
		if s.pending == nil {
			break
		}
		s.resolve(nil)
	}
	if s.run != nil {
		s.closeRun(EndEOF, true)
	}
	s.prelude = s.prelude[:0]
	return s.take()
}

func (s *Segmenter) take() []Result {
	out := s.out
	s.out = nil
	return out
}

func (s *Segmenter) drain() {
	for s.head < len(s.work) {
		it := s.work[s.head]
		s.head++
		s.step(it)
	}
}

func signed(d int64) int64 {
	const year = int64(366) * 86_400_000
	d %= year
	if d < 0 {
		d += year
	}
	if d > year/2 {
		d -= year
	}
	return d
}

// hintReached reports whether a hint lies in (previous line, this line]. With
// apply the stale and reached hints are consumed.
func (s *Segmenter) hintReached(it item, apply bool) bool {
	ref := it.mono
	if it.havePrev {
		ref = it.prev
	}
	split, i := false, 0
	for i < len(s.hints) {
		d := signed(s.hints[i].Raw() - ref)
		if d <= 0 {
			i++ // stale
			continue
		}
		if d > it.mono-ref {
			break
		}
		split = true
		i++
	}
	if apply {
		s.hints = s.hints[i:]
	}
	return split
}

func isCombat(k parse.Kind) bool {
	switch k {
	case parse.DamageOutSpell, parse.DamageOutMelee, parse.DamageOutPeriodic,
		parse.DamageInMelee, parse.DamageInSpell, parse.Died, parse.AuraAfflict, parse.DamageOutPet:
		return true
	}
	return false
}

// sourceOf names the damage source of an outgoing damage event.
func sourceOf(ev parse.Event) string {
	if ev.Kind == parse.DamageOutMelee {
		return MeleeSpell
	}
	return ev.Spell
}

func (s *Segmenter) step(it item) {
	if s.pending != nil {
		s.stepPending(it)
		return
	}
	hasHints := it.ev.HasTS && len(s.hints) > 0
	if hasHints && s.hintReached(it, true) {
		if s.run != nil {
			s.closeRun(EndHint, false)
		}
		s.prelude = s.prelude[:0]
		s.quiet = false
	}

	if s.run != nil && it.havePrev && it.mono-it.prev >= s.maxMS {
		s.closeRun(EndMaxGap, true)
	}
	if s.run != nil && !it.decided && it.havePrev {
		sil := it.mono - it.prev
		switch {
		case sil >= s.longMS && len(s.build) < s.minBuild:
			s.closeRun(EndLongGap, true)
		case sil >= s.silenceMS && len(s.build) >= s.minBuild && it.ev.Kind != parse.PlayerDied:
			s.pending = &lookahead{after: map[string][]int{}}
			s.stepPending(it)
			return
		}
	}

	if s.run != nil {
		s.addToRun(it)
		if it.ev.Kind == parse.PlayerDied {
			s.closeRun(EndDeath, false)
			s.quiet = true
			s.quietUntil = it.mono + s.quietMS
		}
		return
	}
	switch {
	case s.quiet && it.ev.HasTS && it.mono < s.quietUntil:
		s.stragglers++
	case isCombat(it.ev.Kind):
		s.quiet = false
		s.startRun()
		for _, p := range s.prelude {
			s.addToRun(p)
		}
		s.prelude = s.prelude[:0]
		s.addToRun(it)
	default:
		if s.quiet && it.ev.HasTS {
			s.quiet = false // past the quiet window
		}
		if len(s.prelude) >= maxPrelude {
			s.prelude = s.prelude[:copy(s.prelude, s.prelude[1:])]
		}
		s.prelude = append(s.prelude, it)
	}
}

// stepPending adds a line to the held look-ahead, or resolves it first when the
// line ends the look-ahead (death, hint).
func (s *Segmenter) stepPending(it item) {
	p := s.pending
	if len(p.buf) > 0 {
		if it.ev.Kind == parse.PlayerDied || (it.havePrev && it.mono-it.prev >= s.maxMS) || (it.ev.HasTS && len(s.hints) > 0 && s.hintReached(it, false)) {
			s.resolve(&it)
			return
		}
	}
	p.buf = append(p.buf, it)
	if it.ev.HasTS {
		if p.haveLast {
			if d := it.mono - p.last; d < s.silenceMS {
				p.activeMS += d
			}
		}
		p.last, p.haveLast = it.mono, true
	}
	if it.ev.Kind.IsDamageOut() {
		p.hits++
		src := sourceOf(it.ev)
		if !it.ev.Crit {
			p.after[src] = append(p.after[src], it.ev.Amount)
		} else if _, ok := p.after[src]; !ok {
			p.after[src] = nil
		}
	}
	if p.activeMS >= s.horizonMS || len(p.buf) >= s.maxLook {
		s.resolve(nil)
	}
}

// resolve decides the pending candidate and puts the held lines (then trigger,
// if the look-ahead ended on a line that is not part of it) back at the front
// of the work queue.
func (s *Segmenter) resolve(trigger *item) {
	p := s.pending
	s.pending = nil
	if s.decide(p) {
		s.closeRun(EndReset, true)
	}
	p.buf[0].decided = true
	rest := s.work[s.head:]
	nw := make([]item, 0, len(p.buf)+1+len(rest))
	nw = append(nw, p.buf...)
	if trigger != nil {
		nw = append(nw, *trigger)
	}
	nw = append(nw, rest...)
	s.work, s.head = nw, 0
}

// decide applies the reset rule to the held look-ahead.
func (s *Segmenter) decide(p *lookahead) bool {
	if p.hits < s.minHits {
		return false
	}
	lost := 0
	for src := range s.build {
		if _, ok := p.after[src]; !ok {
			lost++
		}
	}
	if float64(lost)/float64(len(s.build)) >= s.lostShare {
		return true
	}
	var ratios []float64
	for src, a := range p.after {
		b, ok := s.build[src]
		if !ok || len(b.v) == 0 || len(a) == 0 {
			continue
		}
		ratios = append(ratios, float64(sizeMedian(a))/math.Max(1, float64(sizeMedian(b.v))))
	}
	if len(ratios) == 0 {
		return false
	}
	sort.Float64s(ratios)
	return ratios[len(ratios)/2] <= s.hitRatio
}

func (s *Segmenter) startRun() {
	s.runs++
	s.run = &Run{
		Index:           s.runs,
		DamageBySpell:   map[string]int64{},
		Kills:           map[string]int{},
		TakenByAttacker: map[string]int64{},
		DamageByPet:     map[string]int64{},
		Auras:           map[string]AuraStat{},
	}
	s.build = map[string]*sizes{}
	s.rHave = false
	s.out = append(s.out, Result{Kind: RunStarted, Run: s.run})
}

func (s *Segmenter) addToRun(it item) {
	r, ev, mono := s.run, it.ev, it.mono
	r.Lines++
	if r.FirstLine == 0 {
		r.FirstLine = it.no
	}
	r.LastLine = it.no
	if len(r.firstLines) < FingerprintLines {
		r.firstLines = append(r.firstLines, it.raw)
		if len(r.firstLines) == FingerprintLines {
			r.Fingerprint = fingerprint(r.firstLines)
		}
	}
	if ev.HasTS {
		if s.rHave {
			if d := mono - s.rPrev; d < s.silenceMS {
				r.ActiveMs += d
			}
			if s.noteMS > 0 && mono-s.rPrev >= s.noteMS {
				r.Gaps = append(r.Gaps, Gap{AfterLine: r.Lines - 1, From: s.rPrevTS, To: ev.TS, MS: mono - s.rPrev})
			}
		} else {
			r.First, r.FirstMono = ev.TS, mono
		}
		r.Last, r.LastMono = ev.TS, mono
		s.rHave, s.rPrev, s.rPrevTS = true, mono, ev.TS
	}

	switch ev.Kind {
	case parse.Unknown:
		r.Unknown++
	case parse.DamageOutSpell, parse.DamageOutMelee:
		r.Hits++
		if ev.Crit {
			r.Crits++
		}
		r.addOut(ev)
		s.noteBuild(ev)
	case parse.DamageOutPeriodic:
		r.Hits++
		r.Ticks++
		r.addOut(ev)
		s.noteBuild(ev)
	case parse.DamageOutPet:
		// Kept out of the build: a summon's hits say nothing about whether your spells were reset.
		r.PetHits++
		r.PetDamage += int64(ev.Amount)
		r.DamageByPet[ev.Source] += int64(ev.Amount)
	case parse.DamageInMelee, parse.DamageInSpell:
		if r.shielded {
			r.ShieldedHits++
			r.ShieldedTaken += int64(ev.Amount)
			break
		}
		r.DamageTaken += int64(ev.Amount)
		r.TakenByAttacker[ev.Source] += int64(ev.Amount)
	case parse.Died:
		r.Kills[ev.Target]++
	case parse.AuraGain:
		if ev.Spell == ShieldAura && ev.Target == "" {
			r.shielded = true
		}
		a := r.Auras[ev.Spell]
		a.Gains++
		r.Auras[ev.Spell] = a
	case parse.AuraAfflict:
		a := r.Auras[ev.Spell]
		a.Afflicts++
		r.Auras[ev.Spell] = a
	case parse.AuraFade:
		if ev.Spell == ShieldAura && ev.Target == "" {
			r.shielded = false
		}
		a := r.Auras[ev.Spell]
		a.Fades++
		r.Auras[ev.Spell] = a
	}
}

// noteBuild records a damage event in the run's build. Any hit makes the source
// part of the build; only non-crit sizes feed the medians.
func (s *Segmenter) noteBuild(ev parse.Event) {
	src := sourceOf(ev)
	b := s.build[src]
	if b == nil {
		b = &sizes{}
		s.build[src] = b
	}
	if !ev.Crit {
		b.add(ev.Amount, s.window)
	}
}

func (r *Run) addOut(ev parse.Event) {
	r.DamageOut += int64(ev.Amount)
	r.DamageBySpell[sourceOf(ev)] += int64(ev.Amount)
}

func (s *Segmenter) closeRun(reason string, inferred bool) {
	r := s.run
	if r.Fingerprint == "" {
		r.Fingerprint = fingerprint(r.firstLines)
	}
	r.firstLines = nil
	r.EndReason, r.Inferred, r.Ended = reason, inferred, true
	s.run = nil
	s.build = nil
	s.out = append(s.out, Result{Kind: RunEnded, Run: r})
}

func fingerprint(lines []string) string {
	sum := sha256.Sum256(chain.Canonical(lines))
	return hex.EncodeToString(sum[:])
}
