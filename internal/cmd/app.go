package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/api"
	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	appListCols    = []string{"id", "name", "platformType", "latestDisplayVersion", "appType", "platformAppId"}
	appViewCols    = []string{"id", "name", "platformType", "appType", "platformAppId", "appStoreId", "developerName", "latestAppVersionString", "dmPartitionId", "cmPartitionId", "createdAt", "modifiedAt"}
	appVersionCols = []string{"applicationVersionId", "displayVersion", "primaryVersion", "minOsVersion", "size"}
	appStoreCols   = []string{"applicationVersionId", "name", "platformType", "displayVersion", "appType", "bundleId", "installed"}
)

func newAppCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Manage apps: catalog, store imports, in-house uploads, installs, configs, reviews",
		Long: `Manage apps: the app catalog, public store imports (Google Play, iTunes),
in-house uploads, device app inventory, installs, app configurations,
custom attributes, and Apps@Work reviews.

Apps have an app id and one id per version (applicationVersionId); most
write calls take the version id, which "app view <app-id>" lists. Ivanti
applies App API operations to the default space only. Catalog lists,
store searches, reviews, and app deletion are scoped to the content space
(cmPartitionId, detected automatically); inventory, installs, and
in-house profiles to the device space.`,
	}
	categories := appGetCmd("categories", "List app categories with their app counts", "/api/v1/app/category", []string{"id", "name", "count"})
	distribution := appGetCmd("distribution <version-id|app-id>", "Show who an app version is distributed to", "/api/v1/app/version/{id}/distribution", nil)
	distribution.Long = `Show the distribution of an app version: the users, user groups, devices,
and device groups it is assigned to.`
	cmd.AddCommand(
		newAppListCmd(), newAppViewCmd(), categories, distribution, newAppFiltersCmd(),
		newAppAvailableCmd(), newAppEligibleCmd(), newAppCreateCmd(), newAppDeleteCmd(),
		newAppInventoryCmd(), newAppStoreCmd(), newAppInhouseCmd(), newAppInstallCmd(),
		newAppTaskCmd(), newAppReviewCmd(), newAppConfigCmd(), newAppAttrsCmd(),
	)
	return cmd
}

var appBundleRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// appCheckBundle validates a bundle id / package name used in a URL path.
func appCheckBundle(s string) error {
	if !appBundleRe.MatchString(s) {
		return fmt.Errorf("invalid bundle id %q", s)
	}
	return nil
}

// appGetCmd builds a read-only command for GET path; an "{id}" in path
// takes the command's single argument (numeric, or a GUID for async tasks).
func appGetCmd(use, short, path string, cols []string) *cobra.Command {
	nargs := strings.Count(path, "{id}")
	byVersion := strings.Contains(path, "/version/{id}")
	var version string
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(nargs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if nargs == 1 && !isGUID(args[0]) {
				if _, err := parseID(args[0], false); err != nil {
					return err
				}
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			p := path
			if nargs == 1 {
				id := args[0]
				if byVersion { // an app id resolves to its latest version
					if id, _, err = appVersionArg(cmd, client, id, version); err != nil {
						return err
					}
				}
				p = strings.Replace(path, "{id}", id, 1)
			}
			raw, err := client.Get(cmd.Context(), p, nil)
			if err != nil {
				return err
			}
			return render(cmd, raw, cols)
		},
	}
	if byVersion {
		appVersionFlags(cmd, &version, nil)
	}
	return cmd
}

// appBody builds a JSON object request body from --data (inline, @file, or
// - for stdin) overlaid with -f key=value fields; defaults fill keys that
// neither sets.
func appBody(cmd *cobra.Command, data string, fields []string, defaults map[string]any) (map[string]any, error) {
	body, err := jsonBody(cmd, data, fields)
	if err != nil {
		return nil, err
	}
	for k, v := range defaults {
		if _, ok := body[k]; !ok {
			body[k] = v
		}
	}
	return body, nil
}

// appSetSpace fills dmPartitionId with the current device space unless
// the body already sets it.
func appSetSpace(cmd *cobra.Command, c *api.Client, body map[string]any) error {
	if _, ok := body["dmPartitionId"]; ok {
		return nil
	}
	space, err := spaceID(cmd, c)
	if err != nil {
		return err
	}
	body["dmPartitionId"] = json.Number(space)
	return nil
}

// appDataFlags registers --data and -f for JSON-body write commands.
func appDataFlags(cmd *cobra.Command, data *string, fields *[]string) {
	cmd.Flags().StringVarP(data, "data", "d", "", `JSON request body: inline, @file, or "-" for stdin`)
	cmd.Flags().StringArrayVarP(fields, "field", "f", nil, "top-level body field key=value, JSON values keep their type (repeatable; overrides --data)")
}

// appReport prints a write result: objects (new ids, task lists, skipped
// devices) as key/value lines, anything else (true, counts) as msg.
func appReport(cmd *cobra.Command, raw json.RawMessage, msg string) error {
	if t := bytes.TrimSpace(raw); len(t) > 0 && t[0] == '{' {
		return render(cmd, raw, nil)
	}
	return report(cmd, raw, msg)
}

// appRenderApp prints an app object followed by a table of its versions,
// whose applicationVersionId most write commands need.
func appRenderApp(cmd *cobra.Command, raw json.RawMessage) error {
	if err := render(cmd, raw, appViewCols); err != nil || flagOutput == "json" {
		return err
	}
	var app struct {
		Versions []json.RawMessage `json:"versions"`
	}
	if json.Unmarshal(raw, &app) != nil || len(app.Versions) == 0 {
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout())
	return renderItems(cmd, app.Versions, appVersionCols)
}

func newAppListCmd() *cobra.Command {
	var opts searchOpts
	var appType string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List and search apps in the app catalog",
		Long: `List and search the app catalog (public and in-house apps) of the
content space. --type IN_HOUSE lists in-house apps only, --type PUBLIC
store apps only.`,
		Example: `  ivanti-neurons app list --type IN_HOUSE
  ivanti-neurons app list -q outlook --sort name`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			cm, err := contentSpaceID(cmd, client)
			if err != nil {
				return err
			}
			extra := params("cmPartitionId", cm, "excludeContainerApps", "false")
			if appType != "" {
				// observed: the type filter works in fq; facetFq is ignored
				fq := fmt.Sprintf("appType='%s'", strings.ToUpper(appType))
				if opts.fq != "" {
					fq = "(" + opts.fq + ") AND " + fq
				}
				extra.Set("fq", fq)
			}
			return runSearch(cmd, client, "/api/v1/app", &opts, extra, appListCols)
		},
	}
	opts.register(cmd)
	cmd.Flags().StringVar(&appType, "type", "", "only apps of this type: IN_HOUSE, PUBLIC, PRIVATE or WEB_APP")
	return cmd
}

func newAppViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "view <app-id>",
		Short: "Show an app and its versions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := parseID(args[0], false); err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := client.Get(cmd.Context(), "/api/v1/app/"+args[0], nil)
			if err != nil {
				return err
			}
			return appRenderApp(cmd, raw)
		},
	}
}

func newAppFiltersCmd() *cobra.Command {
	var opts searchOpts
	cmd := &cobra.Command{
		Use:   "filters",
		Short: "List app distribution filters of the device space",
		Long: `List the app distribution filters (device filters such as "iPad Only
Apps") of the device space. Use a filter id with "app install dist
--filter".`,
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
			return runSearch(cmd, client, "/api/v1/app/distributionfilters/"+space, &opts, nil, []string{"id", "name", "definition", "appCount", "mutable"})
		},
	}
	opts.register(cmd)
	return cmd
}

func newAppAvailableCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "available",
		Short: "List apps available to a user, device, or device group",
	}
	for _, a := range []struct {
		use, short, path, nav string
		guid                  bool
	}{
		{"user <account-id>", "List apps available to a user", "/api/v1/app/user/", "", true},
		{"device <device-id>", "List apps available to a device", "/api/v1/app/device/", "", true},
		{"device-group <group-id>", "List apps distributed to a device group", "/api/v1/app/rulegroup/", "true", false},
	} {
		var opts searchOpts
		sub := &cobra.Command{
			Use:   a.use,
			Short: a.short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				id, err := parseID(args[0], a.guid)
				if err != nil {
					return err
				}
				client, err := newClient()
				if err != nil {
					return err
				}
				return runSearch(cmd, client, a.path+id, &opts, params("nav", a.nav), appStoreCols)
			},
		}
		opts.register(sub)
		cmd.AddCommand(sub)
	}
	return cmd
}

func newAppEligibleCmd() *cobra.Command {
	var platform, version string
	cmd := &cobra.Command{
		Use:   "eligible <bundle-id>",
		Short: "List GUIDs of devices eligible for an app that do not have it yet",
		Long: `List the GUIDs of devices that are eligible for an app under its
distribution rules but do not have it installed, one per line (pipe them
into "app install devices"). --app-version picks a specific in-house
version.`,
		Example: `  ivanti-neurons app eligible com.mobileiron.anyware.ios
  ivanti-neurons app eligible com.example.specs --app-version 1.0.6`,
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
			q := params("dmPartitionId", space, "appId", args[0], "platformType", strings.ToUpper(platform), "appVersion", version)
			raw, err := client.Get(cmd.Context(), "/api/v1/app/eligibledevices", q)
			if err != nil {
				return err
			}
			var guids []string
			if flagOutput == "json" || json.Unmarshal(raw, &guids) != nil {
				return render(cmd, raw, nil)
			}
			if len(guids) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "No eligible devices.")
			}
			for _, g := range guids {
				fmt.Fprintln(cmd.OutOrStdout(), g)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "IOS", "platform type: IOS or ANDROID")
	cmd.Flags().StringVar(&version, "app-version", "", "specific in-house app version")
	return cmd
}

func newAppCreateCmd() *cobra.Command {
	var platform, itunesID string
	var newVersion bool
	var fields []string
	cmd := &cobra.Command{
		Use:   "create [<file>]",
		Short: "Upload an in-house app binary or import a public iOS app",
		Long: `Upload an in-house app binary (.ipa, .apk, ...) to the content space, or
import a public iOS app by its App Store id (--itunes-id).

To update an in-house app, upload the new binary with --new-version; the
result holds the new applicationVersionId. Then run "app inhouse copy
<applicationVersionId>" to carry over the previous version's screenshots,
description, and distribution.`,
		Example: `  ivanti-neurons app create PayDay.ipa --platform IOS
  ivanti-neurons app create HelpAtWork-2.ipa --platform IOS --new-version
  ivanti-neurons app create --itunes-id 422020153`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (len(args) == 1) == (itunesID != "") {
				return fmt.Errorf("pass an app file or --itunes-id")
			}
			if itunesID != "" {
				if _, err := parseID(itunesID, false); err != nil {
					return err
				}
			} else if platform == "" {
				return fmt.Errorf("--platform is required for uploads (IOS or ANDROID)")
			}
			form, err := fieldValues(fields)
			if err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			var raw json.RawMessage
			if itunesID != "" {
				form.Set("itunes-id", itunesID)
				raw, err = client.Call(cmd.Context(), http.MethodPost, "/api/v1/app", form, nil)
			} else {
				cm, cerr := contentSpaceID(cmd, client)
				if cerr != nil {
					return cerr
				}
				form.Set("platformType", strings.ToUpper(platform))
				form.Set("newVersion", strconv.FormatBool(newVersion))
				form.Set("cmPartitionId", cm)
				body := &api.Multipart{Fields: form, Files: map[string]string{"app-data": args[0]}}
				raw, err = client.Call(cmd.Context(), http.MethodPost, "/api/v1/app", nil, body)
			}
			if err != nil {
				return err
			}
			return appReport(cmd, raw, "App created")
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "", "platform of the uploaded binary: IOS or ANDROID")
	cmd.Flags().BoolVar(&newVersion, "new-version", false, "upload as a new version of an existing in-house app")
	cmd.Flags().StringVar(&itunesID, "itunes-id", "", "import a public iOS app by App Store id instead of uploading")
	cmd.Flags().StringArrayVarP(&fields, "field", "f", nil, "extra parameter key=value, e.g. overrideUrl=... (repeatable)")
	return cmd
}

func newAppDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <app-id> <version-id>",
		Short: "Delete an app version from the catalog",
		Long: `Delete one version of an app from the content space. Irreversible; asks
for confirmation (skip with --yes).`,
		Example: `  ivanti-neurons app delete 16003 17004 --yes`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, a := range args {
				if _, err := parseID(a, false); err != nil {
					return err
				}
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			if err := confirm(cmd, fmt.Sprintf("delete version %s of app %s", args[1], args[0])); err != nil {
				return err
			}
			cm, err := contentSpaceID(cmd, client)
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodDelete, "/api/v1/app/"+args[0]+"/version", params("cmPartitionId", cm, "id", args[1]), nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Deleted version %s of app %s", args[1], args[0]))
		},
	}
}

func newAppInventoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inventory",
		Short: "Show installed apps: tenant-wide app list or per device",
	}
	var listOpts searchOpts
	list := &cobra.Command{
		Use:   "list",
		Short: "List the apps found in the tenant's app inventory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			// shortcut: first page only (rows capped at 500): the result wraps
			// its page in an array, which Search cannot page; add paging when
			// tenants need more.
			rows := listOpts.limit
			if rows <= 0 || rows > api.MaxRows {
				rows = api.MaxRows
			}
			q := listOpts.values()
			q.Set("rows", strconv.Itoa(rows))
			q.Set("start", "0")
			q.Set("nav", "true")
			raw, err := client.Get(cmd.Context(), "/api/v1/applist", q)
			if err != nil {
				return err
			}
			var pages []json.RawMessage
			if json.Unmarshal(raw, &pages) == nil && len(pages) == 1 {
				raw = pages[0]
			}
			return render(cmd, raw, nil)
		},
	}
	listOpts.register(list)
	list.Flags().Lookup("limit").Usage = "maximum number of results (1-500, first page only)"

	var opts searchOpts
	var facetFq string
	var v2 bool
	device := &cobra.Command{
		Use:   "device <device-id>...",
		Short: "List the apps installed on one or more devices",
		Long: `List the apps installed on a device. With one device id this uses the
paged v1 inventory search (supports --query, --fq, --sort, --limit);
with several ids, or --v2, the v2 inventory call, which adds usage
counters and install/source types per app.

The v1 call has been rate-limited by Ivanti to 30 calls per minute per
user (newer API guides say the limit was lifted); HTTP 429 responses are
retried automatically. --facet-fq filters on install facets, e.g. to hide
system apps.`,
		Example: `  ivanti-neurons app inventory device 22115
  ivanti-neurons app inventory device 22115 --facet-fq "INSTALLTYPE != 'SYSTEM_APP' AND INSTALLTYPE != 'UPDATED_SYSTEM_APP'"
  ivanti-neurons app inventory device 22115,22116,22117 -o json`,
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
			if len(ids) == 1 && !v2 {
				space, err := spaceID(cmd, client)
				if err != nil {
					return err
				}
				cols := []string{"name", "identifier", "displayVersion", "appType", "platformType", "managed", "installedAt"}
				return runSearch(cmd, client, "/api/v1/app/inventory", &opts, params("deviceId", ids[0], "dmPartitionId", space, "facetFq", facetFq), cols)
			}
			q := params("facetFq", facetFq)
			if len(ids) == 1 {
				q.Set("deviceId", ids[0])
			} else {
				q.Set("deviceIds", strings.Join(ids, ","))
			}
			raw, err := client.Get(cmd.Context(), "/api/v2/app/inventory", q)
			if err != nil {
				return err
			}
			return appRenderInventory(cmd, raw)
		},
	}
	opts.register(device)
	device.Flags().StringVar(&facetFq, "facet-fq", "", "install facet filter, e.g. \"INSTALLTYPE != 'SYSTEM_APP'\"")
	device.Flags().BoolVar(&v2, "v2", false, "use the v2 inventory call for a single device too")
	cmd.AddCommand(list, device)
	return cmd
}

// appRenderInventory prints a v2 inventory (devices, each with an
// appInventory list) as one table row per device and app.
func appRenderInventory(cmd *cobra.Command, raw json.RawMessage) error {
	// The multi-device response is documented as {"results": [...]}, which
	// Call returns whole.
	var env struct {
		Results json.RawMessage `json:"results"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Results != nil {
		raw = env.Results
	}
	if flagOutput == "json" {
		return output.JSON(cmd.OutOrStdout(), raw)
	}
	var devices []struct {
		DeviceID     json.RawMessage              `json:"deviceId"`
		AppInventory []map[string]json.RawMessage `json:"appInventory"`
	}
	if err := json.Unmarshal(raw, &devices); err != nil {
		return fmt.Errorf("decode inventory: %w", err)
	}
	var items []json.RawMessage
	for _, d := range devices {
		for _, app := range d.AppInventory {
			if app == nil {
				continue
			}
			app["deviceId"] = d.DeviceID
			item, err := json.Marshal(app)
			if err != nil {
				return err
			}
			items = append(items, item)
		}
	}
	return renderItems(cmd, items, []string{"deviceId", "name", "identifier", "displayVersion", "sourceType", "installType", "managed", "installedAt"})
}
