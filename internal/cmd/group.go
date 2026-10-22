package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/api"
	"github.com/spf13/cobra"
)

var (
	groupCols           = []string{"id", "name", "description", "membershipStatic", "source", "applicationCount"}
	deviceGroupCols     = []string{"id", "name", "description", "memberCount", "membershipStatic", "definition"}
	deviceGroupFindCols = []string{"id", "name", "dmPartitionName", "dmPartitionId", "memberCount", "membershipStatic"}
	deviceGroupViewCols = []string{"id", "name", "description", "type", "definition", "membershipStatic", "memberCount",
		"staticMembers", "systemName", "mutable", "dmPartitionId", "dmPartitionName", "createdAt", "modifiedAt"}
)

// newGroupCmd manages user groups (API: /api/v1/group). They are tenant-wide,
// not tied to a device space.
func newGroupCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "group", Short: "Manage user groups",
		Long: `Manage user groups. To list a group's members:
  ivanti-neurons user list --fq "ACCOUNTGROUP='<group name>'"`}
	cmd.AddCommand(newGroupListCmd(), groupCreateCmd("user group", "/api/v1/group",
		`Create a user group (static by default; add users with "group members add").
--rule creates a dynamic group from an ANTLR expression instead, e.g.
"ACCOUNTGROUP EQ 'All Users'"; rules are easiest to build in the web UI
and copy from "device-group view" / "group list -o json". Requires
Tenant Admin credentials.`,
		`  ivanti-neurons group create Managers --description "Department managers"
  ivanti-neurons group create Sales --rule "ACCOUNTGROUP EQ 'Managers'"`,
		func(_ *cobra.Command, _ *api.Client, q url.Values) error {
			q.Set("source", "LOCAL")
			return nil
		}),
		newGroupMembersCmd(), newGroupCheckDeleteCmd(), newGroupDeleteCmd())
	return cmd
}

func newGroupListCmd() *cobra.Command {
	var opts searchOpts
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List user groups",
		Long: `List user groups. --query/-q matches the exact group name only
("-q sales" does not find "sales-emea").`,
		Example: `  ivanti-neurons group list
  ivanti-neurons group list -q "Android Kiosk - Staging Users"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			return runSearch(cmd, client, "/api/v1/group", &opts, nil, groupCols)
		},
	}
	opts.register(cmd)
	return cmd
}

func newGroupMembersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "members",
		Short: "Add users to a user group or replace its members",
		Long: `Change the users in a user group (PUT /api/v1/group/{id}/accounts).
"add" keeps the current members; "set" replaces them and asks for
confirmation. To put one user into several groups at once, use
"user add-to-group --group-id" (PUT /api/v1/account).`,
	}
	for _, c := range []struct {
		use, short, long, msg string
		appendMode            bool
	}{
		{"add", "Add users to a user group",
			`Add users (account ids) to a user group; current members stay. To put one
user into several groups at once, use "user add-to-group --group-id".`,
			"Added %[2]d user(s) to user group %[1]s", true},
		{"set", "Replace a user group's members (drops users not listed)",
			`Replace a user group's members with the given users (account ids); users
not listed leave the group. Asks for confirmation.`,
			"Replaced the members of user group %[1]s with %[2]d user(s)", false},
	} {
		cmd.AddCommand(&cobra.Command{
			Use:     c.use + " <group-id> <account-id>...",
			Short:   c.short,
			Long:    c.long,
			Example: "  ivanti-neurons group members " + c.use + " 11003 10004 10005",
			Args:    cobra.MinimumNArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				id, err := parseID(args[0], false)
				if err != nil {
					return err
				}
				accounts, err := parseIDs(args[1:])
				if err != nil {
					return err
				}
				if !c.appendMode {
					if err := confirm(cmd, fmt.Sprintf("replace the members of user group %s with %d user(s)", id, len(accounts))); err != nil {
						return err
					}
				}
				client, err := newClient()
				if err != nil {
					return err
				}
				q := url.Values{"accountIds": accounts, "append": {strconv.FormatBool(c.appendMode)}}
				raw, err := client.Call(cmd.Context(), http.MethodPut, "/api/v1/group/"+id+"/accounts", q, nil)
				if err != nil {
					return err
				}
				return report(cmd, raw, fmt.Sprintf(c.msg, id, len(accounts)))
			},
		})
	}
	return cmd
}

func newGroupCheckDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check-delete <id>...",
		Short: "Check whether user groups can be deleted",
		Long: `Run the API's delete viability check and list what blocks deleting the
groups. No results means they can be deleted. "group delete" runs this
check itself.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseIDs(args)
			if err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := client.Get(cmd.Context(), "/api/v1/group", url.Values{"checkDeleteViability": {"true"}, "ids": ids})
			if err != nil {
				return err
			}
			return render(cmd, raw, nil)
		},
	}
}

func newGroupDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>...",
		Short: "Delete user groups",
		Long: `Delete user groups. Runs the delete viability check first and stops if it
flags anything (see "group check-delete"), then asks for confirmation.`,
		Example: "  ivanti-neurons group delete 4677669",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseIDs(args)
			if err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			if err := deleteViability(cmd, client, "/api/v1/group", ids); err != nil {
				return err
			}
			if err := confirm(cmd, fmt.Sprintf("delete %d user group(s)", len(ids))); err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodDelete, "/api/v1/group", url.Values{"id": ids}, nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Deleted %d user group(s)", len(ids)))
		},
	}
}

// deleteViability runs the API's delete viability check (user and group
// delete) and refuses when it flags anything or cannot be read: its
// searchResults list what blocks the delete.
func deleteViability(cmd *cobra.Command, c *api.Client, path string, ids []string) error {
	raw, err := c.Get(cmd.Context(), path, url.Values{"checkDeleteViability": {"true"}, "ids": ids})
	if err != nil {
		return err
	}
	flagged, n, err := parsePage(raw)
	if err != nil {
		return fmt.Errorf("delete viability check: %w: nothing deleted", err)
	}
	if n > 0 {
		list, _ := json.Marshal(flagged)
		return fmt.Errorf("delete viability check flagged %d item(s), nothing deleted: %s", n, list)
	}
	return nil
}

// newDeviceGroupCmd manages device groups (API: rule groups,
// /api/v1/rule_group with type=DEVICE), scoped to a device space.
func newDeviceGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "device-group",
		Short: "Manage device groups",
		Long: `Manage device groups (API: rule groups). A group is dynamic (a rule, e.g.
"PRETTYMODEL EQ 'iPad'") or static (devices added by id). Groups belong to
a device space (--space).`,
	}
	cmd.AddCommand(newDeviceGroupListCmd(), newDeviceGroupViewCmd(), newDeviceGroupFindCmd(),
		groupCreateCmd("device group", "/api/v1/rule_group",
			`Create a device group in the device space (static by default; add devices
with "device-group members add"). --rule creates a dynamic group from a
rule expression such as "PRETTYMODEL EQ 'iPad'" (the definition shown by
"device-group view"). Numeric custom device attributes need a "_#" suffix
on the attribute name. Requires Tenant Admin credentials.`,
			`  ivanti-neurons device-group create Kiosks --description "Lobby kiosks"
  ivanti-neurons device-group create iPads --rule "PRETTYMODEL EQ 'iPad'"`,
			func(cmd *cobra.Command, c *api.Client, q url.Values) error {
				space, err := spaceID(cmd, c)
				q.Set("dmPartitionId", space)
				q.Set("type", "DEVICE")
				return err
			}),
		newDeviceGroupDevicesCmd(), newDeviceGroupMembersCmd())
	return cmd
}

func newDeviceGroupListCmd() *cobra.Command {
	var opts searchOpts
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List device groups in the device space",
		Long: `List device groups in the device space. Assigned policies and
configurations are left out (assignedPoliciesAndConfigs=false) to keep the
response small; "device-group view" shows them with -o json.`,
		Example: `  ivanti-neurons device-group list
  ivanti-neurons device-group list -q ipad --space Kiosks`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			space, err := spaceID(cmd, client)
			if err != nil {
				return err
			}
			return runSearch(cmd, client, "/api/v1/rule_group", &opts,
				params("dmPartitionId", space, "type", "DEVICE", "assignedPoliciesAndConfigs", "false"), deviceGroupCols)
		},
	}
	opts.register(cmd)
	return cmd
}

func newDeviceGroupViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "view <id>",
		Short: "Show a device group",
		Long: `Show a device group: its rule (definition), static members and member
count. -o json adds the rule tree (treeView) and the assigned policies and
configurations.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], false)
			if err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			space, err := spaceID(cmd, client)
			if err != nil {
				return err
			}
			raw, err := client.Get(cmd.Context(), "/api/v1/rule_group/"+id, params("dmPartitionId", space, "type", "DEVICE"))
			if err != nil {
				return err
			}
			return render(cmd, raw, deviceGroupViewCols)
		},
	}
}

func newDeviceGroupFindCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "find <name>",
		Short: "Find device groups by name across all device spaces",
		Long: `Search every device space for device groups whose name matches (the API
matches partial names too). Assigned policies and configurations are left
out of the response.`,
		Example: `  ivanti-neurons device-group find "All Devices"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			spaces, err := client.Search(cmd.Context(), "/api/v1/tenant/partition/device", nil, 0)
			if err != nil {
				return err
			}
			var ids []string
			for _, r := range spaces.SearchResults {
				var s api.Space
				if err := json.Unmarshal(r, &s); err != nil {
					return fmt.Errorf("decode device space: %w", err)
				}
				ids = append(ids, strconv.FormatInt(s.ID, 10))
			}
			// The guide documents dmPartitionIds as a comma-separated list.
			return runSearch(cmd, client, "/api/v1/rule_group/partitions", &searchOpts{query: args[0]},
				params("type", "DEVICE", "dmPartitionIds", strings.Join(ids, ","), "assignedPoliciesAndConfigs", "false"), deviceGroupFindCols)
		},
	}
}

// deviceGroupID resolves a device group argument: a numeric id, or a group
// name in the device space (exact, case-insensitive).
func deviceGroupID(cmd *cobra.Command, c *api.Client, space, arg string) (string, error) {
	if id, err := parseID(arg, false); err == nil {
		return id, nil
	}
	page, err := c.Search(cmd.Context(), "/api/v1/rule_group", params("dmPartitionId", space, "type", "DEVICE", "q", arg), 0)
	if err != nil {
		return "", err
	}
	for _, r := range page.SearchResults {
		var g struct {
			ID   json.Number `json:"id"`
			Name string      `json:"name"`
		}
		if json.Unmarshal(r, &g) == nil && strings.EqualFold(g.Name, arg) {
			return g.ID.String(), nil
		}
	}
	return "", fmt.Errorf("no device group named %q in this space: see \"ivanti-neurons device-group list\"", arg)
}

func newDeviceGroupDevicesCmd() *cobra.Command {
	var opts searchOpts
	cmd := &cobra.Command{
		Use:   "devices <group-id|name>",
		Short: "List the devices in a device group (by id or name)",
		Long: `List the devices matching a device group's rule: the group's definition
becomes the fq filter of a device search in the device space; --fq narrows
it further. "All Devices" lists the whole space. Static members are not part
of the rule: groups without a rule fail here, see their staticMembers with
"device-group view".`,
		Example: `  ivanti-neurons device-group devices 38010
  ivanti-neurons device-group devices "iOS Devices"
  ivanti-neurons device-group devices 38010 --fq "platformType='IOS'" --limit 0`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			space, err := spaceID(cmd, client)
			if err != nil {
				return err
			}
			id, err := deviceGroupID(cmd, client, space, args[0])
			if err != nil {
				return err
			}
			g, err := deviceGroupLoad(cmd, client, space, id)
			if err != nil {
				return err
			}
			fq := strings.TrimSpace(g.Definition)
			switch {
			case fq == "" && g.SystemName != "ALL_DEVICES":
				return fmt.Errorf("device group %s has no rule (static membership): see staticMembers in \"device-group view %s\"", id, id)
			case fq != "" && opts.fq != "":
				fq = "(" + fq + ") AND (" + opts.fq + ")"
			case fq == "":
				fq = opts.fq
			}
			opts.sortForLargeFetch()
			return runSearch(cmd, client, "/api/v1/device", &opts, params("dmPartitionId", space, "fq", fq), deviceCols)
		},
	}
	opts.register(cmd)
	return cmd
}

func newDeviceGroupMembersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "members",
		Short: "Change a device group's static members",
		Long:  `Change the devices added to a device group by id (static membership).`,
	}
	for _, c := range []struct {
		use, short, long, method, prompt, msg string
		minArgs                               int
	}{
		{"add <group-id> <device-id>...", "Add devices to a device group",
			`Add devices to a device group's static members. The API only sets the full
list (POST /api/v1/rule_group/{id}/devices), so this reads the current
static members and sends them plus the new ones; it refuses when the
current list cannot be read reliably.`,
			http.MethodPost, "", "Added %[2]d device(s) to device group %[1]s", 2},
		{"remove <group-id> <device-id>...", "Remove devices from a device group",
			`Remove devices from a device group's static members. Devices matching the
group's rule stay in the group. Asks for confirmation.`,
			http.MethodDelete, "remove %[2]d device(s) from device group %[1]s", "Removed %[2]d device(s) from device group %[1]s", 2},
		{"set <group-id> [device-id...]", "Replace a device group's static members (drops devices not listed)",
			`Replace a device group's static members with the given devices; without
device ids, clear them. Asks for confirmation.`,
			http.MethodPost, "replace the static members of device group %[1]s with %[2]d device(s)", "Set the static members of device group %[1]s to %[2]d device(s)", 1},
	} {
		name, _, _ := strings.Cut(c.use, " ")
		cmd.AddCommand(&cobra.Command{
			Use:     c.use,
			Short:   c.short,
			Long:    c.long,
			Example: "  ivanti-neurons device-group members " + name + " 47808 337552 337553",
			Args:    cobra.MinimumNArgs(c.minArgs),
			RunE: func(cmd *cobra.Command, args []string) error {
				id, err := parseID(args[0], false)
				if err != nil {
					return err
				}
				devices, err := parseIDs(args[1:])
				if err != nil {
					return err
				}
				if c.prompt != "" {
					if err := confirm(cmd, fmt.Sprintf(c.prompt, id, len(devices))); err != nil {
						return err
					}
				}
				client, err := newClient()
				if err != nil {
					return err
				}
				ids := devices
				if name == "add" {
					if ids, err = deviceGroupWithMembers(cmd, client, id, devices); err != nil {
						return err
					}
				}
				body := map[string]any{"deviceIds": jsonNumbers(ids)}
				raw, err := client.Call(cmd.Context(), c.method, "/api/v1/rule_group/"+id+"/devices", nil, body)
				if err != nil {
					return err
				}
				return report(cmd, raw, fmt.Sprintf(c.msg, id, len(devices)))
			},
		})
	}
	return cmd
}

// deviceGroupInfo holds the device group fields the devices and members
// commands rely on.
type deviceGroupInfo struct {
	Definition       string            `json:"definition"`
	SystemName       string            `json:"systemName"`
	MembershipStatic bool              `json:"membershipStatic"`
	MemberCount      int               `json:"memberCount"`
	StaticMembers    []json.RawMessage `json:"staticMembers"`
}

func deviceGroupLoad(cmd *cobra.Command, c *api.Client, space, id string) (*deviceGroupInfo, error) {
	raw, err := c.Get(cmd.Context(), "/api/v1/rule_group/"+id,
		params("dmPartitionId", space, "type", "DEVICE", "assignedPoliciesAndConfigs", "false"))
	if err != nil {
		return nil, err
	}
	var g deviceGroupInfo
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("decode device group: %w", err)
	}
	return &g, nil
}

// deviceGroupWithMembers returns the group's current static member ids plus
// devices. It refuses when the current list cannot be read reliably, since
// the following POST would drop every member it misses.
// shortcut: staticMembers is [] or null in every documented example; ids and
// {"id": n} objects are accepted, anything else fails. Read-modify-write, so
// a concurrent membership change can be lost.
func deviceGroupWithMembers(cmd *cobra.Command, c *api.Client, id string, devices []string) ([]string, error) {
	space, err := spaceID(cmd, c)
	if err != nil {
		return nil, err
	}
	g, err := deviceGroupLoad(cmd, c, space, id)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, m := range g.StaticMembers {
		var n json.Number
		var obj struct {
			ID json.Number `json:"id"`
		}
		switch {
		case json.Unmarshal(m, &n) == nil && n != "":
		case json.Unmarshal(m, &obj) == nil && obj.ID != "":
			n = obj.ID
		default:
			return nil, fmt.Errorf("cannot read static member %s of device group %s; use \"members set\" with the full list", m, id)
		}
		ids = append(ids, n.String())
	}
	// staticMembers null (not []) means the API did not report them at all.
	if g.MembershipStatic && g.MemberCount > len(ids) || g.StaticMembers == nil && g.MemberCount > 0 {
		return nil, fmt.Errorf("cannot read the static members of device group %s reliably (%d members, %d static listed); use \"members set\" with the full list", id, g.MemberCount, len(ids))
	}
	for _, d := range devices {
		if !slices.Contains(ids, d) {
			ids = append(ids, d)
		}
	}
	return ids, nil
}

// groupCreateCmd builds the user and device group create commands: static
// membership by default, dynamic with --rule. scope adds the fixed
// parameters of the group kind.
func groupCreateCmd(kind, path, long, example string, scope func(*cobra.Command, *api.Client, url.Values) error) *cobra.Command {
	var description, rule string
	var fields []string
	cmd := &cobra.Command{
		Use:     "create <name>",
		Short:   "Create a " + kind,
		Long:    long,
		Example: example,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			extra, err := fieldValues(fields)
			if err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			// Send membershipStatic both ways: the guide treats a missing value
			// as dynamic, the OpenAPI spec defaults it to true.
			q := params("name", args[0], "description", description, "definition", rule,
				"membershipStatic", strconv.FormatBool(rule == ""))
			if err := scope(cmd, client, q); err != nil {
				return err
			}
			for k, vs := range extra {
				q[k] = vs
			}
			raw, err := client.Call(cmd.Context(), http.MethodPost, path, q, nil)
			if err != nil {
				return err
			}
			var g struct {
				ID json.Number `json:"id"`
			}
			_ = json.Unmarshal(raw, &g)
			return report(cmd, raw, fmt.Sprintf("Created %s %q (id %s)", kind, args[0], g.ID))
		},
	}
	cmd.Flags().StringVar(&description, "description", "", "group description")
	cmd.Flags().StringVar(&rule, "rule", "", "rule expression for a dynamic group (API: definition)")
	cmd.Flags().StringArrayVarP(&fields, "field", "f", nil, "extra key=value API parameter (repeatable)")
	return cmd
}
