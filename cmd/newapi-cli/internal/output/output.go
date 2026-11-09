package output

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

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

// Pretty renders raw in a human-readable form: objects become "key: value"
// lines, arrays of objects become an aligned table, and list envelopes with
// an "items" array additionally print pagination info.
func Pretty(w io.Writer, raw json.RawMessage) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		_, err := w.Write(append(raw, '\n'))
		return err
	}
	switch t := v.(type) {
	case []any:
		writeTable(w, t)
	case map[string]any:
		if items, ok := t["items"].([]any); ok {
			writeTable(w, items)
			for _, k := range []string{"total", "page", "page_size"} {
				if val, ok := t[k]; ok {
					fmt.Fprintf(w, "%s: %v\n", k, val)
				}
			}
			return nil
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "%s: %s\n", k, prettyValue(t[k]))
		}
	default:
		fmt.Fprintln(w, prettyValue(v))
	}
	return nil
}

func prettyValue(v any) string {
	switch t := v.(type) {
	case map[string]any, []any:
		if data, err := json.Marshal(t); err == nil {
			return string(data)
		}
	}
	return fmt.Sprintf("%v", v)
}

func writeTable(w io.Writer, items []any) {
	rows := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			rows = append(rows, m)
		}
	}
	if len(rows) == 0 {
		fmt.Fprintf(w, "(%d items)\n", len(items))
		return
	}
	cols := make([]string, 0, len(rows[0]))
	for k := range rows[0] {
		cols = append(cols, k)
	}
	slices.Sort(cols)
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len(c)
	}
	cells := make([][]string, len(rows))
	for r, row := range rows {
		cells[r] = make([]string, len(cols))
		for i, c := range cols {
			cells[r][i] = prettyValue(row[c])
			widths[i] = max(widths[i], len(cells[r][i]))
		}
	}
	var sb strings.Builder
	for i, c := range cols {
		if i > 0 {
			sb.WriteString("  ")
		}
		fmt.Fprintf(&sb, "%-*s", widths[i], c)
	}
	fmt.Fprintln(w, sb.String())
	for _, row := range cells {
		sb.Reset()
		for i, cell := range row {
			if i > 0 {
				sb.WriteString("  ")
			}
			fmt.Fprintf(&sb, "%-*s", widths[i], cell)
		}
		fmt.Fprintln(w, sb.String())
	}
}
