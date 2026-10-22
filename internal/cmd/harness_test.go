package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/config"
)

// mock is a fake Ivanti API for command-level tests. Register responses with
// on(); every request is recorded for assertions on queries and bodies.
// GET /api/v1/metadata/tenant is pre-registered (default device space 23000,
// content space 20000); unregistered routes answer 404 with an API error.
type mock struct {
	srv  *httptest.Server
	mux  *http.ServeMux
	mu   sync.Mutex
	reqs []request
}

type request struct {
	Method string
	Path   string
	Query  url.Values // URL query only
	Form   url.Values // form-urlencoded body, if any
	Body   string
	Header http.Header
}

// params merges query and form values: the server binds both alike.
func (r request) params() url.Values {
	v := url.Values{}
	for _, src := range []url.Values{r.Query, r.Form} {
		for k, vs := range src {
			v[k] = append(v[k], vs...)
		}
	}
	return v
}

func newMock(t *testing.T) *mock {
	t.Helper()
	return newMockFallback(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"errors":{"globalErrors":[{"code":"NotFound","message":"no mock route: %s %s"}]},"result":null}`, r.Method, r.URL.Path)
	})
}

// newMockFallback is newMock with a custom handler for unregistered routes.
func newMockFallback(t *testing.T, fallback http.HandlerFunc) *mock {
	t.Helper()
	m := &mock{mux: http.NewServeMux()}
	m.on("GET /api/v1/metadata/tenant", `{"errors":null,"result":{"tenantGuid":"t-guid","defaultDmPartitionId":23000,"defaultCmPartitionId":20000,"webAppVersion":{"version":"99.0"}}}`)
	m.mux.HandleFunc("/", fallback)
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		m.mu.Lock()
		var form url.Values
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			form, _ = url.ParseQuery(string(body))
		}
		m.reqs = append(m.reqs, request{r.Method, r.URL.Path, r.URL.Query(), form, string(body), r.Header.Clone()})
		m.mu.Unlock()
		m.mux.ServeHTTP(w, r)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

// on registers a fixed JSON response for a ServeMux pattern such as
// "GET /api/v1/device" or "PUT /api/v1/device/{id}/uninstallconfigs".
func (m *mock) on(pattern, response string) {
	m.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, response)
	})
}

// last returns the most recent request matching method and path.
func (m *mock) last(t *testing.T, method, path string) request {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.reqs) - 1; i >= 0; i-- {
		if m.reqs[i].Method == method && m.reqs[i].Path == path {
			return m.reqs[i]
		}
	}
	t.Fatalf("no %s %s request recorded", method, path)
	return request{}
}

// runErr executes the CLI against the mock with Basic env credentials and an
// isolated config file; returns stdout+stderr combined.
func (m *mock) runErr(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv(config.EnvURL, m.srv.URL)
	t.Setenv(config.EnvUsername, "apiuser")
	t.Setenv(config.EnvPassword, "secret")
	t.Setenv(config.EnvConfig, filepath.Join(t.TempDir(), "config.yml"))
	for _, e := range []string{config.EnvClientID, config.EnvClientSecret, config.EnvInstance, config.EnvSpace} {
		t.Setenv(e, "")
	}
	active.name, active.inst = "", config.Instance{}
	root := newRootCmd("test")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func (m *mock) run(t *testing.T, args ...string) string {
	t.Helper()
	out, err := m.runErr(t, args...)
	if err != nil {
		t.Fatalf("%v failed: %v\noutput:\n%s", args, err, out)
	}
	return out
}

// count returns how many requests matched method and path ("" = any path).
func (m *mock) count(method, path string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.reqs {
		if r.Method == method && (path == "" || r.Path == path) {
			n++
		}
	}
	return n
}

// jsonEq compares JSON documents independent of key order.
func jsonEq(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("body is not JSON (%v): %q", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad want JSON: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("body = %s\nwant   %s", got, want)
	}
}

// mustContain fails unless out contains every want string.
func mustContain(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q:\n%s", w, out)
		}
	}
}
