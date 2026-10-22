package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testServer(t *testing.T, auth Auth, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, auth)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCallUnwrapsEnvelope(t *testing.T) {
	c := testServer(t, Auth{Username: "u", Password: "p"}, func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "u" || p != "p" {
			t.Errorf("bad basic auth")
		}
		fmt.Fprint(w, `{"errors":null,"result":{"id":7}}`)
	})
	raw, err := c.Call(context.Background(), http.MethodGet, "/api/v1/device/7", nil, nil)
	if err != nil || string(raw) != `{"id":7}` {
		t.Fatalf("got %s, %v", raw, err)
	}
}

func TestCallPassesNonEnvelope(t *testing.T) {
	c := testServer(t, Auth{Username: "u", Password: "p"}, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"attrs":{"cityId":["SF"]}}`)
	})
	raw, err := c.Call(context.Background(), http.MethodGet, "/x", nil, nil)
	if err != nil || !strings.Contains(string(raw), "attrs") {
		t.Fatalf("got %s, %v", raw, err)
	}
}

func TestErrorMapping(t *testing.T) {
	c := testServer(t, Auth{Username: "u", Password: "p"}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"errors":{"fieldErrors":[{"code":"Range","message":"must be between 1 and 500","field":"rows"}],"globalErrors":null},"result":null}`)
	})
	_, err := c.Call(context.Background(), http.MethodGet, "/x", nil, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 || !strings.Contains(err.Error(), "rows: Range: must be between 1 and 500") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestErrorIn200Envelope(t *testing.T) {
	c := testServer(t, Auth{Username: "u", Password: "p"}, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"errors":{"globalErrors":[{"code":"E","message":"boom"}]},"result":null}`)
	})
	if _, err := c.Call(context.Background(), http.MethodGet, "/x", nil, nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected boom, got %v", err)
	}
}

func TestOAuthTokenAndRefresh(t *testing.T) {
	var tokens, calls int32
	c := testServer(t, Auth{ClientID: "cid", ClientSecret: "sec"}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" {
			if id, sec, ok := r.BasicAuth(); !ok || id != "cid" || sec != "sec" || r.FormValue("grant_type") != "client_credentials" {
				t.Errorf("bad token request")
			}
			n := atomic.AddInt32(&tokens, 1)
			fmt.Fprintf(w, `{"access_token":"tok%d","token_type":"Bearer","expires_in":3599}`, n)
			return
		}
		// First API call rejects the first token to exercise reactive refresh.
		if atomic.AddInt32(&calls, 1) == 1 || r.Header.Get("Authorization") != "Bearer tok2" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"errors":null,"result":true}`)
	})
	var saved string
	c.OnTokenRefresh = func(tok string, _ time.Time) { saved = tok }
	raw, err := c.Call(context.Background(), http.MethodGet, "/x", nil, nil)
	if err != nil || string(raw) != "true" || tokens != 2 || saved != "tok2" {
		t.Fatalf("raw=%s err=%v tokens=%d saved=%s", raw, err, tokens, saved)
	}
}

func TestSearchPaginatesByTotalCount(t *testing.T) {
	var starts []string
	c := testServer(t, Auth{Username: "u", Password: "p"}, func(w http.ResponseWriter, r *http.Request) {
		starts = append(starts, r.URL.Query().Get("start"))
		// Server caps pages at 2 rows regardless of the rows parameter.
		start := len(starts)*2 - 2
		items := []string{}
		for i := start; i < start+2 && i < 5; i++ {
			items = append(items, fmt.Sprintf(`{"id":%d}`, i))
		}
		fmt.Fprintf(w, `{"errors":null,"result":{"totalCount":5,"searchResults":[%s]}}`, strings.Join(items, ","))
	})
	page, err := c.Search(context.Background(), "/api/v1/group", nil, 0)
	if err != nil || len(page.SearchResults) != 5 || page.TotalCount != 5 {
		t.Fatalf("got %d results, err %v", len(page.SearchResults), err)
	}
	if strings.Join(starts, ",") != "0,2,4" {
		t.Fatalf("unexpected start offsets: %v", starts)
	}
}

func TestSearchAfterPastThreshold(t *testing.T) {
	var lastQuery string
	c := testServer(t, Auth{Username: "u", Password: "p"}, func(w http.ResponseWriter, r *http.Request) {
		lastQuery = r.URL.RawQuery
		var items []string
		for i := 0; i < MaxRows; i++ {
			items = append(items, fmt.Sprintf(`{"id":%d}`, 100000+i))
		}
		fmt.Fprintf(w, `{"errors":null,"result":{"totalCount":20000,"searchResults":[%s]}}`, strings.Join(items, ","))
	})
	q := map[string][]string{"sortFields[0].name": {"id"}, "start": {"9900"}}
	page, err := c.Search(context.Background(), "/api/v1/device", q, 1000)
	if err != nil || len(page.SearchResults) != 1000 {
		t.Fatalf("got %d, %v", len(page.SearchResults), err)
	}
	if !strings.Contains(lastQuery, "searchAfterValues%5B0%5D=100499") || strings.Contains(lastQuery, "start=") {
		t.Fatalf("second page should use searchAfterValues: %s", lastQuery)
	}
}

func TestOAuthWinsOverIncompleteBasic(t *testing.T) {
	c := testServer(t, Auth{Username: "leftover", ClientID: "cid", ClientSecret: "sec"}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" {
			fmt.Fprint(w, `{"access_token":"tok"}`) // no expires_in
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		fmt.Fprint(w, `{"errors":null,"result":true}`)
	})
	if _, err := c.Call(context.Background(), http.MethodGet, "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if time.Until(c.auth.TokenExpiry) < 50*time.Minute {
		t.Fatalf("missing expires_in should default to ~1h, got %v", time.Until(c.auth.TokenExpiry))
	}
}

func TestLoginRedirectIsAnError(t *testing.T) {
	c := testServer(t, Auth{Username: "u", Password: "wrong"}, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login.html", http.StatusFound)
	})
	_, err := c.Call(context.Background(), http.MethodGet, "/api/v1/metadata/tenant", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "credentials were not accepted") {
		t.Fatalf("want login redirect error, got %v", err)
	}
}
