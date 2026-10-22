package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// TenantMetadata is the subset of GET /api/v1/metadata/tenant the CLI needs.
// The call is safe for any authenticated user, so it doubles as the
// credential check.
type TenantMetadata struct {
	TenantGUID           string `json:"tenantGuid"`
	DefaultDmPartitionID int64  `json:"defaultDmPartitionId"`
	DefaultCmPartitionID int64  `json:"defaultCmPartitionId"`
	WebAppVersion        struct {
		Version string `json:"version"`
	} `json:"webAppVersion"`
}

func (c *Client) TenantMetadata(ctx context.Context) (*TenantMetadata, error) {
	raw, err := c.Get(ctx, "/api/v1/metadata/tenant", nil)
	if err != nil {
		return nil, err
	}
	var m TenantMetadata
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode tenant metadata: %w", err)
	}
	return &m, nil
}

// Space is a device space (API name: device partition, "dmPartition").
type Space struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	DefaultPartition bool   `json:"defaultPartition"`
}

// ResolveSpace maps a device space argument (numeric id or name,
// case-insensitive) to its id.
func (c *Client) ResolveSpace(ctx context.Context, arg string) (int64, error) {
	if id, err := strconv.ParseInt(arg, 10, 64); err == nil {
		return id, nil
	}
	page, err := c.Search(ctx, "/api/v1/tenant/partition/device", nil, 0)
	if err != nil {
		return 0, err
	}
	var names []string
	for _, r := range page.SearchResults {
		var s Space
		if json.Unmarshal(r, &s) != nil {
			continue
		}
		if strings.EqualFold(s.Name, arg) {
			return s.ID, nil
		}
		names = append(names, s.Name)
	}
	return 0, fmt.Errorf("unknown device space %q (available: %s)", arg, strings.Join(names, ", "))
}
