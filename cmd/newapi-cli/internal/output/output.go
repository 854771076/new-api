package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxCellRunes bounds the rune count of a rendered table cell; its display
// width can be larger for wide (CJK) text.
const maxCellRunes = 48

// JSON writes v as JSON followed by a newline. json.RawMessage is written
// verbatim (or "null" when empty) so server data passes through unmodified.
func JSON(w io.Writer, v any) error {
	if raw, ok := v.(json.RawMessage); ok {
		if len(raw) == 0 {
			raw = json.RawMessage("null")
		}
		_, err := w.Write(append(raw, '\n'))
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Error writes {"error":{"message":...,"code":...}} to w (typically stderr).
func Error(w io.Writer, message, code string) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"message": message, "code": code},
	})
}

// orderedObject is a JSON object that remembers its key order, because
// encoding/json maps lose it and table columns should follow server order.
type orderedObject struct {
	keys   []string
	values map[string]any
}

// MarshalJSON re-emits the object with its original key order.
func (o *orderedObject) MarshalJSON() ([]byte, error) {
	var sb strings.Builder
	sb.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		encodedValue, err := json.Marshal(o.values[key])
		if err != nil {
			return nil, err
		}
		sb.Write(encodedKey)
		sb.WriteByte(':')
		sb.Write(encodedValue)
	}
	sb.WriteByte('}')
	return []byte(sb.String()), nil
}

// Pretty renders raw in a human-readable form: objects become "key: value"
// lines, arrays of objects become an aligned table, and list envelopes with
// an "items" array additionally print pagination info.
func Pretty(w io.Writer, raw json.RawMessage) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := decodeOrdered(dec)
	if err != nil {
		if _, writeErr := w.Write(append(raw, '\n')); writeErr != nil {
			return writeErr
		}
		return err
	}
	switch t := v.(type) {
	case []any:
		if len(t) > 0 && !slices.ContainsFunc(t, isObject) {
			for _, item := range t {
				fmt.Fprintln(w, prettyValue(item))
			}
			return nil
		}
		writeTable(w, t)
	case *orderedObject:
		if items, ok := t.values["items"].([]any); ok {
			writeTable(w, items)
			for _, k := range []string{"total", "page", "page_size"} {
				if val, ok := t.values[k]; ok {
					fmt.Fprintf(w, "%s: %v\n", k, val)
				}
			}
			return nil
		}
		keys := slices.Clone(t.keys)
		slices.Sort(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "%s: %s\n", k, prettyValue(t.values[k]))
		}
	default:
		fmt.Fprintln(w, prettyValue(v))
	}
	return nil
}

// decodeOrdered decodes one JSON value, keeping object key order and number
// literals (json.Number) so the output mirrors the server payload exactly.
func decodeOrdered(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch delim {
	case '{':
		obj := &orderedObject{values: map[string]any{}}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyTok.(string)
			if !ok {
				return nil, fmt.Errorf("unexpected object key %v", keyTok)
			}
			value, err := decodeOrdered(dec)
			if err != nil {
				return nil, err
			}
			if _, exists := obj.values[key]; !exists {
				obj.keys = append(obj.keys, key)
			}
			obj.values[key] = value
		}
		if _, err := dec.Token(); err != nil { // closing brace
			return nil, err
		}
		return obj, nil
	case '[':
		items := []any{}
		for dec.More() {
			value, err := decodeOrdered(dec)
			if err != nil {
				return nil, err
			}
			items = append(items, value)
		}
		if _, err := dec.Token(); err != nil { // closing bracket
			return nil, err
		}
		return items, nil
	}
	return nil, fmt.Errorf("unexpected delimiter %q", delim)
}

func isObject(v any) bool {
	_, ok := v.(*orderedObject)
	return ok
}

func prettyValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case *orderedObject, []any:
		if data, err := json.Marshal(t); err == nil {
			return string(data)
		}
	}
	return fmt.Sprintf("%v", v)
}

// truncateCell shortens s to maxCellRunes runes and appends an ellipsis so a
// table stays readable regardless of nested payload size.
func truncateCell(s string) string {
	if utf8.RuneCountInString(s) <= maxCellRunes {
		return s
	}
	return string([]rune(s)[:maxCellRunes]) + "…"
}

// singleLine collapses embedded line breaks so one record never spans rows.
func singleLine(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		}
		return r
	}, s)
}

// wideRanges lists the East Asian Wide/Fullwidth blocks and emoji planes that
// terminals render two columns wide. Ranges are sorted and non-overlapping.
var wideRanges = [][2]rune{
	{0x1100, 0x115f},   // Hangul Jamo
	{0x2e80, 0x303e},   // CJK radicals, Kangxi, CJK symbols and punctuation
	{0x3041, 0x33ff},   // Hiragana, Katakana, Bopomofo, Hangul compat, enclosed CJK
	{0x3400, 0x4dbf},   // CJK unified ideographs extension A
	{0x4e00, 0x9fff},   // CJK unified ideographs
	{0xa000, 0xa4cf},   // Yi
	{0xa960, 0xa97f},   // Hangul Jamo extended A
	{0xac00, 0xd7a3},   // Hangul syllables
	{0xf900, 0xfaff},   // CJK compatibility ideographs
	{0xfe10, 0xfe19},   // vertical forms
	{0xfe30, 0xfe6f},   // CJK compatibility forms
	{0xff00, 0xff60},   // fullwidth forms
	{0xffe0, 0xffe6},   // fullwidth signs
	{0x1f300, 0x1f64f}, // emoji (misc symbols and pictographs, emoticons)
	{0x1f680, 0x1f6ff}, // emoji transport
	{0x1f900, 0x1f9ff}, // emoji supplemental symbols
	{0x1fa70, 0x1faff}, // emoji symbols extended A
	{0x20000, 0x3fffd}, // CJK extensions B and beyond
}

func isWideRune(r rune) bool {
	for _, rg := range wideRanges {
		if r < rg[0] {
			return false
		}
		if r <= rg[1] {
			return true
		}
	}
	return false
}

// runeWidth approximates the terminal columns r occupies.
func runeWidth(r rune) int {
	switch {
	case r == 0 || r == '\u200d' || r == '\ufe0e' || r == '\ufe0f':
		return 0
	case r >= 0x1f3fb && r <= 0x1f3ff: // emoji skin tone modifiers
		return 0
	case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r):
		return 0
	case isWideRune(r):
		return 2
	}
	return 1
}

// displayWidth returns the approximate number of columns s occupies.
func displayWidth(s string) int {
	width := 0
	for _, r := range s {
		width += runeWidth(r)
	}
	return width
}

func writeTable(w io.Writer, items []any) {
	rows := make([]*orderedObject, 0, len(items))
	for _, item := range items {
		if m, ok := item.(*orderedObject); ok {
			rows = append(rows, m)
		}
	}
	if len(rows) == 0 {
		fmt.Fprintf(w, "(%d items)\n", len(items))
		return
	}
	// Columns are the union of every row's keys in first-seen order, so a
	// field that only appears in later rows still gets a column.
	var cols []string
	seen := make(map[string]bool)
	for _, row := range rows {
		for _, k := range row.keys {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = displayWidth(c)
	}
	cells := make([][]string, len(rows))
	for r, row := range rows {
		cells[r] = make([]string, len(cols))
		for i, c := range cols {
			text := ""
			if v, ok := row.values[c]; ok {
				text = prettyValue(v)
			}
			cells[r][i] = truncateCell(singleLine(text))
			widths[i] = max(widths[i], displayWidth(cells[r][i]))
		}
	}
	lines := make([][]string, 0, len(cells)+1)
	lines = append(lines, cols)
	lines = append(lines, cells...)
	for _, line := range lines {
		var sb strings.Builder
		for i, cell := range line {
			if i > 0 {
				sb.WriteString("  ")
			}
			sb.WriteString(cell)
			sb.WriteString(strings.Repeat(" ", widths[i]-displayWidth(cell)))
		}
		fmt.Fprintln(w, sb.String())
	}
}
