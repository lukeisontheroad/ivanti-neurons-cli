package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestFormat(t *testing.T) {
	for _, tc := range []struct {
		key, json, want string
	}{
		{"lastCheckin", `1792656000000`, "2026-10-22 08:00"},
		{"platformVersion", `1792656000000`, "1792656000000"},
		{"groups", `[{"id":1,"name":"All Users"},{"id":2,"name":"admin"}]`, "All Users, admin"},
		{"licenseTerm", `{"softLimit":100,"currentUsage":7,"hardLimit":0}`, "currentUsage=7, hardLimit=0, softLimit=100"},
		{"tags", `["a","b"]`, "a, b"},
	} {
		var v any
		dec := json.NewDecoder(strings.NewReader(tc.json))
		dec.UseNumber()
		_ = dec.Decode(&v)
		if got := Format(tc.key, v); got != tc.want {
			t.Errorf("Format(%s, %s) = %q, want %q", tc.key, tc.json, got, tc.want)
		}
	}
}

func TestObjectsMarksUTCColumns(t *testing.T) {
	var buf bytes.Buffer
	Objects(&buf, []json.RawMessage{[]byte(`{"id":1,"platformVersion":"18","lastCheckin":1792656000000}`)}, []string{"id", "platformVersion", "lastCheckin"})
	head := strings.SplitN(buf.String(), "\n", 2)[0]
	if !strings.Contains(head, "LASTCHECKIN (UTC)") || strings.Contains(head, "PLATFORMVERSION (UTC)") {
		t.Errorf("header = %q", head)
	}
}
