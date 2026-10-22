package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/api"
	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/output"
	"github.com/spf13/cobra"
)

// userCols are the default table columns for account lists.
var userCols = []string{"id", "uid", "displayName", "emailAddress", "accountSource", "groups", "createdAt"}

// userViewCols are the fields "user view" prints (-o json prints all).
var userViewCols = []string{"id", "uid", "displayName", "firstName", "lastName", "emailAddress", "accountSource",
	"accountType", "enabled", "locked", "inviteState", "groups", "deviceCount", "createdAt", "loginSuccessAt"}

// userPinCols are the columns of registration PIN results.
var userPinCols = []string{"accountId", "pin", "expiresAt", "used", "ppkgPinStatus"}

// userKeptFields are the account fields PUT /api/v1/account clears when they
// are missing from the call; "user update" resends their current values.
var userKeptFields = []string{"uid", "emailAddress", "firstName", "lastName", "displayName", "accountSource", "enabled",
	"passwordNeverExpire", "androidWorkDeviceAccountEnabled", "managedAppleId", "edipi", "gdprProfileId"}

func newUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage user accounts",
		Long: `Manage user accounts: list, create, update, delete, invite, group
membership, custom attributes, registration PINs, LDAP import, and reports.

Most commands take numeric account ids (see "user list"); "user view" and
"user update" take the UID (usually the email address). Accounts are
tenant-wide: no device space applies.`,
	}
	attrs := &cobra.Command{Use: "attrs", Short: "Set and remove custom attributes on user accounts"}
	attrs.AddCommand(userAttrsSetCmd(), userAttrsDeleteCmd(), userAttrsClearCmd())
	pin := &cobra.Command{Use: "pin", Short: "Generate and show device registration PINs"}
	pin.AddCommand(userPinGenerateCmd(), userPinViewCmd())
	report := &cobra.Command{Use: "report", Short: "List reports and download their latest output"}
	report.AddCommand(userReportListCmd(), userReportDownloadCmd())

	metadata := userShowCmd("metadata", "Show the API user's account metadata (default spaces, permissions)",
		"/api/v1/account/metadata", []string{"accountId", "displayName", "firstName", "lastName", "emailAddress",
			"defaultDmPartitionId", "defaultCmPartitionId", "groupIdAllUsers", "allDevicesRuleGroupId"})
	metadata.Long = `Show the account metadata of the authenticated API user: default device
and content space ids, the "All Users" group id, and more. -o json prints
the full object, including the granted authorities per device space.`

	cmd.AddCommand(userListCmd(), userViewCmd(), userExistsCmd(), userSearchCmd(),
		userShowCmd("me", "Show the authenticated user's own account (end-user view)", "/api/v1/user/account", nil),
		metadata, userCreateCmd(), userUpdateCmd(), userAddToGroupCmd(), userRemoveFromGroupCmd(), userDeleteCmd(),
		userInviteCmd(), userEnrollmentURLCmd(), userLDAPImportCmd(), userLDAPImportStatusCmd(),
		userRegistrationSettingsCmd(), userLoginNonceCmd(), attrs, pin, report)
	return cmd
}

// userIDsClient validates id arguments, then returns them with an API client.
func userIDsClient(args []string) ([]string, *api.Client, error) {
	ids, err := parseIDs(args)
	if err != nil {
		return nil, nil, err
	}
	client, err := newClient()
	return ids, client, err
}

func userOwnershipFlag(cmd *cobra.Command, p *string) {
	cmd.Flags().StringVar(p, "ownership", "", "device ownership for the user's devices: COMPANY, EMPLOYEE or UNKNOWN")
}

// userShowCmd builds a command without arguments that GETs path and renders it.
func userShowCmd(use, short, path string, cols []string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := client.Get(cmd.Context(), path, nil)
			if err != nil {
				return err
			}
			return render(cmd, raw, cols)
		},
	}
}

// userByUID returns the account with this UID or, for a numeric argument,
// account id (GET /api/v1/account/{id}, which the API guide omits). The fq
// filter may match more than one account, so the UID is compared exactly.
func userByUID(cmd *cobra.Command, c *api.Client, uid string) (json.RawMessage, error) {
	if id, err := parseID(uid, false); err == nil {
		raw, err := c.Get(cmd.Context(), "/api/v1/account/"+id, nil)
		if err == nil && string(bytes.TrimSpace(raw)) != "null" {
			return raw, nil
		}
		if !appNotFound(err) && err != nil {
			return nil, err
		}
	}
	raw, err := c.Get(cmd.Context(), "/api/v1/account", params("fq", "UID="+uid))
	if err != nil {
		return nil, err
	}
	var page api.Page
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, fmt.Errorf("decode account search: %w", err)
	}
	for _, r := range page.SearchResults {
		var a struct {
			UID string `json:"uid"`
		}
		if json.Unmarshal(r, &a) == nil && strings.EqualFold(a.UID, uid) {
			return r, nil
		}
	}
	return nil, fmt.Errorf("no account with UID or id %q: use the UID or ID column of \"ivanti-neurons user list\"", uid)
}

func userListCmd() *cobra.Command {
	var opts searchOpts
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List and search user accounts",
		Long: `List user accounts, optionally narrowed by a full-text --query or an --fq
filter such as "UID=jdoe@example.com". Results are sorted by id unless
--sort is given, so large tenants (10000+ accounts) page efficiently.`,
		Example: `  ivanti-neurons user list
  ivanti-neurons user list -q doe --limit 0
  ivanti-neurons user list --fq "UID=jdoe@example.com" -o json
  ivanti-neurons user list --sort displayName:asc`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			if opts.sort == "" {
				opts.sort = "id" // lets Search switch to searchAfterValues past 10000 results
			}
			return runSearch(cmd, client, "/api/v1/account", &opts, nil, userCols)
		},
	}
	opts.register(cmd)
	return cmd
}

func userViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "view <uid|account-id>",
		Short: "Show a user account by UID (usually the email) or account id",
		Example: `  ivanti-neurons user view jdoe@example.com
  ivanti-neurons user view 10542407`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			acct, err := userByUID(cmd, client, args[0])
			if err != nil {
				return err
			}
			return render(cmd, acct, userViewCols)
		},
	}
}

func userExistsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exists <uid>",
		Short: "Check whether a UID is already in use (prints true or false)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := client.Get(cmd.Context(), "/api/v1/account", params("uid", args[0]))
			// observed: the API answers 403 (not false) for an unknown UID
			var apiErr *api.Error
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
				raw, err = json.RawMessage("false"), nil
			}
			if err != nil {
				return err
			}
			return render(cmd, raw, nil)
		},
	}
}

func userSearchCmd() *cobra.Command {
	var opts searchOpts
	var data string
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Search accounts through the bulk enrollment user search (POST)",
		Long: `Search user accounts with POST /api/v1/account/search, the call the bulk
enrollment flow uses to find the user to associate with a profile. Returns
one page (up to 500 results).

Its parameters are not documented: --query, --fq, --sort and --limit are
sent as query parameters as for "user list", and --data sends a JSON body
as-is. Prefer "user list" for general searches.`,
		Example: `  ivanti-neurons user search -q jdoe`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var body any = json.RawMessage("{}") // the endpoint rejects a bodiless POST (HTTP 415)
			var err error
			if data != "" {
				if body, err = readData(cmd, data); err != nil {
					return err
				}
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			q := opts.values()
			rows := opts.limit
			if rows <= 0 || rows > api.MaxRows {
				rows = api.MaxRows
			}
			q.Set("rows", strconv.Itoa(rows))
			q.Set("start", "0")
			raw, err := client.Call(cmd.Context(), http.MethodPost, "/api/v1/account/search", q, body)
			if err != nil {
				return err
			}
			var page api.Page
			if err := json.Unmarshal(raw, &page); err != nil {
				return fmt.Errorf("decode search page: %w", err)
			}
			return renderItems(cmd, page.SearchResults, userCols)
		},
	}
	opts.register(cmd)
	cmd.Flags().StringVarP(&data, "data", "d", "", `JSON request body: inline, @file, or "-" for stdin`)
	return cmd
}

func userCreateCmd() *cobra.Command {
	var email, uid, first, last, display, password, ownership string
	var groups, fields []string
	var invite bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a local user account",
		Long: `Create a local user account. Without --password the account is created
with POST /api/v1/accounts (the user sets a password through the
invitation); with --password, POST /api/v1/account.

--uid defaults to the email address. --send-invite emails the user an
invitation to register devices. -f sets other account fields, e.g.
-f managedAppleId=jdoe@appleid.example or -f accountSource=LDAP.`,
		Example: `  ivanti-neurons user create --email jdoe@example.com --first-name John --last-name Doe --send-invite
  ivanti-neurons user create --email jdoe@example.com --display-name "John Doe" --password 'Testing!23' --group-id 11124`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			groupIDs, err := parseIDs(groups)
			if err != nil {
				return err
			}
			extra, err := fieldValues(fields)
			if err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			if uid == "" {
				uid = email
			}
			acct := params("emailAddress", email, "uid", uid, "firstName", first, "lastName", last,
				"displayName", display, "accountSource", "LOCAL")
			if len(groupIDs) > 0 {
				acct["groupIds"] = groupIDs
			}
			for k, vs := range extra {
				acct[k] = vs
			}
			// account fields go in the form body as in the guide: a password in
			// the URL would end up in access logs, --verbose and error messages
			path, body := "/api/v1/accounts", url.Values{}
			if password != "" {
				path, body = "/api/v1/account", acct
				body.Set("password", password)
				body.Set("confirmPassword", password)
			} else {
				for k, vs := range acct {
					body["accounts[0]."+k] = vs // /accounts binds a list of accounts
				}
			}
			q := params("sendInvite", strconv.FormatBool(invite), "deviceOwnershipType", ownership)
			raw, err := client.Call(cmd.Context(), http.MethodPost, path, q, body)
			if err != nil {
				return err
			}
			// POST /accounts returns no account object (observed); show
			// the created account, with its id, from a lookup instead.
			var one struct {
				ID json.Number `json:"id"`
			}
			var many []struct {
				ID json.Number `json:"id"`
			}
			hasID := (json.Unmarshal(raw, &one) == nil && one.ID != "") ||
				(json.Unmarshal(raw, &many) == nil && len(many) > 0 && many[0].ID != "")
			if !hasID {
				if raw, err = userByUID(cmd, client, acct.Get("uid")); err != nil {
					return fmt.Errorf("account created, but looking it up failed: %w", err)
				}
			}
			return render(cmd, raw, userCols)
		},
	}
	f := cmd.Flags()
	f.StringVar(&email, "email", "", "email address (required)")
	f.StringVar(&uid, "uid", "", "user id (default: the email address)")
	f.StringVar(&first, "first-name", "", "first name")
	f.StringVar(&last, "last-name", "", "last name")
	f.StringVar(&display, "display-name", "", "display name")
	f.StringVar(&password, "password", "", "initial password (omit to let the user set it via the invitation)")
	f.BoolVar(&invite, "send-invite", false, "email the user an invitation to register devices")
	f.StringArrayVar(&groups, "group-id", nil, "user group id to add the account to (repeatable)")
	f.StringArrayVarP(&fields, "field", "f", nil, "other account field as key=value (repeatable)")
	userOwnershipFlag(cmd, &ownership)
	_ = cmd.MarkFlagRequired("email")
	return cmd
}

func userUpdateCmd() *cobra.Command {
	var email, first, last, display, password string
	var enabled bool
	var fields []string
	cmd := &cobra.Command{
		Use:   "update <uid|account-id>",
		Short: "Update a user account's name, email, password, or enabled state",
		Long: `Update a user account (PUT /api/v1/account), looked up by UID or account id.

The API clears every editable field missing from the call (names, email,
enabled, managedAppleId, edipi, gdprProfileId, ...), so this command loads
the account first and resends its current values with your changes
applied. The API omits empty (null) fields from its responses, so a field
the lookup does not return is empty and stays empty. -f sets any other
field, e.g. -f managedAppleId=jdoe@appleid.example.

Group membership: "user add-to-group" and "user remove-from-group".`,
		Example: `  ivanti-neurons user update jdoe@example.com --last-name Smith --display-name "John Smith"
  ivanti-neurons user update jdoe@example.com --enabled=false
  ivanti-neurons user update jdoe@example.com -f managedAppleId=jdoe@appleid.example`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			extra, err := fieldValues(fields)
			if err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := userByUID(cmd, client, args[0])
			if err != nil {
				return err
			}
			var acct map[string]any
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			if err := dec.Decode(&acct); err != nil {
				return fmt.Errorf("decode account: %w", err)
			}
			q := url.Values{"id": {output.Format("", acct["id"])}}
			for _, k := range userKeptFields {
				if v := output.Format("", acct[k]); v != "" {
					q.Set(k, v)
				}
			}
			for _, s := range []struct{ flag, key, val string }{
				{"email", "emailAddress", email}, {"first-name", "firstName", first}, {"last-name", "lastName", last},
				{"display-name", "displayName", display}, {"enabled", "enabled", strconv.FormatBool(enabled)},
				{"password", "password", password}, {"password", "confirmPassword", password},
			} {
				if cmd.Flags().Changed(s.flag) {
					q.Set(s.key, s.val)
				}
			}
			for k, vs := range extra {
				q[k] = vs
			}
			// form body as in the guide: keeps the password out of the URL
			res, err := client.Call(cmd.Context(), http.MethodPut, "/api/v1/account", nil, q)
			if err != nil {
				return err
			}
			return report(cmd, res, fmt.Sprintf("Updated account %s (%s)", q.Get("id"), args[0]))
		},
	}
	f := cmd.Flags()
	f.StringVar(&email, "email", "", "new email address")
	f.StringVar(&first, "first-name", "", "new first name")
	f.StringVar(&last, "last-name", "", "new last name")
	f.StringVar(&display, "display-name", "", "new display name")
	f.StringVar(&password, "password", "", "new password")
	f.BoolVar(&enabled, "enabled", false, "enable (--enabled) or disable (--enabled=false) the account")
	f.StringArrayVarP(&fields, "field", "f", nil, "other account field as key=value (repeatable)")
	return cmd
}

func userAddToGroupCmd() *cobra.Command {
	var groups []string
	var replace bool
	cmd := &cobra.Command{
		Use:   "add-to-group <account-id>...",
		Short: "Add user accounts to user groups",
		Long: `Add user accounts to one or more user groups. --replace makes the given
groups the accounts' only group memberships instead of adding to them.`,
		Example: `  ivanti-neurons user add-to-group 10200 --group-id 11124 --group-id 11125
  ivanti-neurons user add-to-group 10200,10201 --group-id 11124 --replace`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, client, err := userIDsClient(args)
			if err != nil {
				return err
			}
			groupIDs, err := parseIDs(groups)
			if err != nil {
				return err
			}
			if replace {
				if err := confirm(cmd, fmt.Sprintf("replace the group memberships of %d account(s)", len(ids))); err != nil {
					return err
				}
			}
			q := url.Values{"accountIds": ids, "groupIds": groupIDs, "append": {strconv.FormatBool(!replace)}}
			raw, err := client.Call(cmd.Context(), http.MethodPut, "/api/v1/account", q, nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Assigned %d account(s) to group(s) %s", len(ids), strings.Join(groupIDs, ", ")))
		},
	}
	cmd.Flags().StringArrayVar(&groups, "group-id", nil, "user group id (repeatable)")
	cmd.Flags().BoolVar(&replace, "replace", false, "replace the accounts' current group memberships")
	_ = cmd.MarkFlagRequired("group-id")
	return cmd
}

func userRemoveFromGroupCmd() *cobra.Command {
	var groups []string
	cmd := &cobra.Command{
		Use:     "remove-from-group <account-id>...",
		Short:   "Remove user accounts from user groups",
		Example: `  ivanti-neurons user remove-from-group 10200 --group-id 11124`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, client, err := userIDsClient(args)
			if err != nil {
				return err
			}
			groupIDs, err := parseIDs(groups)
			if err != nil {
				return err
			}
			if err := confirm(cmd, fmt.Sprintf("remove %d account(s) from group(s) %s", len(ids), strings.Join(groupIDs, ", "))); err != nil {
				return err
			}
			q := url.Values{"accountIds": ids, "groupIds": groupIDs}
			raw, err := client.Call(cmd.Context(), http.MethodDelete, "/api/v1/account", q, nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Removed %d account(s) from group(s) %s", len(ids), strings.Join(groupIDs, ", ")))
		},
	}
	cmd.Flags().StringArrayVar(&groups, "group-id", nil, "user group id (repeatable)")
	_ = cmd.MarkFlagRequired("group-id")
	return cmd
}

func userDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <account-id>...",
		Short: "Delete user accounts",
		Long: `Delete user accounts. The command first runs the API's delete viability
check and stops if it flags anything, then asks for confirmation.
Irreversible.`,
		Example: `  ivanti-neurons user delete 47253017
  ivanti-neurons user delete 11193157,1516548 --yes`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, client, err := userIDsClient(args)
			if err != nil {
				return err
			}
			if err := deleteViability(cmd, client, "/api/v1/account", ids); err != nil {
				return err
			}
			list := strings.Join(ids, ", ")
			if err := confirm(cmd, fmt.Sprintf("delete %d user account(s) (%s)", len(ids), list)); err != nil {
				return err
			}
			// the guide documents comma-separated ids for this call
			raw, err := client.Call(cmd.Context(), http.MethodDelete, "/api/v1/account", params("id", strings.Join(ids, ",")), nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, "Deleted account(s) "+list)
		},
	}
}

func userInviteCmd() *cobra.Command {
	var ownership string
	cmd := &cobra.Command{
		Use:     "invite <account-id>...",
		Short:   "Email users an invitation to register their devices",
		Example: `  ivanti-neurons user invite 10552 --ownership EMPLOYEE`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, client, err := userIDsClient(args)
			if err != nil {
				return err
			}
			q := params("deviceOwnershipType", ownership)
			q["accountIds"] = ids
			raw, err := client.Call(cmd.Context(), http.MethodPost, "/api/v1/invite/enduser", q, nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Invited %d user(s)", len(ids)))
		},
	}
	userOwnershipFlag(cmd, &ownership)
	return cmd
}

func userEnrollmentURLCmd() *cobra.Command {
	var lifetime time.Duration
	cmd := &cobra.Command{
		Use:   "enrollment-url <account-id>...",
		Short: "Generate PIN-less iOS enrollment URLs for users",
		Long: `Generate an iOS enrollment URL per account. Opening it on the user's
iPhone or iPad downloads the enrollment profile without asking for a PIN,
so treat the URLs as credentials until they expire (--lifetime).`,
		Example: `  ivanti-neurons user enrollment-url 10022 10023 --lifetime 72h`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, client, err := userIDsClient(args)
			if err != nil {
				return err
			}
			body := map[string]any{"accountIds": jsonNumbers(ids), "tokenLifetime": lifetime.Milliseconds()}
			raw, err := client.Call(cmd.Context(), http.MethodPost, "/api/v1/tenant/enrollment/url", nil, body)
			if err != nil {
				return err
			}
			// a table, not key/value lines: those truncate the long URLs
			var urls map[string]string
			if flagOutput == "json" || json.Unmarshal(raw, &urls) != nil {
				return render(cmd, raw, nil)
			}
			var rows [][]string
			for _, id := range ids {
				if u, ok := urls[id]; ok {
					rows = append(rows, []string{id, u})
				}
			}
			output.Table(cmd.OutOrStdout(), []string{"accountId", "url"}, rows)
			return nil
		},
	}
	cmd.Flags().DurationVar(&lifetime, "lifetime", 7*24*time.Hour, "how long the URLs stay valid")
	return cmd
}

func userLDAPImportCmd() *cobra.Command {
	var server, ownership string
	var invite bool
	cmd := &cobra.Command{
		Use:   "ldap-import <uid-or-email>",
		Short: "Import a user from LDAP",
		Long: `Request the import of an LDAP user as an account. The import runs
asynchronously: the command prints a request id to check with
"user ldap-import-status" (Ivanti suggests checking every 15 seconds for up
to 90 seconds). --ldap-server is required when the tenant has more than one
LDAP server.`,
		Example: `  ivanti-neurons user ldap-import jdoe@example.com --ldap-server ldap-2k --send-invite`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			q := params("uidOrEmail", args[0], "ldapServerName", server,
				"sendInvite", strconv.FormatBool(invite), "deviceOwnershipType", ownership)
			raw, err := client.Call(cmd.Context(), http.MethodPost, "/api/v1/ldap/import/user", q, nil)
			if err != nil {
				return err
			}
			id := string(bytes.TrimSpace(raw))
			return report(cmd, raw, fmt.Sprintf("Import request %s submitted; check it with: ivanti-neurons user ldap-import-status %s", id, id))
		},
	}
	cmd.Flags().StringVar(&server, "ldap-server", "", "LDAP server name (default: the tenant's only LDAP server)")
	cmd.Flags().BoolVar(&invite, "send-invite", false, "email the user an invitation to register devices")
	userOwnershipFlag(cmd, &ownership)
	return cmd
}

func userLDAPImportStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ldap-import-status <request-id>",
		Short: "Show the status of an LDAP user import (IN_PROGRESS, COMPLETED, FAILED)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, client, err := userIDsClient(args)
			if err != nil {
				return err
			}
			raw, err := client.Get(cmd.Context(), "/api/v1/ldap/import/user/status", params("requestId", ids[0]))
			if err != nil {
				return err
			}
			return render(cmd, raw, []string{"requestId", "status", "errorCode", "errorMessage"})
		},
	}
}

func userRegistrationSettingsCmd() *cobra.Command {
	var mode, pinType string
	var pinLength int
	var pinLifetime time.Duration
	var pinReset bool
	var fields []string
	cmd := &cobra.Command{
		Use:   "registration-settings",
		Short: "Set how users authenticate when registering devices (password, PIN, ...)",
		Long: `Set the tenant-wide device registration authentication mode: PASSWORD,
PIN, PASSWORD_AND_PIN, OAUTH, SAML or SAML_AND_PIN. Modes with a PIN also
send the PIN settings. "user pin generate" only works in a PIN mode.

The call sends a whole policy object; settings it omits (e.g.
appleEnrollmentType) may be reset, so pass the ones you rely on with -f
(JSON values keep their type).`,
		Example: `  ivanti-neurons user registration-settings --mode PIN --pin-length 6 --pin-lifetime 72h
  ivanti-neurons user registration-settings --mode PASSWORD -f appleEnrollmentType=USER_ENROLLMENT`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			extra, err := jsonBody(cmd, "", fields)
			if err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			mode = strings.ToUpper(mode)
			// fixed fields as the admin portal sends them (guide example)
			body := map[string]any{
				"systemName": "registrationAuthUserPolicy", "name": "Device Registration Authentication Type",
				"policyType": "REGISTRATION_AUTH", "priority": math.MaxInt32, "enabled": true,
				"policyDataMutable": true, "distributionMutable": false, "priorityMutable": false, "deletable": false,
				"registrationAuthMode": mode,
			}
			if strings.Contains(mode, "PIN") {
				body["pinType"], body["pinLength"] = strings.ToUpper(pinType), pinLength
				body["pinLifetimeMillis"], body["pinResetByUser"] = pinLifetime.Milliseconds(), pinReset
			}
			for k, v := range extra {
				body[k] = v
			}
			raw, err := client.Call(cmd.Context(), http.MethodPut, "/api/v1/userpolicy", params("policyType", "REGISTRATION_AUTH"), body)
			if err != nil {
				return err
			}
			return report(cmd, raw, "Device registration mode set to "+mode)
		},
	}
	f := cmd.Flags()
	f.StringVar(&mode, "mode", "", "PASSWORD, PIN, PASSWORD_AND_PIN, OAUTH, SAML or SAML_AND_PIN (required)")
	f.StringVar(&pinType, "pin-type", "NUMERIC", "PIN type: NUMERIC or ALPHA_NUMERIC")
	f.IntVar(&pinLength, "pin-length", 4, "PIN length")
	f.DurationVar(&pinLifetime, "pin-lifetime", 7*24*time.Hour, "how long a PIN stays valid")
	f.BoolVar(&pinReset, "pin-reset-by-user", true, "let users reset their PIN")
	f.StringArrayVarP(&fields, "field", "f", nil, "other policy field as key=value (repeatable)")
	_ = cmd.MarkFlagRequired("mode")
	return cmd
}

func userLoginNonceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login-nonce",
		Short: "Fetch a one-time DeepLink login nonce for the API user",
		Long: `Fetch a one-time login nonce for the authenticated account, used for
DeepLink login into the admin portal. The nonce is a single-use credential:
do not log or share it. Requires tenant admin, System Management or User
Management rights.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodPost, "/api/v1/account/login-nonce", nil, nil)
			if err != nil {
				return err
			}
			var res struct {
				LoginNonce string `json:"loginNonce"`
			}
			if flagOutput == "json" || json.Unmarshal(raw, &res) != nil || res.LoginNonce == "" {
				return render(cmd, raw, nil)
			}
			fmt.Fprintln(cmd.OutOrStdout(), res.LoginNonce) // in full: key/value output truncates
			return nil
		},
	}
}

func userAttrsSetCmd() *cobra.Command {
	var overwrite bool
	cmd := &cobra.Command{
		Use:   "set <account-id>[,<account-id>...] <key=value>...",
		Short: "Set custom attribute values on user accounts",
		Long: `Set custom attributes on one or more accounts; repeat a key for several
values. The attributes must already be defined as account custom attributes
in the tenant. --overwrite sends the API's forceOverwrite flag to replace
the existing values instead of merging.`,
		Example: `  ivanti-neurons user attrs set 739375 testUserAttribute=forDocs
  ivanti-neurons user attrs set 19850 testUserAttribute=forCheck --overwrite
  ivanti-neurons user attrs set 10022,10023 costCenter=4711 costCenter=4712`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, client, err := userIDsClient(args[:1])
			if err != nil {
				return err
			}
			attrs, err := parseAttrs(args[1:])
			if err != nil {
				return err
			}
			path, body := "/api/v1/account/"+ids[0]+"/customattributes", map[string]any{"attrs": attrs}
			if len(ids) > 1 {
				// the bulk call nests the map one level deeper: {"attrs": {"attrs": {...}}}
				path, body = "/api/v1/account/customattributes", map[string]any{"ids": jsonNumbers(ids), "attrs": body}
			}
			if overwrite {
				body["forceOverwrite"] = true
			}
			raw, err := client.Call(cmd.Context(), http.MethodPatch, path, nil, body)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Set %d custom attribute(s) on %d account(s)", len(attrs), len(ids)))
		},
	}
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace existing values instead of merging (forceOverwrite)")
	return cmd
}

func userAttrsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <account-id>[,<account-id>...] <key>...",
		Short:   "Remove the named custom attributes from user accounts",
		Example: `  ivanti-neurons user attrs delete 42299037 test_abc test`,
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, client, err := userIDsClient(args[:1])
			if err != nil {
				return err
			}
			keys := args[1:]
			if err := confirm(cmd, fmt.Sprintf("remove custom attribute(s) %s from %d account(s)", strings.Join(keys, ", "), len(ids))); err != nil {
				return err
			}
			// ids as strings, as in the guide's example
			body := map[string]any{"ids": ids, "attrKeys": keys}
			raw, err := client.Call(cmd.Context(), http.MethodDelete, "/api/v1/account/customattributes", nil, body)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Removed %d custom attribute(s) from %d account(s)", len(keys), len(ids)))
		},
	}
}

func userAttrsClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "clear <account-id>",
		Short:   "Remove all custom attributes from a user account",
		Example: `  ivanti-neurons user attrs clear 42299037`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, client, err := userIDsClient(args)
			if err != nil {
				return err
			}
			if err := confirm(cmd, "remove all custom attributes from account "+ids[0]); err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodDelete, "/api/v1/account/"+ids[0]+"/customattributes", nil, nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, "Removed all custom attributes from account "+ids[0])
		},
	}
}

func userPinGenerateCmd() *cobra.Command {
	var ownership string
	cmd := &cobra.Command{
		Use:   "generate <account-id>...",
		Short: "Generate device registration PINs for users and print them",
		Long: `Generate a device registration PIN per account and print it. Fails unless
the registration mode includes a PIN (see "user registration-settings").`,
		Example: `  ivanti-neurons user pin generate 38942135 38942136 --ownership COMPANY`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, client, err := userIDsClient(args)
			if err != nil {
				return err
			}
			q := params("deviceOwnershipType", ownership)
			// the guide sends accountIds as a form field, the OpenAPI spec a JSON {"ids"} body: send both
			q["accountIds"] = ids
			raw, err := client.Call(cmd.Context(), http.MethodPost, "/api/v1/account/regpin", q, map[string]any{"ids": jsonNumbers(ids)})
			if err != nil {
				return err
			}
			return render(cmd, raw, userPinCols)
		},
	}
	userOwnershipFlag(cmd, &ownership)
	return cmd
}

func userPinViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "view <account-id>",
		Short: "Show a user's current device registration PIN",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, client, err := userIDsClient(args)
			if err != nil {
				return err
			}
			raw, err := client.Get(cmd.Context(), "/api/v1/account/"+ids[0]+"/registrationPin", nil)
			if err != nil {
				return err
			}
			return render(cmd, raw, userPinCols)
		},
	}
}

func userReportListCmd() *cobra.Command {
	var opts searchOpts
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List reports with their schedule and output formats",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			return runSearch(cmd, client, "/api/v1/report/flex/job/summary", &opts, nil,
				[]string{"name", "status", "reportJobRunStatus", "outputFormats", "startDate", "nextRunDate"})
		},
	}
	cmd.Flags().IntVar(&opts.limit, "limit", 50, "maximum number of results (0 = all)")
	return cmd
}

func userReportDownloadCmd() *cobra.Command {
	var format, out string
	cmd := &cobra.Command{
		Use:   "download <report-name>",
		Short: "Download the latest output of a report (CSV or PDF)",
		Long: `Download the latest generated output of a report (names: "user report
list"). Writes to --out (mode 0600: reports hold personal data), or to
stdout without it.`,
		Example: `  ivanti-neurons user report download Users-1 --out users.csv
  ivanti-neurons user report download Users-1 --format PDF --out users.pdf`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			data, err := client.Raw(cmd.Context(), http.MethodGet, "/api/v1/report/flex/job/output/latest/download",
				params("reportName", args[0], "fileType", strings.ToUpper(format)), nil)
			if err != nil {
				return err
			}
			return saveFile(cmd, data, out)
		},
	}
	cmd.Flags().StringVar(&format, "format", "CSV", "file type: CSV or PDF")
	cmd.Flags().StringVar(&out, "out", "", "output file (default: stdout)")
	return cmd
}
