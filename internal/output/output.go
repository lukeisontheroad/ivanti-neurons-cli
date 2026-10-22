// Package output renders CLI results as aligned tables, key/value lists, or
// raw JSON.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// Table writes rows as a tab-aligned table with an uppercased header row.
func Table(w io.Writer, headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.ToUpper(strings.Join(headers, "\t")))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
}

// JSON pretty-prints v (typically json.RawMessage or a slice of them).
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// Truncate shortens s to max runes, appending "…" when cut.
func Truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// Objects renders JSON objects as a table with the given columns. A column
// is a field name or dotted path ("customAttributes.attrs"); the header is
// the column string itself.
func Objects(w io.Writer, items []json.RawMessage, cols []string) {
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		var m map[string]any
		_ = decode(item, &m)
		row := make([]string, len(cols))
		for i, col := range cols {
			row[i] = Truncate(Format(col, lookup(m, col)), 60)
		}
		rows = append(rows, row)
	}
	Table(w, cols, rows)
}

// KV prints a JSON object as "key: value" lines, skipping null and empty
// values. With cols, only those fields are printed, in that order;
// otherwise all fields, sorted by name.
func KV(w io.Writer, obj json.RawMessage, cols []string) {
	var m map[string]any
	if err := decode(obj, &m); err != nil {
		fmt.Fprintln(w, string(obj))
		return
	}
	if len(cols) == 0 {
		for k := range m {
			cols = append(cols, k)
		}
		sort.Strings(cols)
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, k := range cols {
		if v := Format(k, lookup(m, k)); v != "" {
			fmt.Fprintf(tw, "%s:\t%s\n", k, Truncate(v, 200))
		}
	}
	tw.Flush()
}

func decode(data json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber() // ids are int64; float64 would mangle them
	return dec.Decode(v)
}

func lookup(m map[string]any, path string) any {
	var cur any = m
	for _, part := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[part]
	}
	return cur
}

// Format renders a JSON value for a table cell. Epoch-millisecond numbers in
// timestamp-like fields (createdAt, lastCheckin, ...) become UTC dates.
func Format(key string, v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		if ms, err := x.Int64(); err == nil && ms > 1e11 && isTimeKey(key) {
			return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04")
		}
		return x.String()
	case bool:
		return fmt.Sprint(x)
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			parts = append(parts, Format(key, e))
		}
		return strings.Join(parts, ", ")
	}
	data, _ := json.Marshal(v)
	return string(data)
}

func isTimeKey(key string) bool {
	k := strings.ToLower(key[strings.LastIndex(key, ".")+1:])
	for _, s := range []string{"at", "time", "date", "checkin", "expiry", "expires", "on", "before", "after"} {
		if strings.HasSuffix(k, s) {
			return true
		}
	}
	return false
}
