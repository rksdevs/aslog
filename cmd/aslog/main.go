// Command aslog prints the runs found in an Azeroth Survivors combat log, so the
// parser can be tried without the rest of the pipeline.
//
//	go run ./cmd/aslog [-v] [-json] [-min 60s] <WoWCombatLog.txt or .gz>
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rksdevs/aslog/parse"
	"github.com/rksdevs/aslog/segment"
)

const maxLine = 1 << 20

type report struct {
	Lines   int
	Unknown int
	Runs    []*segment.Run
}

func main() {
	verbose := flag.Bool("v", false, "also show top spells and what dealt the most damage to you")
	asJSON := flag.Bool("json", false, "print the runs as JSON")
	min := flag.Duration("min", time.Minute, "mark runs with less active time than this as short")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: aslog [-v] [-json] [-min 60s] <WoWCombatLog.txt or .gz>")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	if err := run(flag.Arg(0), *verbose, *asJSON, *min); err != nil {
		fmt.Fprintln(os.Stderr, "aslog:", err)
		os.Exit(1)
	}
}

func run(path string, verbose, asJSON bool, min time.Duration) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var src io.Reader = f
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		src = gz
	}

	rep, err := read(src)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	printReport(os.Stdout, rep, verbose, min)
	return nil
}

func read(src io.Reader) (*report, error) {
	rep := &report{}
	seg := segment.New(segment.Config{})
	collect := func(results []segment.Result) {
		for _, r := range results {
			if r.Kind == segment.RunEnded {
				rep.Runs = append(rep.Runs, r.Run)
			}
		}
	}

	sc := bufio.NewScanner(src)
	sc.Buffer(make([]byte, 64*1024), maxLine)
	for sc.Scan() {
		line := parse.TrimEOL(sc.Text())
		if line == "" {
			continue
		}
		ev := parse.Parse(line)
		rep.Lines++
		if ev.Kind == parse.Unknown {
			rep.Unknown++
		}
		collect(seg.PushEvent(line, ev))
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	collect(seg.Flush())
	return rep, nil
}

func printReport(w io.Writer, rep *report, verbose bool, min time.Duration) {
	fmt.Fprintf(w, "%d lines, %d unrecognised, %d runs\n\n", rep.Lines, rep.Unknown, len(rep.Runs))
	fmt.Fprintf(w, "%-3s %-18s %-18s %7s %7s %6s %10s %8s %8s  %s\n",
		"#", "start", "end", "lines", "hits", "kills", "damage", "taken", "active", "ended by")
	for _, r := range rep.Runs {
		end := r.EndReason
		if r.Inferred {
			end += " (inferred)"
		}
		active := time.Duration(r.ActiveMs) * time.Millisecond
		if active < min {
			end += ", short"
		}
		fmt.Fprintf(w, "%-3d %-18s %-18s %7d %7d %6d %10d %8d %8s  %s\n",
			r.Index, stamp(r.First), stamp(r.Last), r.Lines, r.Hits, r.KillCount(), r.DamageOut, r.DamageTaken, active.Round(time.Second), end)
		if verbose {
			fmt.Fprintf(w, "    spells: %s\n", top(r.DamageBySpell, 5))
			if r.PetHits > 0 {
				fmt.Fprintf(w, "    pets:   %s (%d hits)\n", top(r.DamageByPet, 5), r.PetHits)
			}
			fmt.Fprintf(w, "    taken:  %s\n", top(r.TakenByAttacker, 5))
			if r.ShieldedHits > 0 {
				fmt.Fprintf(w, "    shield: %d hits, %d damage stopped\n", r.ShieldedHits, r.ShieldedTaken)
			}
		}
	}
}

func stamp(ts parse.Timestamp) string {
	return fmt.Sprintf("%d/%d %02d:%02d:%02d.%03d", ts.Month, ts.Day, ts.Hour, ts.Min, ts.Sec, ts.Milli)
}

func top(totals map[string]int64, n int) string {
	names := make([]string, 0, len(totals))
	for name := range totals {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if totals[names[i]] != totals[names[j]] {
			return totals[names[i]] > totals[names[j]]
		}
		return names[i] < names[j]
	})
	if len(names) > n {
		names = names[:n]
	}
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = fmt.Sprintf("%s %d", name, totals[name])
	}
	return strings.Join(parts, ", ")
}
