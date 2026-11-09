// Package review is the pre-upload step: it finds the runs in a log file so a player can choose
// which to send, then cuts exactly those runs back out. Selection is by whole run, never by line, so
// what is sent is still the log the game wrote. Browser (WebAssembly) and uploader both use it, so
// they show the same runs the server will find.
package review

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/rksdevs/aslog/parse"
	"github.com/rksdevs/aslog/segment"
)

// ErrNoLog means the file holds nothing that reads as an AS combat log.
var ErrNoLog = errors.New("no combat log found in this file")

// Run is one run as a player sees it before uploading.
type Run struct {
	Index     int     `json:"index"`
	Start     string  `json:"start"`
	End       string  `json:"end"`
	ActiveMs  int64   `json:"activeMs"`
	Lines     int     `json:"lines"`
	Hits      int     `json:"hits"`
	Kills     int     `json:"kills"`
	Damage    int64   `json:"damage"`
	PetDamage int64   `json:"petDamage"`
	Taken     int64   `json:"taken"`
	DPS       float64 `json:"dps"`
	TopSpells []Spell `json:"topSpells"`
	EndReason string  `json:"endReason"`
	Inferred  bool    `json:"inferred"`
	// Fingerprint is segment.Run.Fingerprint, which the server keeps per stored run, so a player can
	// see which runs they have already uploaded.
	Fingerprint string `json:"fingerprint"`
}

// Spell is a top damage source, which is how a player recognises a run: the log names no hero.
type Spell struct {
	Name   string `json:"name"`
	Damage int64  `json:"damage"`
}

// Scan is a parsed log file. It keeps the text, so chosen runs can be cut out without reading the
// file again.
type Scan struct {
	Runs    []Run `json:"runs"`
	Lines   int   `json:"lines"`
	Unknown int   `json:"unknown"`

	text  []byte
	spans [][2]int // [start, end) of each non-empty line
	found []*segment.Run
}

// Open accepts the log as text, gzip, or a zip holding it.
func Open(data []byte) (*Scan, error) {
	text, err := logText(data)
	if err != nil {
		return nil, err
	}
	return scan(text)
}

func logText(data []byte) ([]byte, error) {
	switch {
	case len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b:
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, ErrNoLog
		}
		defer gz.Close()
		return io.ReadAll(gz)
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		return zippedLog(data)
	}
	return data, nil
}

// zippedLog prefers a file named like the game's log, else the largest .txt.
func zippedLog(data []byte) ([]byte, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, ErrNoLog
	}
	var pick *zip.File
	for _, f := range archive.File {
		name := strings.ToLower(f.Name[strings.LastIndexByte(f.Name, '/')+1:])
		if !strings.HasSuffix(name, ".txt") {
			continue
		}
		isLog := strings.HasPrefix(name, "wowcombatlog")
		pickIsLog := pick != nil && strings.HasPrefix(strings.ToLower(pick.Name[strings.LastIndexByte(pick.Name, '/')+1:]), "wowcombatlog")
		if pick == nil || (isLog && !pickIsLog) || (isLog == pickIsLog && f.UncompressedSize64 > pick.UncompressedSize64) {
			pick = f
		}
	}
	if pick == nil {
		return nil, ErrNoLog
	}
	r, err := pick.Open()
	if err != nil {
		return nil, ErrNoLog
	}
	defer r.Close()
	return io.ReadAll(r)
}

func scan(text []byte) (*Scan, error) {
	s := &Scan{text: text}
	seg := segment.New(segment.Config{})
	collect := func(results []segment.Result) {
		for _, r := range results {
			if r.Kind == segment.RunEnded {
				s.found = append(s.found, r.Run)
			}
		}
	}

	for start := 0; start < len(text); {
		end := bytes.IndexByte(text[start:], '\n')
		if end < 0 {
			end = len(text)
		} else {
			end += start
		}
		line := parse.TrimEOL(string(text[start:end]))
		if line != "" {
			s.spans = append(s.spans, [2]int{start, start + len(line)})
			ev := parse.Parse(line)
			if ev.Kind == parse.Unknown {
				s.Unknown++
			}
			collect(seg.PushEvent(line, ev))
		}
		start = end + 1
	}
	collect(seg.Flush())

	s.Lines = len(s.spans)
	if s.Lines == 0 || s.Unknown == s.Lines || len(s.found) == 0 {
		return nil, ErrNoLog
	}
	s.Runs = make([]Run, len(s.found))
	for i, r := range s.found {
		s.Runs[i] = summarize(r)
	}
	return s, nil
}

func stamp(ts parse.Timestamp) string {
	const digits = "0123456789"
	two := func(n uint8) string { return string([]byte{digits[n/10%10], digits[n%10]}) }
	return two(ts.Month) + "/" + two(ts.Day) + " " + two(ts.Hour) + ":" + two(ts.Min) + ":" + two(ts.Sec)
}

func summarize(r *segment.Run) Run {
	out := Run{
		Index: r.Index, Start: stamp(r.First), End: stamp(r.Last), ActiveMs: r.ActiveMs, Lines: r.Lines,
		Hits: r.Hits, Kills: r.KillCount(), Damage: r.DamageOut, PetDamage: r.PetDamage, Taken: r.DamageTaken,
		EndReason: r.EndReason, Inferred: r.Inferred, TopSpells: []Spell{}, Fingerprint: r.Fingerprint,
	}
	if r.ActiveMs > 0 {
		out.DPS = float64(r.DamageOut+r.PetDamage) / (float64(r.ActiveMs) / 1000)
	}
	for name, damage := range r.DamageBySpell {
		out.TopSpells = append(out.TopSpells, Spell{name, damage})
	}
	sort.Slice(out.TopSpells, func(i, j int) bool {
		if out.TopSpells[i].Damage != out.TopSpells[j].Damage {
			return out.TopSpells[i].Damage > out.TopSpells[j].Damage
		}
		return out.TopSpells[i].Name < out.TopSpells[j].Name
	})
	if len(out.TopSpells) > 3 {
		out.TopSpells = out.TopSpells[:3]
	}
	return out
}

// Extract returns the chosen runs as gzip, in log order, with each line exactly as the game wrote
// it. indexes are Run.Index values. Unknown ones are ignored.
func (s *Scan) Extract(indexes []int) ([]byte, error) {
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	if err := s.write(gz, indexes); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Text is Extract without compression, for a clean copy a player keeps beside the original.
func (s *Scan) Text(indexes []int) ([]byte, error) {
	var out bytes.Buffer
	if err := s.write(&out, indexes); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (s *Scan) write(w io.Writer, indexes []int) error {
	chosen := make(map[int]bool, len(indexes))
	for _, i := range indexes {
		chosen[i] = true
	}
	wrote := false
	for _, r := range s.found {
		if !chosen[r.Index] {
			continue
		}
		wrote = true
		for _, span := range s.spans[r.FirstLine-1 : r.LastLine] {
			if _, err := w.Write(s.text[span[0]:span[1]]); err != nil {
				return err
			}
			if _, err := w.Write([]byte{'\n'}); err != nil {
				return err
			}
		}
	}
	if !wrote {
		return errors.New("no runs chosen")
	}
	return nil
}

// RunLines returns one run's lines as the game wrote them, for matching addon saves to it. It is nil
// for an unknown index.
func (s *Scan) RunLines(index int) []string {
	for _, r := range s.found {
		if r.Index != index {
			continue
		}
		lines := make([]string, 0, r.LastLine-r.FirstLine+1)
		for _, span := range s.spans[r.FirstLine-1 : r.LastLine] {
			lines = append(lines, string(s.text[span[0]:span[1]]))
		}
		return lines
	}
	return nil
}
