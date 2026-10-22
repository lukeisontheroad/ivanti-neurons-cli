package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/api"
	"github.com/spf13/cobra"
)

// tenantCmds returns the tenant-wide command groups: tenant info, device
// spaces, configurations, certificates, connectors, custom attribute
// definitions, OIDC clients, and DEP.
func tenantCmds() []*cobra.Command {
	return []*cobra.Command{tenantCmd(), spaceCmd(), configCmd(), certCmd(), connectorCmd(), attributeCmd(), oidcCmd(), depCmd()}
}

// tenantRun wraps a RunE body with the output check and client setup every
// command starts with.
func tenantRun(fn func(cmd *cobra.Command, c *api.Client, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		return fn(cmd, c, args)
	}
}

func tenantCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "tenant", Short: "Show tenant metadata, analytics status, and license usage"}
	metadata := &cobra.Command{
		Use:   "metadata",
		Short: "Show tenant metadata (GUID, default spaces, version, license limits)",
		Long: `Show the tenant metadata: GUID, default device and content space ids, web
app version, white-label product, license restrictions, and Connector and
Sentry download URLs. The permission lists (cmPermissionList,
dmPermissionList) are only in -o json.`,
		Args: cobra.NoArgs,
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, _ []string) error {
			raw, err := c.Get(cmd.Context(), "/api/v1/metadata/tenant", nil)
			if err != nil {
				return err
			}
			return render(cmd, raw, []string{"tenantGuid", "defaultDmPartitionId", "defaultCmPartitionId",
				"webAppVersion.version", "whiteLabelProductName", "licenseRestrictions.totalDevicePartitionsAllowed",
				"licenseRestrictions.totalContentAllowed", "licenseRestrictions.maximumContentFileSize",
				"tenantSetupRequired", "eulaRequired", "supportTicketEnabled", "connectorISOPath", "alcorISOPath"})
		}),
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "Show the tenant's Mixpanel (usage analytics) status: true or false",
		Args:  cobra.NoArgs,
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, _ []string) error {
			raw, err := c.Get(cmd.Context(), "/api/v1/tenant/mixpanel-status", nil)
			if err != nil {
				return err
			}
			return render(cmd, raw, nil)
		}),
	}
	var fields string
	license := &cobra.Command{
		Use:   "license",
		Short: "Show license type, status, and usage against limits",
		Long: `Show the tenant's license: type, SKUs, status, term, active devices, and
usage against the soft/hard limits. --fields selects the usage terms the
API fills in (comma-separated: licenseTerm, accountTerm, contentTerm,
upgradeInfo); without it the API returns its default set.`,
		Example: `  ivanti-neurons tenant license
  ivanti-neurons tenant license --fields licenseTerm,accountTerm`,
		Args: cobra.NoArgs,
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, _ []string) error {
			raw, err := c.Get(cmd.Context(), "/api/v1/tenant/license/usage", params("requestedFields", fields))
			if err != nil {
				return err
			}
			return render(cmd, raw, []string{"displayName", "licenseType", "licenseStatus", "billingType",
				"licenseSkus", "trial", "startDate", "durationInDays", "activeDeviceCount",
				"licenseTerm", "accountTerm", "contentTerm", "upgradeInfo"})
		}),
	}
	license.Flags().StringVar(&fields, "fields", "", "usage terms to return, comma-separated (API: requestedFields)")
	cmd.AddCommand(metadata, status, license)
	return cmd
}

func spaceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "space", Short: "List device spaces"}
	var opts searchOpts
	list := &cobra.Command{
		Use:   "list",
		Short: "List device spaces (ids and names for --space)",
		Args:  cobra.NoArgs,
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, _ []string) error {
			return runSearch(cmd, c, "/api/v1/tenant/partition/device", &opts, nil,
				[]string{"id", "name", "defaultPartition", "priority", "deviceCount", "deviceGroupCount", "configurationCount", "policyCount"})
		}),
	}
	opts.register(list)
	cmd.AddCommand(list)
	return cmd
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "List device configurations, assign them to device groups, list Sentry profiles"}

	var opts searchOpts
	list := &cobra.Command{
		Use:   "list",
		Short: "List the device configurations of the device space",
		Example: `  ivanti-neurons config list
  ivanti-neurons config list -q wifi --space Kiosks --limit 0`,
		Args: cobra.NoArgs,
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, _ []string) error {
			space, err := spaceID(cmd, c)
			if err != nil {
				return err
			}
			return runSearch(cmd, c, "/api/v1/policy", &opts, params("dmPartitionId", space, "tag", "DEVICE_CONFIGURATION"),
				[]string{"id", "name", "policyType", "enabled", "priority", "installedDeviceCount", "totalDeviceCount", "violationDeviceCount", "modifiedAt"})
		}),
	}
	opts.register(list)

	var replace bool
	assign := &cobra.Command{
		Use:   "assign <config-id> <device-group-id>...",
		Short: "Assign a device configuration to device groups",
		Long: `Assign a device configuration to one or more device groups. The groups are
added to the configuration's current assignments (API: append=true).
--replace makes the given groups the configuration's only groups
(append=false), unassigning it from all others; it asks for confirmation.

Configuration ids: "config list"; device group ids: "device-group list".`,
		Example: `  ivanti-neurons config assign 35140 38002
  ivanti-neurons config assign 35140 38002,38005 --replace --yes`,
		Args: cobra.MinimumNArgs(2),
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, args []string) error {
			id, err := parseID(args[0], false)
			if err != nil {
				return err
			}
			groups, err := parseIDs(args[1:])
			if err != nil {
				return err
			}
			q := params("append", strconv.FormatBool(!replace))
			q["ruleGroupIds"] = groups
			if replace {
				if err := confirm(cmd, fmt.Sprintf("replace the device groups of configuration %s with %d group(s)", id, len(groups))); err != nil {
					return err
				}
			}
			raw, err := c.Call(cmd.Context(), http.MethodPut, "/api/v1/policy/"+id+"/rule_groups", q, nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Assigned configuration %s to %d device group(s)", id, len(groups)))
		}),
	}
	assign.Flags().BoolVar(&replace, "replace", false, "unassign the configuration from all groups not given (append=false)")

	var sentryOpts searchOpts
	sentry := &cobra.Command{
		Use:   "sentry",
		Short: "List Sentry profiles (Standalone Sentry configuration)",
		Long: `List the tenant's Sentry profiles (configurationType PROFILE). -o json
includes the full configuration: listeners, device authentication, AppTunnel
services, and the Sentry appliances (alcors) with their status.`,
		Args: cobra.NoArgs,
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, _ []string) error {
			return runSearch(cmd, c, "/api/v1/sentry/config", &sentryOpts, params("configurationType", "PROFILE"),
				[]string{"id", "name", "configurationType", "isDefaultAlcorProfile", "deletable", "modifiedAt"})
		}),
	}
	sentryOpts.register(sentry)

	cmd.AddCommand(list, assign, sentry)
	return cmd
}

func connectorCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "connector", Short: "Show Connector status"}
	var opts searchOpts
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"status"},
		Short:   "List Connectors with registration state, version, and last connection",
		Long: `List the tenant's Connectors (on-premise Connector appliances; API name
"ec"/kocab) with registration state, version, availability, and last
connection time. -o json includes each Connector's CA server capabilities.`,
		Args: cobra.NoArgs,
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, _ []string) error {
			return runSearch(cmd, c, "/api/v1/ec", &opts, nil,
				[]string{"id", "name", "registrationState", "enabled", "available", "kocabVersion", "lastConnectedAt", "loggingLevel"})
		}),
	}
	opts.register(list)
	cmd.AddCommand(list)
	return cmd
}

func attributeCmd() *cobra.Command {
	const path = "/api/v1/customattributes"
	cmd := &cobra.Command{
		Use:   "attribute",
		Short: "Manage custom attribute definitions (account, device, AAD, LDAP)",
		Long: `Manage the tenant's custom attribute definitions. Setting attribute values
on a device or user is done with the device and user commands.`,
	}
	cols := []string{"id", "name", "type", "dataType", "isPreserved", "createdAt"}

	var listType string
	list := &cobra.Command{
		Use:   "list",
		Short: "List custom attribute definitions",
		Example: `  ivanti-neurons attribute list
  ivanti-neurons attribute list --type device`,
		Args: cobra.NoArgs,
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, _ []string) error {
			page, err := c.Search(cmd.Context(), path, params("type", listType), 0)
			if err != nil {
				return err
			}
			return renderItems(cmd, page.SearchResults, cols)
		}),
	}
	list.Flags().StringVar(&listType, "type", "", "only this type: account, device, AAD, or LDAP")

	var createType, dataType string
	var createPreserved bool
	create := &cobra.Command{
		Use:   "create <name>...",
		Short: "Create custom attribute definitions",
		Long: `Create one or more custom attribute definitions of one --type (DEVICE,
ACCOUNT, or APP; app custom attributes only accept keys defined as APP).
--data-type is the value type (default TEXT). --preserved sets the
API's isPreserved flag. Prints the created definitions with their ids.`,
		Example: `  ivanti-neurons attribute create cityId costCenter --type DEVICE`,
		Args:    cobra.MinimumNArgs(1),
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, args []string) error {
			attrs := make([]map[string]any, len(args))
			for i, name := range args {
				attrs[i] = map[string]any{"name": name, "type": createType, "dataType": dataType, "isPreserved": createPreserved}
			}
			raw, err := c.Call(cmd.Context(), http.MethodPost, path, nil, map[string]any{"attrs": attrs})
			if err != nil {
				return err
			}
			return render(cmd, raw, cols)
		}),
	}
	create.Flags().StringVar(&createType, "type", "", "attribute type: DEVICE, ACCOUNT or APP (required)")
	create.Flags().StringVar(&dataType, "data-type", "TEXT", "value type (API: dataType)")
	create.Flags().BoolVar(&createPreserved, "preserved", false, "set the isPreserved flag")
	_ = create.MarkFlagRequired("type")

	var updateName, updateType string
	var updatePreserved bool
	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Rename a custom attribute definition or change its isPreserved flag",
		Long: `Update a custom attribute definition. --name (the new name when renaming)
and --type are required by the API; --preserved=true|false changes the
isPreserved flag and is left unchanged when omitted.`,
		Example: `  ivanti-neurons attribute update 86972 --name cityCode --type DEVICE`,
		Args:    cobra.ExactArgs(1),
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, args []string) error {
			id, err := parseID(args[0], false)
			if err != nil {
				return err
			}
			attr := map[string]any{"id": id, "name": updateName, "type": updateType}
			if cmd.Flags().Changed("preserved") {
				attr["isPreserved"] = updatePreserved
			}
			raw, err := c.Call(cmd.Context(), http.MethodPut, path, nil, map[string]any{"attrs": []any{attr}})
			if err != nil {
				return err
			}
			return report(cmd, raw, "Updated custom attribute "+id)
		}),
	}
	update.Flags().StringVar(&updateName, "name", "", "attribute name (required)")
	update.Flags().StringVar(&updateType, "type", "", "attribute type: DEVICE, ACCOUNT or APP (required)")
	update.Flags().BoolVar(&updatePreserved, "preserved", false, "set or clear the isPreserved flag")
	_ = update.MarkFlagRequired("name")
	_ = update.MarkFlagRequired("type")

	del := &cobra.Command{
		Use:   "delete <id>...",
		Short: "Delete custom attribute definitions",
		Long:  `Delete custom attribute definitions by id ("attribute list"). Irreversible; asks for confirmation.`,
		Args:  cobra.MinimumNArgs(1),
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, args []string) error {
			ids, err := parseIDs(args)
			if err != nil {
				return err
			}
			if err := confirm(cmd, fmt.Sprintf("delete %d custom attribute definition(s)", len(ids))); err != nil {
				return err
			}
			raw, err := c.Call(cmd.Context(), http.MethodDelete, path, nil, map[string]any{"ids": jsonNumbers(ids)})
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Deleted %d custom attribute definition(s)", len(ids)))
		}),
	}

	cmd.AddCommand(list, create, update, del)
	return cmd
}

func oidcCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "oidc",
		Short: "Create OIDC clients for OAuth2 API access and reset their secrets",
		Long: `Create OIDC clients (OAuth2 client credentials) for API access and reset
their secrets. A client acts as the account it is created for; log in with
its id and secret via "auth login --client-id ... --client-secret ...".
Needed when the API user has MFA (Basic auth then fails). Requires Tenant
Admin, System Management, or User Management.`,
	}
	cmd.AddCommand(
		oidcClientCmd("create", "Create an OIDC client (client id + secret) for an account", "/api/v1/oidc/client", ""),
		oidcClientCmd("reset-secret", "Issue a new client secret for an account's OIDC client", "/api/v1/oidc/secret-reset",
			"reset the OIDC client secret of account %s (the current secret stops working)"),
	)
	return cmd
}

// oidcClientCmd builds create/reset-secret: both POST {"accountGuid"} and
// return {clientId, clientSecret, accountGuid}. A non-empty confirmMsg
// (with %s for the GUID) asks before the call.
func oidcClientCmd(name, short, path, confirmMsg string) *cobra.Command {
	return &cobra.Command{
		Use:   name + " <account-guid>",
		Short: short,
		Long: short + `. The secret is printed once and cannot be retrieved later;
store it right away.`,
		Example: "  ivanti-neurons oidc " + name + " 1181cdec-f141-4de3-b71a-36fc26ab9a4a",
		Args:    cobra.ExactArgs(1),
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, args []string) error {
			guid := args[0]
			if !isGUID(guid) {
				return fmt.Errorf("invalid account GUID %q", guid)
			}
			if confirmMsg != "" {
				if err := confirm(cmd, fmt.Sprintf(confirmMsg, guid)); err != nil {
					return err
				}
			}
			raw, err := c.Call(cmd.Context(), http.MethodPost, path, nil, map[string]string{"accountGuid": guid})
			if err != nil {
				return err
			}
			if err := render(cmd, raw, []string{"clientId", "clientSecret", "accountGuid"}); err != nil {
				return err
			}
			var cl struct {
				ClientID string `json:"clientId"`
			}
			_ = json.Unmarshal(raw, &cl)
			fmt.Fprintf(cmd.ErrOrStderr(), "\nThe client secret is shown only once: store it now. Log in with:\n  ivanti-neurons auth login --url %s --client-id %s --client-secret <secret>\n",
				c.BaseURL(), cl.ClientID)
			return nil
		}),
	}
}

func depCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "dep", Short: "Apple DEP (Automated Device Enrollment): profiles and custom enrollment"}

	var v2 bool
	profiles := &cobra.Command{
		Use:   "profiles <virtual-server-id>",
		Short: "List the DEP profiles of a DEP virtual server",
		Long: `iOS/macOS only. List the DEP profiles of a DEP virtual server (one Apple
Business/School Manager MDM server token in the tenant). --v2 calls the
/api/v2 variant of the endpoint instead of /api/v1.`,
		Example: `  ivanti-neurons dep profiles 198
  ivanti-neurons dep profiles 198 -o json`,
		Args: cobra.ExactArgs(1),
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, args []string) error {
			id, err := parseID(args[0], false)
			if err != nil {
				return err
			}
			version := "v1"
			if v2 {
				version = "v2"
			}
			raw, err := c.Get(cmd.Context(), "/api/"+version+"/depServer/"+id+"/profiles", nil)
			if err != nil {
				return err
			}
			return render(cmd, raw, []string{"id", "profileName", "status", "default", "supervised", "mandatory", "mdmRemovable", "numOfDevices", "department"})
		}),
	}
	profiles.Flags().BoolVar(&v2, "v2", false, "use the /api/v2 endpoint")

	var user, deviceInfo, out string
	mdmProfile := &cobra.Command{
		Use:   "mdm-profile",
		Short: "Fetch the .mobileconfig enrollment profile for a custom DEP enrollment page",
		Long: `iOS 13+ and macOS 10.15+. Fetch the MDM enrollment profile (.mobileconfig)
that a custom Device Enrollment web page hands to a device during
Automated Device Enrollment. --device-info is the value of the
x-apple-aspen-deviceinfo header the device sent to the custom page; --user
binds the enrollment to that user. Writes the profile to --out (a new file;
existing files are never overwritten) or stdout.`,
		Example: `  ivanti-neurons dep mdm-profile --user jdoe@example.com --device-info "$DEVICE_INFO" --out enroll.mobileconfig`,
		Args:    cobra.NoArgs,
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, _ []string) error {
			c.Header = http.Header{"X-Apple-Aspen-Deviceinfo": {deviceInfo}}
			data, err := c.Raw(cmd.Context(), http.MethodPost, "/c/i/dep/custom.mobileconfig", params("user", user), nil)
			if err != nil {
				return err
			}
			return saveFile(cmd, data, out)
		}),
	}
	mdmProfile.Flags().StringVar(&deviceInfo, "device-info", "", "x-apple-aspen-deviceinfo header value sent by the device (required)")
	mdmProfile.Flags().StringVar(&user, "user", "", "user the device enrolls for (UID, e.g. email)")
	mdmProfile.Flags().StringVar(&out, "out", "", "write to this new file instead of stdout")
	_ = mdmProfile.MarkFlagRequired("device-info")

	cmd.AddCommand(profiles, mdmProfile)
	return cmd
}
