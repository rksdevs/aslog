// Package saves reads and writes the addon save files (SavedVariables) that sit beside an Azeroth
// Survivors log, and ties their runs to the log's runs. The server and the uploader both use it, so
// a run matches in the same way everywhere.
package saves

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// A SavedVariables file is Lua source written by the game, and players can edit it. It is read here
// as literal data only: anything but tables, strings, numbers and booleans is an error, so nothing
// in it can run.
const (
	maxLuaDepth = 32
	maxLuaNodes = 4_000_000
)

// ErrBadLua means the file isn't literal SavedVariables data.
var ErrBadLua = errors.New("not a saved variables file")

type luaReader struct {
	src   string
	pos   int
	depth int
	nodes int
}

// Parse returns each top-level assignment. A table with keys 1..n becomes []any and any other table
// becomes map[string]any.
func Parse(src string) (map[string]any, error) {
	r := &luaReader{src: strings.TrimPrefix(src, "\xef\xbb\xbf")}
	out := map[string]any{}
	for {
		r.skip()
		if r.pos >= len(r.src) {
			return out, nil
		}
		name := r.identifier()
		if name == "" {
			return nil, r.fail("expected a variable name")
		}
		r.skip()
		if !r.take('=') {
			return nil, r.fail("expected =")
		}
		value, err := r.value()
		if err != nil {
			return nil, err
		}
		out[name] = value
	}
}

func (r *luaReader) fail(reason string) error {
	return fmt.Errorf("%w: %s at byte %d", ErrBadLua, reason, r.pos)
}

func (r *luaReader) take(c byte) bool {
	if r.pos < len(r.src) && r.src[r.pos] == c {
		r.pos++
		return true
	}
	return false
}

// skip passes whitespace and -- comments.
func (r *luaReader) skip() {
	for r.pos < len(r.src) {
		switch c := r.src[r.pos]; {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			r.pos++
		case strings.HasPrefix(r.src[r.pos:], "--"):
			if end := strings.IndexByte(r.src[r.pos:], '\n'); end >= 0 {
				r.pos += end + 1
			} else {
				r.pos = len(r.src)
			}
		default:
			return
		}
	}
}

func (r *luaReader) identifier() string {
	start := r.pos
	for r.pos < len(r.src) {
		c := r.src[r.pos]
		isLetter := c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !isLetter && !(r.pos > start && c >= '0' && c <= '9') {
			break
		}
		r.pos++
	}
	return r.src[start:r.pos]
}

func (r *luaReader) value() (any, error) {
	r.skip()
	if r.nodes++; r.nodes > maxLuaNodes {
		return nil, r.fail("too much data")
	}
	if r.pos >= len(r.src) {
		return nil, r.fail("unexpected end")
	}
	switch c := r.src[r.pos]; {
	case c == '{':
		return r.table()
	case c == '"' || c == '\'':
		return r.str()
	case c == '-' || c == '.' || c >= '0' && c <= '9':
		return r.number()
	}
	start := r.pos
	switch word := r.identifier(); word {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "nil":
		return nil, nil
	case "inf", "nan":
		return 0.0, nil
	default:
		r.pos = start
		return nil, r.fail("only literal data is allowed")
	}
}

func (r *luaReader) number() (float64, error) {
	start := r.pos
	for r.pos < len(r.src) && strings.IndexByte("+-.0123456789abcdefxABCDEFXinfINFnaNA", r.src[r.pos]) >= 0 {
		r.pos++
	}
	text := r.src[start:r.pos]
	if text == "-inf" || text == "-nan" || text == "inf" || text == "nan" {
		return 0, nil
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, r.fail("bad number")
	}
	return n, nil
}

func (r *luaReader) str() (string, error) {
	quote := r.src[r.pos]
	r.pos++
	var b strings.Builder
	for r.pos < len(r.src) {
		c := r.src[r.pos]
		r.pos++
		switch c {
		case quote:
			return b.String(), nil
		case '\\':
			if r.pos >= len(r.src) {
				return "", r.fail("unfinished string")
			}
			e := r.src[r.pos]
			r.pos++
			switch {
			case e == 'n':
				b.WriteByte('\n')
			case e == 't':
				b.WriteByte('\t')
			case e == 'r':
				b.WriteByte('\r')
			case e >= '0' && e <= '9':
				code := int(e - '0')
				for n := 0; n < 2 && r.pos < len(r.src) && r.src[r.pos] >= '0' && r.src[r.pos] <= '9'; n++ {
					code = code*10 + int(r.src[r.pos]-'0')
					r.pos++
				}
				b.WriteByte(byte(code))
			default:
				b.WriteByte(e) // \\ \" \' and a backslash before a line break
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", r.fail("unfinished string")
}

func (r *luaReader) table() (any, error) {
	if r.depth++; r.depth > maxLuaDepth {
		return nil, r.fail("nested too deeply")
	}
	defer func() { r.depth-- }()
	r.pos++ // {

	fields := map[string]any{}
	next := 1
	for {
		r.skip()
		if r.take('}') {
			return tableValue(fields), nil
		}
		key := ""
		switch {
		case r.take('['):
			k, err := r.value()
			if err != nil {
				return nil, err
			}
			r.skip()
			if !r.take(']') {
				return nil, r.fail("expected ]")
			}
			r.skip()
			if !r.take('=') {
				return nil, r.fail("expected =")
			}
			switch v := k.(type) {
			case string:
				key = v
			case float64:
				key = strconv.FormatFloat(v, 'f', -1, 64)
			default:
				return nil, r.fail("table key must be a string or a number")
			}
		default:
			start := r.pos
			name := r.identifier()
			r.skip()
			if name != "" && name != "true" && name != "false" && name != "nil" && r.take('=') {
				key = name
			} else {
				r.pos = start
				key = strconv.Itoa(next)
				next++
			}
		}
		value, err := r.value()
		if err != nil {
			return nil, err
		}
		fields[key] = value
		r.skip()
		if !r.take(',') && !r.take(';') {
			if r.pos < len(r.src) && r.src[r.pos] == '}' {
				continue
			}
			return nil, r.fail("expected , or }")
		}
	}
}

func tableValue(fields map[string]any) any {
	if len(fields) == 0 {
		return []any{}
	}
	list := make([]any, len(fields))
	for i := range list {
		value, ok := fields[strconv.Itoa(i+1)]
		if !ok {
			return fields
		}
		list[i] = value
	}
	return list
}

// Map, List, String, Float and Bool read parsed values without panicking on the wrong type.
func Map(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// List also accepts a map, for a table whose numbered keys have gaps.
func List(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case map[string]any:
		out := make([]any, 0, len(t))
		for _, value := range t {
			out = append(out, value)
		}
		return out
	}
	return nil
}

func String(v any) string {
	s, _ := v.(string)
	return s
}

func Float(v any) float64 {
	n, _ := v.(float64)
	return n
}

func Bool(v any) bool {
	b, _ := v.(bool)
	return b
}
