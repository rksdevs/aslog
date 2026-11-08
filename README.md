# aslog

Public, stdlib-only Go library behind the Azeroth Survivors (AS) log pipeline. The
native uploader and the server both import it, so a line is parsed, split into
runs and hash-chained the same way on both sides.

Module: `github.com/rksdevs/aslog` (Go 1.23, no cgo, no dependencies). MIT licensed.

## Packages

| Package | What it does |
|---|---|
| `parse` | One log line to a typed `Event`. Pure string work: it does not import `os` or any network package. Unknown lines come back as `Kind` `Unknown`, never an error and never dropped. |
| `segment` | Streaming run splitter. `Push` a line (or `PushLines` a batch), get `RunStarted` / `RunEnded` results; `Flush` closes the last run. A live tail and a finished file give identical runs. Each `Run` carries line count, `ActiveMs`, hits, crits, damage by spell, pet and summon damage by name, kills by name, damage taken by attacker, hits stopped by the card-screen shield, auras, unknown-line count, pauses inside the run and a first-20-lines SHA-256 fingerprint. |
| `review` | The pre-upload step: finds the runs in a log (text, gzip or zip) and cuts whole chosen runs back out, line for line. Browser and uploader both use it. |
| `chain` | The upload hash chain: `h0 = SHA256(serverNonce \|\| runKey)`, `h_k = SHA256(h_{k-1} \|\| ackNonce_{k-1} \|\| canonicalLines_k)`. `Advance`, plus `Verify`, which replays batches and reports the first bad index. |
| `wire` | Types and JSON tags for live protocol v1 (`Batch`, `Ack`, session open request and response, `Protocol = 1`). Unknown JSON fields are ignored. |

## Compatibility

The parser targets English 1.12-style combat log lines, `M/D HH:MM:SS.mmm  text`
(two spaces after the time), LF or CRLF. The log has no year, so none is guessed.
Supported shapes: your spell hits and crits (with or without a school), melee hits
and crits, periodic damage from your spells, hits by anything else on a target that
is not you (`Felhunter hits X for N.`), damage taken (melee and `X's Spell`),
afflictions, gains, fades, `X dies.` and `You die.`, with `(12 blocked)`,
`(glancing)`-style trailers kept as raw text.

How the log names pets varies. The Imp and totems are written as your own spells
(`Your Fireball`, `Your Magma Totem`), so they count as yours and the Imp cannot be
told apart from your own Fireball. A Felhunter is written by name, which goes to
`Run.PetDamage`, not `DamageOut`. Those lines name no owner, so in co-op they may be
another player's. Hunter pets and co-op are untested.

`Run.DamageTaken` leaves out hits during Divine Shield, which the game applies while
you choose a card or loot: they are logged but cost no health, and `ShieldedTaken`
keeps them. Logged amounts are whole numbers. The game keeps fractions, about 0.5
more per hit, and boss spells are not logged at all, so this figure runs below the
game's own.

### How runs are split

Pauses are normal (level-ups, item drops, AFK), so silence alone never ends a
run. Inside a run spells are never lost and hit sizes only grow; a new run starts
from scratch. Boundaries are:

- `You die.` (reason `death`, marked by the log).
- A detected build reset (`reset`, inferred). After a silence of at least 3 s, with
  at least 3 distinct damage sources in the run, the next 45 s of active combat
  (gaps under 3 s; stops at `You die.`; needs 10 damage events) are compared with
  the run so far: a reset when at least 50% of the earlier sources never reappear,
  or the median hit-size ratio (per source, last 40 non-crit hits) is at most 0.5.
  The boundary is the first line after the silence.
- A silence of at least 10 min when the build has under 3 sources (`longgap`,
  inferred). With a bigger build that returns, a long pause is still one run.
- A silence of at least 30 min (`maxgap`, inferred): a hard limit that applies whatever the
  build, even if it returns, and also ends any look-ahead in progress. Under 30 min a returning
  build is still one run.
- End of input (`eof`, inferred) and external hints (`Segmenter.Hint`, `hint`).

All thresholds live in `segment.Config`; zero values take the defaults above.

Look-ahead means a reset is confirmed after the fact. The candidate line and
everything after it are held (`Segmenter.Pending()` says how many, bounded by
`Config.MaxLookahead`), then replayed in order into the new run or the same run;
no line is lost or counted twice, and results do not depend on how lines are
batched. `Flush` resolves a pending candidate with whatever look-ahead exists,
still requiring 10 damage events.

Known limit: a run that starts with the same sources at similar hit sizes as the
one before it, after a pause under 30 min, cannot be told apart from a continuation
using the log alone. `Run.ActiveMs` (sum of gaps under 3 s, so pauses excluded)
is data only; the parser never drops short runs, that rule belongs to the server.

## ParseRev

`parse.ParseRev` (currently `2`) identifies the parser's behaviour. Any change
that could alter the `Event` produced for some line bumps it, so stored results
can be recognised as stale and re-derived. Uploader and server should compare it.

## Trying it on a log

```
go run ./cmd/aslog WoWCombatLog.txt       # runs found in a log (.txt or .gz)
go run ./cmd/aslog -v WoWCombatLog.txt    # plus top spells and what hit you
go run ./cmd/aslog -json WoWCombatLog.txt
```

Runs under a minute of active time are marked `short`, matching the server rule
(`-min` changes it). The library packages stay free of `os`; only this command
reads files.

## Browser build

```
GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o aslog.wasm ./cmd/aslog-wasm
```

The website serves this file with `wasm_exec.js` from the same Go version.

## Development

```
go build ./... && go vet ./... && go test ./...
go test ./parse -run '^$' -fuzz FuzzParse -fuzztime 30s
```

The real-log acceptance test is skipped unless a log is supplied:

```
ASLOG_REAL_LOG=/path/to/WoWCombatLog.txt go test -run TestRealLog -v .
```

Small fixtures in `testdata/` are cut from a real log (mob and spell names only).
