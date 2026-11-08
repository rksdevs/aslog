package saves

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// Write renders one top-level assignment as Lua literal data that Parse reads back unchanged. Keys
// are sorted, so the same data always gives the same file. Only values Parse produces are accepted:
// maps, lists, strings, numbers, booleans and nil.
func Write(name string, value any) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(name)
	b.WriteString(" = ")
	if err := writeValue(&b, value, 0); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

func writeValue(b *bytes.Buffer, value any, depth int) error {
	if depth > maxLuaDepth {
		return fmt.Errorf("%w: nested too deeply", ErrBadLua)
	}
	switch v := value.(type) {
	case nil:
		b.WriteString("nil")
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			b.WriteByte('0')
		} else {
			b.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
		}
	case int:
		b.WriteString(strconv.Itoa(v))
	case string:
		writeString(b, v)
	case []any:
		b.WriteByte('{')
		for _, item := range v {
			b.WriteByte('\n')
			indent(b, depth+1)
			if err := writeValue(b, item, depth+1); err != nil {
				return err
			}
			b.WriteByte(',')
		}
		closeTable(b, depth, len(v) > 0)
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for _, key := range keys {
			b.WriteByte('\n')
			indent(b, depth+1)
			b.WriteByte('[')
			// A numbered key from a gapped list stays a number, as the game wrote it.
			if n, err := strconv.Atoi(key); err == nil && strconv.Itoa(n) == key {
				b.WriteString(key)
			} else {
				writeString(b, key)
			}
			b.WriteString("] = ")
			if err := writeValue(b, v[key], depth+1); err != nil {
				return err
			}
			b.WriteByte(',')
		}
		closeTable(b, depth, len(v) > 0)
	default:
		return fmt.Errorf("%w: can't write %T", ErrBadLua, value)
	}
	return nil
}

func closeTable(b *bytes.Buffer, depth int, any bool) {
	if any {
		b.WriteByte('\n')
		indent(b, depth)
	}
	b.WriteByte('}')
}

func indent(b *bytes.Buffer, depth int) {
	for i := 0; i < depth; i++ {
		b.WriteByte('\t')
	}
}

// writeString escapes control bytes as three-digit codes, so a digit that follows can't join them.
func writeString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(b, `\%03d`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
}
