package cmd

import (
	"net/http"
	"strings"
	"testing"
)

func TestAPIRawWithFields(t *testing.T) {
	m := newMock(t)
	m.on("PUT /api/v1/device/forceCheckin", `{"errors":null,"result":2}`)
	out := m.run(t, "api", "put", "api/v1/device/forceCheckin", "-f", "ids=1", "-f", "ids=2")
	mustContain(t, out, `"result": 2`)
	req := m.last(t, "PUT", "/api/v1/device/forceCheckin")
	if got := req.Query["ids"]; len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("ids not sent as repeated query params: %v", req.Query)
	}
	if u, p, ok := basicAuth(req); !ok || u != "apiuser" || p != "secret" {
		t.Fatalf("basic auth missing: %q", req.Header.Get("Authorization"))
	}
}

func TestAuthStatusUsesDefaultSpace(t *testing.T) {
	m := newMock(t)
	out := m.run(t, "auth", "status")
	mustContain(t, out, "t-guid", "99.0", "apiuser", "Space:    23000")
}

func TestSpaceByName(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/tenant/partition/device", `{"errors":null,"result":{"totalCount":2,"searchResults":[{"id":23000,"name":"Default Space"},{"id":24000,"name":"Kiosks"}]}}`)
	out := m.run(t, "auth", "status", "--space", "kiosks")
	mustContain(t, out, "Space:    24000")
	_, err := m.runErr(t, "auth", "status", "--space", "nope")
	if err == nil || !strings.Contains(err.Error(), "Kiosks") {
		t.Fatalf("expected unknown-space error listing names, got %v", err)
	}
}

func TestConfirmRefusesWithoutTTY(t *testing.T) {
	root := newRootCmd("test")
	flagYes = false
	if err := confirm(root, "wipe 2 devices"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected refusal, got %v", err)
	}
	flagYes = true
	defer func() { flagYes = false }()
	if err := confirm(root, "wipe 2 devices"); err != nil {
		t.Fatal(err)
	}
}

func TestDocsListsCommands(t *testing.T) {
	m := newMock(t)
	out := m.run(t, "docs")
	mustContain(t, out, "### ivanti-neurons auth login [write]", "### ivanti-neurons api [read or write",
		"### ivanti-neurons device wipe [destructive; --check is read-only]", "### ivanti-neurons device list [read]",
		"## Ids:", "## Common questions", "List flags: -q/--query")
	if strings.Contains(out, "--fq string") {
		t.Error("shared list flags should be explained once, not per command")
	}
	device := m.run(t, "docs", "device")
	if strings.Contains(device, "## Setup") || !strings.Contains(device, "### ivanti-neurons device view") || strings.Contains(device, "### ivanti-neurons user list") {
		t.Error("docs device should print only the device commands")
	}
	if _, err := m.runErr(t, "docs", "nope"); err == nil {
		t.Error("docs nope: want an error")
	}
}

// The safety tag lists must name real commands, or renamed commands would
// silently become [write].
func TestDocsTagPathsExist(t *testing.T) {
	root := newRootCmd("test")
	for _, path := range append(append([]string{}, docsReadOnly...), docsDestructive...) {
		c, rest, err := root.Find(strings.Fields(path))
		if err != nil || len(rest) > 0 || c.HasSubCommands() || c.CommandPath() != "ivanti-neurons "+path {
			t.Errorf("tagged command %q does not exist", path)
		}
	}
}

func TestUnknownSubcommandFails(t *testing.T) {
	m := newMock(t)
	for _, args := range [][]string{{"device", "foo"}, {"group", "members", "list", "1"}} {
		if _, err := m.runErr(t, args...); err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	m.run(t, "device") // a bare group still prints its help
}

func basicAuth(r request) (string, string, bool) {
	return (&http.Request{Header: r.Header}).BasicAuth()
}

func TestParseIDCanonicalOnly(t *testing.T) {
	for _, bad := range []string{"+5", "007", "0", "-1", "1e3", "22000;rm", ""} {
		if _, err := parseID(bad, false); err == nil {
			t.Errorf("parseID(%q) accepted", bad)
		}
	}
	if id, err := parseID("22000", false); err != nil || id != "22000" {
		t.Errorf("parseID(22000) = %q, %v", id, err)
	}
	if _, err := parseID(coverageGUID, true); err != nil {
		t.Errorf("GUID rejected: %v", err)
	}
}

func TestAuthBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://eu1.mobileiron.com/login.html#!/": "https://eu1.mobileiron.com",
		"eu1.mobileiron.com":                       "https://eu1.mobileiron.com",
		"https://na1.mobileiron.com/":              "https://na1.mobileiron.com",
	} {
		if got, err := authBaseURL(in); err != nil || got != want {
			t.Errorf("authBaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}
