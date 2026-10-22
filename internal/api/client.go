// Package api is a minimal client for the Ivanti Neurons for MDM (formerly
// MobileIron Cloud) REST API
// (https://help.ivanti.com/mi/help/en_us/CLD/8x/api/default.htm).
package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Auth holds one credential shape: Username+Password (HTTP Basic) or
// ClientID+ClientSecret (OAuth2 client credentials against /oauth2/token).
// AccessToken/TokenExpiry may carry a cached token for the latter.
type Auth struct {
	Username     string
	Password     string
	ClientID     string
	ClientSecret string
	AccessToken  string
	TokenExpiry  time.Time
}

type Client struct {
	baseURL *url.URL
	auth    Auth
	http    *http.Client
	Verbose bool

	// Header holds extra headers sent with every request (e.g. the device
	// info header of the custom DEP enrollment profile call).
	Header http.Header

	// OnTokenRefresh, when set, is called after each OAuth2 token fetch so
	// the caller can cache the token (best-effort write-back).
	OnTokenRefresh func(accessToken string, expiry time.Time)
}

func New(rawURL string, auth Auth) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(rawURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("invalid instance URL %q: %w", rawURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("instance URL must start with http:// or https://, got %q", rawURL)
	}
	// No overall timeout: app uploads can take minutes, and the server may
	// process a large binary for a while before answering. A server that
	// never answers still times out.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 10 * time.Minute
	return &Client{
		baseURL: u,
		auth:    auth,
		// Unauthenticated API calls are redirected to the HTML login page;
		// surface the redirect instead of following it into a decode error.
		http: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}, nil
}

// BaseURL returns the instance base URL without a trailing slash.
func (c *Client) BaseURL() string { return c.baseURL.String() }

// oauth reports whether to use client credentials: Basic auth wins only
// with a complete username+password pair.
func (c *Client) oauth() bool {
	return c.auth.ClientID != "" && (c.auth.Username == "" || c.auth.Password == "")
}

// Error is an Ivanti API error. Messages come from the
// {"errors": {"fieldErrors": [...], "globalErrors": [...]}} envelope, or a
// plain Spring/OAuth error body.
type Error struct {
	StatusCode int
	Messages   []string
}

func (e *Error) Error() string {
	msg := strings.Join(e.Messages, "; ")
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	s := fmt.Sprintf("API error (HTTP %d): %s", e.StatusCode, msg)
	switch {
	case e.StatusCode >= 300 && e.StatusCode < 400:
		s += "\nhint: the tenant redirected to its login page: the credentials were not accepted (or the URL is not the tenant's admin portal host)"
	case e.StatusCode == http.StatusUnauthorized:
		s += "\nhint: check the credentials; Basic auth fails for API users with MFA — use an OIDC client (`auth login --client-id`)"
	case e.StatusCode == http.StatusForbidden:
		s += "\nhint: the API user's role lacks the permission for this call (or the object is in another device space)"
	}
	return s
}

// parseError extracts human-readable messages from an error body.
func parseError(status int, data []byte) *Error {
	e := &Error{StatusCode: status}
	var env struct {
		Errors struct {
			FieldErrors []struct {
				Code, Field, Message string
			} `json:"fieldErrors"`
			GlobalErrors []struct {
				Code, Message string
			} `json:"globalErrors"`
		} `json:"errors"`
		Message          string `json:"message"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if json.Unmarshal(data, &env) != nil {
		return e // non-JSON body (HTML proxy page etc.)
	}
	for _, g := range env.Errors.GlobalErrors {
		e.Messages = append(e.Messages, joinNonEmpty(g.Code, g.Message))
	}
	for _, f := range env.Errors.FieldErrors {
		e.Messages = append(e.Messages, joinNonEmpty(f.Field, joinNonEmpty(f.Code, f.Message)))
	}
	if len(e.Messages) == 0 {
		if m := joinNonEmpty(env.Error, firstNonEmpty(env.ErrorDescription, env.Message)); m != "" {
			e.Messages = append(e.Messages, m)
		}
	}
	return e
}

func joinNonEmpty(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + ": " + b
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Multipart is a multipart/form-data request body (file uploads).
type Multipart struct {
	Fields url.Values
	Files  map[string]string // form field name -> local file path
}

// shortcut: the whole body is built in memory (an IPA upload costs its size
// in RAM); stream with io.Pipe if multi-GB uploads become common.
func (m *Multipart) encode() ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, vs := range m.Fields {
		for _, v := range vs {
			if err := w.WriteField(k, v); err != nil {
				return nil, "", err
			}
		}
	}
	for field, path := range m.Files {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", err
		}
		part, err := w.CreateFormFile(field, filepath.Base(path))
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(data); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// encodeBody turns a request body into bytes plus Content-Type:
// url.Values -> form-urlencoded, *Multipart -> multipart/form-data,
// json.RawMessage / []byte -> verbatim JSON, anything else -> JSON-encoded.
func encodeBody(body any) ([]byte, string, error) {
	switch b := body.(type) {
	case nil:
		return nil, "", nil
	case url.Values:
		return []byte(b.Encode()), "application/x-www-form-urlencoded", nil
	case *Multipart:
		return b.encode()
	case json.RawMessage:
		return b, "application/json", nil
	case []byte:
		return b, "application/json", nil
	}
	data, err := json.Marshal(body)
	return data, "application/json", err
}

// Raw performs an API request and returns the response body bytes. path is
// relative to the instance root (e.g. "/api/v1/device"). Most Ivanti write
// endpoints take their parameters as query values (the server binds query
// and form parameters alike), so query is used on PUT/POST too; body is
// encoded per encodeBody. A 401 with OAuth credentials triggers one token
// refresh + retry; 429 retries twice with backoff.
func (c *Client) Raw(ctx context.Context, method, path string, query url.Values, body any) ([]byte, error) {
	payload, contentType, err := encodeBody(body)
	if err != nil {
		return nil, err
	}
	if err := c.ensureToken(ctx, false); err != nil {
		return nil, err
	}
	refreshed := false
	backoffs := []time.Duration{time.Second, 3 * time.Second}
	for attempt := 0; ; attempt++ {
		status, data, err := c.roundTrip(ctx, method, path, query, payload, contentType)
		if err != nil {
			return nil, err
		}
		rejected := status == http.StatusUnauthorized || (status >= 300 && status < 400)
		if rejected && c.oauth() && !refreshed {
			refreshed = true
			if err := c.ensureToken(ctx, true); err != nil {
				return nil, err
			}
			continue
		}
		if status == http.StatusTooManyRequests && attempt < len(backoffs) {
			select {
			case <-time.After(backoffs[attempt]):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		if status >= 300 {
			return nil, parseError(status, data)
		}
		return data, nil
	}
}

// Do performs a request and JSON-decodes the full response body into out
// (if non-nil and the body is non-empty).
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	data, err := c.Raw(ctx, method, path, query, body)
	if err != nil {
		return err
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// Call performs a request and returns the "result" member of the standard
// {"errors": ..., "result": ...} envelope. Responses without the envelope
// (e.g. custom attribute calls) are returned whole. A 2xx envelope with
// errors and a null result is returned as *Error.
func (c *Client) Call(ctx context.Context, method, path string, query url.Values, body any) (json.RawMessage, error) {
	data, err := c.Raw(ctx, method, path, query, body)
	if err != nil {
		return nil, err
	}
	return unwrap(data)
}

func unwrap(data []byte) (json.RawMessage, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return data, nil
	}
	var env map[string]json.RawMessage
	if json.Unmarshal(data, &env) != nil {
		return data, nil
	}
	result, hasResult := env["result"]
	errs, hasErrors := env["errors"]
	if !hasResult || !hasErrors {
		return data, nil
	}
	if isNull(result) && !isNull(errs) {
		if e := parseError(http.StatusOK, data); len(e.Messages) > 0 {
			return nil, e
		}
	}
	return result, nil
}

func isNull(r json.RawMessage) bool {
	s := string(bytes.TrimSpace(r))
	return s == "" || s == "null"
}

// Get is a convenience wrapper for GET requests returning the envelope result.
func (c *Client) Get(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	return c.Call(ctx, http.MethodGet, path, query, nil)
}

func (c *Client) roundTrip(ctx context.Context, method, path string, query url.Values, payload []byte, contentType string) (int, []byte, error) {
	u := *c.baseURL
	u.Path = strings.TrimRight(u.Path, "/") + path
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	var reqBody io.Reader
	if payload != nil {
		reqBody = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return 0, nil, err
	}
	if c.oauth() {
		req.Header.Set("Authorization", "Bearer "+c.auth.AccessToken)
	} else {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "ivanti-neurons-cli")
	for k, vs := range c.Header {
		req.Header[k] = vs
	}
	if payload != nil {
		req.Header.Set("Content-Type", contentType)
	}

	if c.Verbose {
		fmt.Fprintf(os.Stderr, "> %s %s\n", method, u.String())
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	if c.Verbose {
		fmt.Fprintf(os.Stderr, "< HTTP %d (%d bytes)\n", resp.StatusCode, len(data))
	}
	return resp.StatusCode, data, nil
}

// ensureToken fetches an OAuth2 access token (client credentials) when
// missing or expiring within 60s. No-op for Basic auth.
func (c *Client) ensureToken(ctx context.Context, force bool) error {
	if !c.oauth() {
		return nil
	}
	if !force && c.auth.AccessToken != "" && time.Until(c.auth.TokenExpiry) > 60*time.Second {
		return nil
	}
	if c.Verbose {
		fmt.Fprintln(os.Stderr, "> fetching OAuth2 access token")
	}
	u := *c.baseURL
	u.Path = strings.TrimRight(u.Path, "/") + "/oauth2/token"
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.auth.ClientID+":"+c.auth.ClientSecret)))
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("token request: %w", parseError(resp.StatusCode, data))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &tok); err != nil || tok.AccessToken == "" {
		return fmt.Errorf("token request: no access_token in response (HTTP %d)", resp.StatusCode)
	}
	if tok.ExpiresIn <= 0 {
		tok.ExpiresIn = 3600 // optional per RFC 6749; Ivanti issues ~1h tokens
	}
	c.auth.AccessToken = tok.AccessToken
	c.auth.TokenExpiry = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	if c.OnTokenRefresh != nil {
		c.OnTokenRefresh(c.auth.AccessToken, c.auth.TokenExpiry)
	}
	return nil
}
