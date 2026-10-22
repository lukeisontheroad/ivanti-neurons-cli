package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/api"
	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/config"
	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// active is the instance resolved by the last newClient call.
var active struct {
	name string
	inst config.Instance
}

// newClient validates --output, resolves the active instance and returns an
// API client for it. OAuth2 access tokens are cached in the config file
// best-effort.
func newClient() (*api.Client, error) {
	if err := validateOutput(); err != nil {
		return nil, err
	}
	name, inst, err := config.Resolve(flagInstance)
	if err != nil {
		return nil, err
	}
	active.name, active.inst = name, inst
	c, err := api.New(inst.URL, api.Auth{
		Username:     inst.Username,
		Password:     inst.Password,
		ClientID:     inst.ClientID,
		ClientSecret: inst.ClientSecret,
		AccessToken:  inst.AccessToken,
		TokenExpiry:  inst.TokenExpiry,
	})
	if err != nil {
		return nil, err
	}
	c.Verbose = flagVerbose
	c.OnTokenRefresh = func(token string, expiry time.Time) {
		_ = config.Update(name, func(i *config.Instance) { i.AccessToken, i.TokenExpiry = token, expiry })
	}
	return c, nil
}

// spaceID returns the device space id (dmPartitionId) for the call:
// --space (id or name), then IVANTI_SPACE, then the default cached at login,
// then the tenant metadata's defaultDmPartitionId.
func spaceID(cmd *cobra.Command, c *api.Client) (string, error) {
	arg := flagSpace
	if arg == "" {
		arg = os.Getenv(config.EnvSpace)
	}
	if arg != "" {
		id, err := c.ResolveSpace(cmd.Context(), arg)
		return strconv.FormatInt(id, 10), err
	}
	if active.inst.Space != 0 {
		return strconv.FormatInt(active.inst.Space, 10), nil
	}
	m, err := c.TenantMetadata(cmd.Context())
	if err != nil {
		return "", fmt.Errorf("detect default device space: %w", err)
	}
	cacheSpaces(m)
	return strconv.FormatInt(m.DefaultDmPartitionID, 10), nil
}

// contentSpaceID returns the content space id (cmPartitionId) used by app
// store calls (reviews, app delete, ...).
func contentSpaceID(cmd *cobra.Command, c *api.Client) (string, error) {
	if active.inst.ContentSpace != 0 {
		return strconv.FormatInt(active.inst.ContentSpace, 10), nil
	}
	m, err := c.TenantMetadata(cmd.Context())
	if err != nil {
		return "", fmt.Errorf("detect default content space: %w", err)
	}
	cacheSpaces(m)
	return strconv.FormatInt(m.DefaultCmPartitionID, 10), nil
}

func cacheSpaces(m *api.TenantMetadata) {
	active.inst.Space, active.inst.ContentSpace = m.DefaultDmPartitionID, m.DefaultCmPartitionID
	_ = config.Update(active.name, func(i *config.Instance) {
		i.Space, i.ContentSpace = m.DefaultDmPartitionID, m.DefaultCmPartitionID
	})
}

// params builds query parameters from alternating key/value pairs, skipping
// pairs with an empty value: params("q", q, "dmPartitionId", space).
func params(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			v.Add(kv[i], kv[i+1])
		}
	}
	return v
}

// splitArgs splits id arguments on commas and whitespace: ["1,2", "3"] ->
// ["1", "2", "3"].
func splitArgs(args []string) []string {
	var out []string
	for _, a := range args {
		out = append(out, strings.FieldsFunc(a, func(r rune) bool { return r == ',' || r == ' ' })...)
	}
	return out
}

// parseID validates one numeric id (or a GUID when guid is set) before it
// goes into a URL path or request. Only canonical decimals pass ("+5" and
// "007" do not), so the argument itself is safe to use after the check.
func parseID(arg string, guid bool) (string, error) {
	if n, err := strconv.ParseInt(arg, 10, 64); err == nil && n > 0 && strconv.FormatInt(n, 10) == arg {
		return arg, nil
	}
	if guid && isGUID(arg) {
		return arg, nil
	}
	return "", fmt.Errorf("invalid id %q (want a number)", arg)
}

// parseIDs splits and validates id arguments ("1,2" "3"), dropping
// duplicates. No arguments yields none (cobra's Args enforces required
// ids), but arguments that hold no ids are an error, so an empty "$IDS" in
// a script never means "none".
func parseIDs(args []string) ([]string, error) {
	var ids []string
	for _, a := range splitArgs(args) {
		if _, err := parseID(a, false); err != nil {
			return nil, err
		}
		if !slices.Contains(ids, a) {
			ids = append(ids, a)
		}
	}
	if len(ids) == 0 && len(args) > 0 {
		return nil, fmt.Errorf("no ids in %q", strings.Join(args, " "))
	}
	return ids, nil
}

// parsePage reads a search page ({totalCount, searchResults}) strictly:
// a missing searchResults is an error, never an empty list, so safety
// checks built on it fail closed. n is the larger of totalCount and the
// number of results.
func parsePage(raw json.RawMessage) (items []json.RawMessage, n int, err error) {
	var page struct {
		TotalCount    int                `json:"totalCount"`
		SearchResults *[]json.RawMessage `json:"searchResults"`
	}
	if err := json.Unmarshal(raw, &page); err != nil || page.SearchResults == nil {
		return nil, 0, fmt.Errorf("unexpected API result %s", output.Truncate(string(raw), 200))
	}
	return *page.SearchResults, max(page.TotalCount, len(*page.SearchResults)), nil
}

// jsonNumbers turns validated ids into JSON numbers (exact, unlike float64).
func jsonNumbers(ids []string) []json.Number {
	nums := make([]json.Number, len(ids))
	for i, id := range ids {
		nums[i] = json.Number(id)
	}
	return nums
}

// saveFile writes a downloaded file to out (mode 0600, never overwriting an
// existing file), or to stdout when out is empty.
func saveFile(cmd *cobra.Command, data []byte, out string) error {
	if out == "" {
		_, err := cmd.OutOrStdout().Write(data)
		return err
	}
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists: remove it or redirect stdout instead of --out", out)
	}
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(out) // don't leave a truncated file behind
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Saved %s (%d bytes)\n", out, len(data))
	return nil
}

var guidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isGUID reports whether s looks like a GUID (device and account endpoints
// accept numeric ids or GUIDs).
func isGUID(s string) bool { return guidRe.MatchString(s) }

// confirm asks before a destructive action. --yes skips the prompt; without
// a terminal the action is refused rather than silently executed.
func confirm(cmd *cobra.Command, action string) error {
	if err := validateOutput(); err != nil {
		return err // reject a bad -o before prompting
	}
	if flagYes {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("refusing to %s without confirmation: pass --yes", action)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s? [y/N]: ", strings.ToUpper(action[:1])+action[1:])
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		return fmt.Errorf("aborted")
	}
	return nil
}

// render prints an API result. Search pages ({totalCount, searchResults})
// are lists: a JSON array with -o json, a table otherwise. Other results
// print raw with -o json; in tables, arrays become rows (cols), objects
// key/value lines (cols selects and orders them; nil prints all), scalars
// their plain value.
func render(cmd *cobra.Command, raw json.RawMessage, cols []string) error {
	out := cmd.OutOrStdout()
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var page struct {
			SearchResults *[]json.RawMessage `json:"searchResults"`
		}
		if json.Unmarshal(trimmed, &page) == nil && page.SearchResults != nil {
			return renderItems(cmd, *page.SearchResults, cols)
		}
	}
	if flagOutput == "json" {
		return output.JSON(out, raw)
	}
	switch {
	case len(trimmed) == 0 || string(trimmed) == "null":
		return nil
	case trimmed[0] == '[':
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return err
		}
		return renderItems(cmd, items, cols)
	case trimmed[0] == '{':
		output.KV(out, trimmed, cols)
		return nil
	}
	var s any
	if json.Unmarshal(trimmed, &s) == nil {
		fmt.Fprintln(out, output.Format("", s))
		return nil
	}
	fmt.Fprintln(out, string(trimmed))
	return nil
}

// renderItems prints a list of objects: a JSON array with -o json, else a
// table with cols (nil: one compact JSON line per item, for odd shapes).
func renderItems(cmd *cobra.Command, items []json.RawMessage, cols []string) error {
	out := cmd.OutOrStdout()
	if items == nil {
		items = []json.RawMessage{}
	}
	if flagOutput == "json" {
		return output.JSON(out, items)
	}
	if len(items) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "No results.")
		return nil
	}
	if len(cols) == 0 {
		for _, it := range items {
			var buf bytes.Buffer
			if json.Compact(&buf, it) != nil {
				buf.Write(it)
			}
			fmt.Fprintln(out, buf.String())
		}
		return nil
	}
	output.Objects(out, items, cols)
	return nil
}

// report prints the result of a write call: raw JSON with -o json, else msg.
func report(cmd *cobra.Command, raw json.RawMessage, msg string) error {
	if flagOutput == "json" {
		if len(bytes.TrimSpace(raw)) == 0 {
			raw = json.RawMessage("null")
		}
		return output.JSON(cmd.OutOrStdout(), raw)
	}
	fmt.Fprintln(cmd.OutOrStdout(), msg)
	return nil
}

// searchOpts are the shared flags of list/search commands.
type searchOpts struct {
	query, fq, sort string
	limit           int
}

func (s *searchOpts) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&s.query, "query", "q", "", "full-text search term")
	cmd.Flags().StringVar(&s.fq, "fq", "", `filter query: field='value' clauses joined with AND/OR`)
	cmd.Flags().StringVar(&s.sort, "sort", "", `sort field, optionally with direction: "lastCheckin:desc"`)
	cmd.Flags().IntVar(&s.limit, "limit", 50, "maximum number of results (0 = all)")
}

// values returns the q/fq/sort query parameters.
func (s *searchOpts) values() url.Values {
	v := params("q", s.query, "fq", s.fq)
	if s.sort != "" {
		field, dir, _ := strings.Cut(s.sort, ":")
		if dir == "" {
			dir = "asc"
		}
		v.Set("sortFields[0].name", field)
		v.Set("sortFields[0].order", strings.ToUpper(dir))
	}
	return v
}

// sortForLargeFetch sorts by id when more than 10000 results may be
// fetched: device and account searches refuse start offsets of 10000 or
// more, and an id sort lets Search continue with searchAfterValues.
func (s *searchOpts) sortForLargeFetch() {
	if s.sort == "" && (s.limit <= 0 || s.limit > 10000) {
		s.sort = "id"
	}
}

// runSearch pages through a search endpoint and renders the results.
// extra parameters (dmPartitionId, type, ...) are merged into the query.
func runSearch(cmd *cobra.Command, c *api.Client, path string, s *searchOpts, extra url.Values, cols []string) error {
	q := s.values()
	for k, vs := range extra {
		q[k] = vs
	}
	page, err := c.Search(cmd.Context(), path, q, s.limit)
	if err != nil {
		return err
	}
	if flagOutput != "json" && page.TotalCount > len(page.SearchResults) {
		defer fmt.Fprintf(cmd.ErrOrStderr(), "Showing %d of %d (raise with --limit, 0 = all)\n", len(page.SearchResults), page.TotalCount)
	}
	return renderItems(cmd, page.SearchResults, cols)
}

// fieldValues parses repeatable key=value flags into query parameters.
func fieldValues(fields []string) (url.Values, error) {
	v := url.Values{}
	for _, f := range fields {
		k, val, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid field %q (want key=value)", f)
		}
		v.Add(k, val)
	}
	return v, nil
}

// jsonBody builds a JSON object request body from --data (inline, @file,
// or - for stdin) overlaid with -f key=value fields; field values that parse
// as JSON (numbers, true/false, null, arrays, objects) keep their type.
// Numbers decode as json.Number so int64 ids stay exact.
func jsonBody(cmd *cobra.Command, data string, fields []string) (map[string]any, error) {
	body := map[string]any{}
	if data != "" {
		raw, err := readData(cmd, data)
		if err != nil {
			return nil, err
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&body); err != nil || body == nil {
			return nil, fmt.Errorf("--data must be a JSON object")
		}
	}
	for _, f := range fields {
		k, val, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid field %q (want key=value)", f)
		}
		body[k] = coerceValue(val)
	}
	return body, nil
}

// parseAttrs parses custom attribute arguments ("key=value", repeat a key
// for several values) into the API's {"key": ["value", ...]} attrs map.
func parseAttrs(pairs []string) (map[string][]string, error) {
	attrs := map[string][]string{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid attribute %q (want key=value)", p)
		}
		attrs[k] = append(attrs[k], v)
	}
	return attrs, nil
}

func coerceValue(v string) any {
	dec := json.NewDecoder(strings.NewReader(v))
	dec.UseNumber()
	var parsed any
	if err := dec.Decode(&parsed); err == nil && !dec.More() {
		return parsed
	}
	return v
}

// readData reads a JSON request body given inline, as @file, or "-" for
// stdin, and validates it.
func readData(cmd *cobra.Command, arg string) (json.RawMessage, error) {
	var data []byte
	var err error
	switch {
	case arg == "-":
		data, err = io.ReadAll(cmd.InOrStdin())
	case strings.HasPrefix(arg, "@"):
		data, err = os.ReadFile(arg[1:])
	default:
		data = []byte(arg)
	}
	if err != nil {
		return nil, err
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("--data is not valid JSON")
	}
	return data, nil
}
