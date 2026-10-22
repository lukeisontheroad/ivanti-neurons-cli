package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// MaxRows is the server-side cap on the rows parameter; larger values are
// rejected with HTTP 400 ("must be between 1 and 500").
const MaxRows = 500

// searchAfterThreshold: device and account searches must not use start
// offsets of 10000 or more; past it they page with searchAfterValues.
const searchAfterThreshold = 10000

// Page is the result shape shared by every search endpoint.
type Page struct {
	TotalCount    int               `json:"totalCount"`
	SearchResults []json.RawMessage `json:"searchResults"`
}

// Search pages through a search endpoint (rows/start) until limit results
// (limit <= 0: all) or totalCount is reached. Pages may come back shorter
// than requested (database-backed searches cap at 50 rows), so the loop
// trusts totalCount, not page length. When the query sorts by id, offsets
// at or past 10000 switch to searchAfterValues[0]=<last id>, as the API
// guide requires for large device/account sets.
func (c *Client) Search(ctx context.Context, path string, query url.Values, limit int) (Page, error) {
	q := url.Values{}
	for k, v := range query {
		q[k] = v
	}
	var all Page
	start := 0
	if s := q.Get("start"); s != "" {
		start, _ = strconv.Atoi(s)
	}
	for {
		rows := MaxRows
		if limit > 0 && limit-len(all.SearchResults) < rows {
			rows = limit - len(all.SearchResults)
		}
		q.Set("rows", strconv.Itoa(rows))
		if start >= searchAfterThreshold && q.Get("sortFields[0].name") == "id" && len(all.SearchResults) > 0 {
			lastID, err := idOf(all.SearchResults[len(all.SearchResults)-1])
			if err != nil {
				return all, err
			}
			q.Set("searchAfterValues[0]", lastID)
			q.Del("start")
		} else {
			q.Set("start", strconv.Itoa(start))
		}

		raw, err := c.Get(ctx, path, q)
		if err != nil {
			return all, err
		}
		var page Page
		if err := json.Unmarshal(raw, &page); err != nil {
			return all, fmt.Errorf("decode search page: %w", err)
		}
		all.TotalCount = page.TotalCount
		all.SearchResults = append(all.SearchResults, page.SearchResults...)
		start += len(page.SearchResults)
		if len(page.SearchResults) == 0 || len(all.SearchResults) >= page.TotalCount ||
			(limit > 0 && len(all.SearchResults) >= limit) {
			return all, nil
		}
	}
}

func idOf(obj json.RawMessage) (string, error) {
	var v struct {
		ID json.Number `json:"id"`
	}
	if err := json.Unmarshal(obj, &v); err != nil || v.ID == "" {
		return "", fmt.Errorf("searchAfterValues paging needs an id on every result")
	}
	return v.ID.String(), nil
}
