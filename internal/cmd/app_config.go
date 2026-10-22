package cmd

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/api"
	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/output"
	"github.com/spf13/cobra"
)

func newAppConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "List, read, and change the configurations of an app version",
		Long: `Configurations belong to an app version and come in types: INSTALL,
PROMOTION, ANDROID_FOR_WORK (the Android managed configuration), APP_CONNECT,
APP_TUNNEL, DELEGATED_PERMISSIONS, GOOGLE_PLAY_RELEASE, and iOS-specific ones.
The API keeps one list per type; "config types <version>" shows which types a
version has, "config list" reads them all.

Every command takes an app version id (APPLICATIONVERSIONID in "app view
<app-id>") or an app id, which resolves to the app's latest version (--version
picks another: a version id or display version). The App API works on the
default device space only.

Typical flow for an Android managed configuration:
  ivanti-neurons app config list 16005                   # find the ANDROID_FOR_WORK config id
  ivanti-neurons app config get 16005 276183            # keys the devices get
  ivanti-neurons app config set 16005 276183 HomepageLocation=https://intranet.example`,
	}
	cmd.AddCommand(appConfigListCmd(), appConfigGetCmd(), appConfigSetCmd(), appConfigTypesCmd(),
		appConfigCreateCmd(), appConfigUpdateCmd())
	return cmd
}

// appVersionFlags registers --version (pick a version of an app id) and,
// with typeUsage, --type.
func appVersionFlags(cmd *cobra.Command, version, configType *string, typeUsage ...string) {
	cmd.Flags().StringVar(version, "version", "", "when the argument is an app id: the version to use (version id or display version; default: latest)")
	if configType != nil {
		usage := "only this configuration type, e.g. ANDROID_FOR_WORK (default: all types)"
		if len(typeUsage) > 0 {
			usage = typeUsage[0]
		}
		cmd.Flags().StringVar(configType, "type", "", usage)
	}
}

// appVersionArg resolves an app version argument: an app version id, or an
// app id (its latest version, or the one version names). Passing an app id
// where a version id belongs is the common mistake (the API answers 403), so
// it is resolved instead of failed. Returns the version id and its
// configuration types.
func appVersionArg(cmd *cobra.Command, c *api.Client, arg, version string) (string, []string, error) {
	id, err := parseID(arg, false)
	if err != nil {
		return "", nil, err
	}
	if version == "" {
		if types, err := appConfigTypes(cmd, c, id); err == nil {
			return id, types, nil
		} else if !appNotFound(err) {
			return "", nil, err
		}
	}
	raw, err := c.Get(cmd.Context(), "/api/v1/app/"+id, nil)
	if err != nil {
		if appNotFound(err) {
			return "", nil, fmt.Errorf("%s is neither an app version id nor an app id: version ids are the APPLICATIONVERSIONID column of \"ivanti-neurons app view <app-id>\", app ids the ID column of \"ivanti-neurons app list\"", id)
		}
		return "", nil, err
	}
	var app struct {
		Name     string `json:"name"`
		Latest   string `json:"latestAppVersionString"`
		Versions []struct {
			ID      json.Number `json:"applicationVersionId"`
			Display string      `json:"displayVersion"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(raw, &app); err != nil || len(app.Versions) == 0 {
		return "", nil, fmt.Errorf("app %s lists no versions", id)
	}
	pick := -1
	for i, v := range app.Versions {
		switch {
		case version != "" && (v.ID.String() == version || v.Display == version):
			pick = i
		case version == "" && pick < 0 && v.Display == app.Latest:
			pick = i
		}
	}
	if version == "" && pick < 0 {
		pick = 0
	}
	if pick < 0 {
		var have []string
		for _, v := range app.Versions {
			have = append(have, v.ID.String()+" ("+v.Display+")")
		}
		return "", nil, fmt.Errorf("app %s has no version %q (versions: %s)", id, version, strings.Join(have, ", "))
	}
	v := app.Versions[pick]
	fmt.Fprintf(cmd.ErrOrStderr(), "Using version %s (%s) of app %s %q\n", v.ID, v.Display, id, app.Name)
	types, err := appConfigTypes(cmd, c, v.ID.String())
	return v.ID.String(), types, err
}

// appNotFound reports the API's answers to an id of the wrong kind.
func appNotFound(err error) bool {
	var apiErr *api.Error
	return errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusForbidden || apiErr.StatusCode == http.StatusNotFound)
}

// appConfigTypes lists the configuration types of an app version (the keys
// of GET .../config/types), INSTALL first.
func appConfigTypes(cmd *cobra.Command, c *api.Client, vid string) ([]string, error) {
	raw, err := c.Get(cmd.Context(), "/api/v1/app/version/"+vid+"/config/types", nil)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode configuration types: %w", err)
	}
	types := slices.Sorted(maps.Keys(m))
	if i := slices.Index(types, "INSTALL"); i > 0 {
		types = append([]string{"INSTALL"}, slices.Delete(types, i, i+1)...)
	}
	return types, nil
}

// appConfigs reads the configurations of the given types (one list per
// type) and merges them.
func appConfigs(cmd *cobra.Command, c *api.Client, vid string, types []string) ([]json.RawMessage, error) {
	var all []json.RawMessage
	for _, t := range types {
		page, err := c.Search(cmd.Context(), "/api/v1/app/version/"+vid+"/config", params("type", t), 0)
		if err != nil {
			return nil, fmt.Errorf("list %s configurations: %w", t, err)
		}
		all = append(all, page.SearchResults...)
	}
	return all, nil
}

// appTypesFilter narrows the version's types to --type.
func appTypesFilter(types []string, only string) ([]string, error) {
	if only == "" {
		return types, nil
	}
	only = strings.ToUpper(only)
	if !slices.Contains(types, only) {
		return nil, fmt.Errorf("this app version has no configuration type %s (types: %s)", only, strings.Join(types, ", "))
	}
	return []string{only}, nil
}

// appConfig reads one configuration with its assignments (groupIds,
// accountIds, deviceIds, ruleGroupIds) from GET .../config/{id}. That call is
// missing from the API guide; the documented per-type lists lack the
// assignments, so a tenant without it falls back to them (read-only use).
func appConfig(cmd *cobra.Command, c *api.Client, vid, cfgID string, types []string) (map[string]any, error) {
	// an id that is no configuration of this version answers "result": null
	if raw, err := c.Get(cmd.Context(), "/api/v1/app/version/"+vid+"/config/"+cfgID, nil); err == nil && string(bytes.TrimSpace(raw)) != "null" {
		var cfg map[string]any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&cfg); err != nil {
			return nil, fmt.Errorf("decode configuration: %w", err)
		}
		return cfg, nil
	} else if err != nil && !appNotFound(err) {
		return nil, err
	}
	for _, t := range types {
		configs, err := appConfigs(cmd, c, vid, []string{t})
		if err != nil {
			return nil, err
		}
		for _, raw := range configs {
			var cfg map[string]any
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			if dec.Decode(&cfg) == nil && output.Format("", cfg["id"]) == cfgID {
				return cfg, nil
			}
		}
	}
	return nil, fmt.Errorf("app version %s has no configuration %s (types searched: %s); see \"ivanti-neurons app config list %s\"",
		vid, cfgID, strings.Join(types, ", "), vid)
}

// appAssignmentKeys are the distribution fields of a configuration: its PUT
// replaces them, so a body without them unassigns the configuration
// (observed).
var appAssignmentKeys = []string{"groupIds", "accountIds", "deviceIds", "ruleGroupIds"}

// appConfigPutBody builds the PUT body from a configuration: the fields the
// call takes, with the current assignments, and settings.configuration.uuid
// cleared (observed: "must be null"). It refuses when the assignments are
// unknown rather than drop them.
func appConfigPutBody(cfg map[string]any) (map[string]any, error) {
	body := map[string]any{}
	for _, k := range append([]string{"id", "applicationVersionId", "name", "description", "priority", "type",
		"configVariables", "defaultConfig", "settings"}, appAssignmentKeys...) {
		if v, ok := cfg[k]; ok {
			body[k] = v
		}
	}
	for _, k := range appAssignmentKeys {
		if body[k] == nil {
			return nil, fmt.Errorf("cannot read the assignments (%s) of configuration %v: refusing to write, the API would drop them", k, cfg["id"])
		}
	}
	if settings, ok := body["settings"].(map[string]any); ok {
		if conf, ok := settings["configuration"].(map[string]any); ok {
			conf["uuid"] = nil
		}
	}
	return body, nil
}

// appRestrictions returns the managed-configuration keys of a configuration
// (settings.configuration.restrictions), or nil when it has none.
func appRestrictions(cfg map[string]any) map[string]any {
	settings, _ := cfg["settings"].(map[string]any)
	conf, _ := settings["configuration"].(map[string]any)
	r, _ := conf["restrictions"].(map[string]any)
	return r
}

func appConfigListCmd() *cobra.Command {
	var version, configType string
	cmd := &cobra.Command{
		Use:   "list <version-id|app-id>",
		Short: "List an app version's configurations of every type",
		Example: `  ivanti-neurons app config list 121646
  ivanti-neurons app config list 16005 --type ANDROID_FOR_WORK   # app id: latest version`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			vid, types, err := appVersionArg(cmd, client, args[0], version)
			if err != nil {
				return err
			}
			if types, err = appTypesFilter(types, configType); err != nil {
				return err
			}
			configs, err := appConfigs(cmd, client, vid, types)
			if err != nil {
				return err
			}
			return renderItems(cmd, configs, []string{"id", "type", "name", "priority", "defaultConfig", "modifiedAt"})
		},
	}
	appVersionFlags(cmd, &version, &configType)
	return cmd
}

func appConfigGetCmd() *cobra.Command {
	var version, configType string
	var all bool
	cmd := &cobra.Command{
		Use:   "get <version-id|app-id> <config-id>",
		Short: "Show one configuration; for a managed configuration, the keys it pushes",
		Long: `Show one configuration with its assignments (groupIds, accountIds, ...).
For an Android managed configuration (ANDROID_FOR_WORK) it lists the keys
that are enabled, i.e. pushed to the devices, with their values (what the
app gets), followed by the pushed app permission grants and app settings
(autoLaunch, blockUninstall, ...). --all lists every key the app supports,
with an ENABLED column. Other types print their settings. -o json prints the
full configuration object.`,
		Example: `  ivanti-neurons app config get 16005 276183
  ivanti-neurons app config get 121646 276183 --all
  ivanti-neurons app config get 121646 276183 -o json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgID, err := parseID(args[1], false)
			if err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			vid, types, err := appVersionArg(cmd, client, args[0], version)
			if err != nil {
				return err
			}
			if types, err = appTypesFilter(types, configType); err != nil {
				return err
			}
			cfg, err := appConfig(cmd, client, vid, cfgID, types)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if flagOutput == "json" {
				return output.JSON(out, cfg)
			}
			head, _ := json.Marshal(cfg)
			output.KV(out, head, []string{"id", "type", "name", "priority", "defaultConfig", "modifiedAt", "groupIds", "accountIds", "deviceIds", "ruleGroupIds"})
			fmt.Fprintln(out)
			r := appRestrictions(cfg)
			if r == nil {
				settings, _ := json.Marshal(cfg["settings"])
				output.KV(out, settings, nil)
				return nil
			}
			headers := []string{"key", "type", "title", "value"} // value last: it can be long
			if all {
				headers = []string{"key", "enabled", "type", "title", "value"}
			}
			var rows [][]string
			for _, k := range slices.Sorted(maps.Keys(r)) {
				e, _ := r[k].(map[string]any)
				on := e["enabled"] == true
				if !on && !all {
					continue
				}
				title := output.Truncate(output.Format("", e["title"]), 45)
				if on && e["title"] == nil && e["value"] == nil {
					title = "(enabled, no value; not in the app's current schema)"
				}
				row := []string{k, output.Format("", e["type"]), title, appRestrictionValue(e)}
				if all {
					row = slices.Insert(row, 1, fmt.Sprint(on))
				}
				rows = append(rows, row)
			}
			if len(rows) == 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "No keys enabled (%d available: --all lists them).\n", len(r))
			} else {
				output.Table(out, headers, rows)
			}
			// app permission grants and app behaviour are pushed too
			settings, _ := cfg["settings"].(map[string]any)
			conf, _ := settings["configuration"].(map[string]any)
			extra, _ := json.Marshal(conf)
			fmt.Fprintln(out)
			output.KV(out, extra, []string{"androidWorkAppPermissionPolicies", "autoLaunch", "blockUninstall", "blockWidget", "hideRestrictions"})
			return nil
		},
	}
	appVersionFlags(cmd, &version, &configType, "type of <config-id>, to skip detecting it (e.g. ANDROID_FOR_WORK)")
	cmd.Flags().BoolVar(&all, "all", false, "list every key, enabled or not")
	return cmd
}

// appRestrictionValue renders a restriction's value; a CHOICE value gets
// its label.
func appRestrictionValue(e map[string]any) string {
	if e["value"] == nil {
		return "(null)"
	}
	v := output.Format("", e["value"])
	values, _ := e["entryValue"].([]any)
	labels, _ := e["entry"].([]any)
	for i, ev := range values {
		if output.Format("", ev) == v && i < len(labels) {
			return v + " (" + output.Truncate(output.Format("", labels[i]), 40) + ")"
		}
	}
	return v
}

func appConfigSetCmd() *cobra.Command {
	var version, configType string
	cmd := &cobra.Command{
		Use:   "set <version-id|app-id> <config-id> <key=value>...",
		Short: "Change keys of a managed configuration (key= stops pushing a key)",
		Long: `Change single keys of an Android managed configuration (ANDROID_FOR_WORK)
without hand-editing its JSON. key=value sets the value and enables the key
(it is pushed to the devices); key= disables it (the value is kept, not
pushed). Values are checked against the key's type: BOOL true|false, INTEGER,
STRING, CHOICE (one of the allowed values or labels; "app config get --all"
lists them). Unknown keys are refused.

The configuration is read, changed, and written back whole with its
assignments (the API replaces both); the changes are printed. Nothing is
sent when nothing changes. Other key
types (MULTISELECT, BUNDLE) and other configuration types: "app config update"
with --data.`,
		Example: `  ivanti-neurons app config set 16005 276183 HomepageLocation=https://intranet.example
  ivanti-neurons app config set 16005 276183 DisableAuthNegotiateCnameLookup=true ProxySettings=`,
		Args: cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgID, err := parseID(args[1], false)
			if err != nil {
				return err
			}
			changes := map[string]string{}
			for _, a := range args[2:] {
				k, v, ok := strings.Cut(a, "=")
				if !ok || k == "" {
					return fmt.Errorf("invalid argument %q (want key=value, or key= to disable)", a)
				}
				changes[k] = v
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			vid, types, err := appVersionArg(cmd, client, args[0], version)
			if err != nil {
				return err
			}
			if types, err = appTypesFilter(types, configType); err != nil {
				return err
			}
			cfg, err := appConfig(cmd, client, vid, cfgID, types)
			if err != nil {
				return err
			}
			r := appRestrictions(cfg)
			if r == nil {
				return fmt.Errorf("configuration %s (type %v) has no managed-configuration keys: use \"ivanti-neurons app config update\" with --data", cfgID, cfg["type"])
			}
			var diff []string
			for _, k := range slices.Sorted(maps.Keys(changes)) {
				line, err := appRestrictionSet(r, k, changes[k])
				if err != nil {
					return fmt.Errorf("configuration %s: %w", cfgID, err)
				}
				if line != "" {
					diff = append(diff, line)
				}
			}
			out := cmd.OutOrStdout()
			if len(diff) == 0 {
				fmt.Fprintln(out, "No changes; nothing sent.")
				return nil
			}
			body, err := appConfigPutBody(cfg)
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodPut, "/api/v1/app/version/"+vid+"/config/"+cfgID, nil, body)
			if err != nil {
				return err
			}
			if flagOutput == "json" {
				return output.JSON(out, raw)
			}
			fmt.Fprintf(out, "Updated configuration %s of app version %s:\n  %s\n", cfgID, vid, strings.Join(diff, "\n  "))
			return nil
		},
	}
	appVersionFlags(cmd, &version, &configType, "type of <config-id>, to skip detecting it (e.g. ANDROID_FOR_WORK)")
	return cmd
}

// appRestrictionSet applies key=value to a restrictions map and returns the
// change as a diff line ("" when nothing changed).
func appRestrictionSet(r map[string]any, key, value string) (string, error) {
	e, ok := r[key].(map[string]any)
	if !ok {
		return "", fmt.Errorf("unknown key %q%s", key, appSimilarKeys(r, key))
	}
	was, wasOn := appRestrictionValue(e), e["enabled"] == true
	if value == "" {
		if !wasOn {
			return "", nil
		}
		e["enabled"] = false
		return fmt.Sprintf("- %s (no longer pushed; value %s kept)", key, was), nil
	}
	var v any = value
	switch t := output.Format("", e["type"]); t {
	case "BOOL":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return "", fmt.Errorf("%s is BOOL: want true or false, got %q", key, value)
		}
		v = b
	case "INTEGER":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return "", fmt.Errorf("%s is INTEGER: got %q", key, value)
		}
		v = n
	case "CHOICE":
		values, _ := e["entryValue"].([]any)
		labels, _ := e["entry"].([]any)
		if len(values) > 0 {
			match := ""
			var allowed []string
			for i, ev := range values {
				evs := output.Format("", ev)
				allowed = append(allowed, evs)
				if evs == value || (i < len(labels) && strings.EqualFold(output.Format("", labels[i]), value)) {
					match = evs
				}
			}
			if match == "" {
				return "", fmt.Errorf("%s is CHOICE: want one of %s (labels: \"app config get --all\")", key, strings.Join(allowed, ", "))
			}
			v = match
		}
	case "STRING":
	default:
		return "", fmt.Errorf("%s is %s: not supported by set yet, use \"app config update\" with --data", key, t)
	}
	if wasOn && output.Format("", e["value"]) == output.Format("", v) {
		return "", nil
	}
	e["value"], e["enabled"] = v, true
	now := appRestrictionValue(e)
	if !wasOn {
		return fmt.Sprintf("+ %s = %s (now pushed)", key, now), nil
	}
	return fmt.Sprintf("~ %s: %s -> %s", key, was, now), nil
}

// appSimilarKeys suggests keys for a mistyped one: those sharing a
// case-insensitive substring, else all keys when there are few.
func appSimilarKeys(r map[string]any, key string) string {
	keys := slices.Sorted(maps.Keys(r))
	var near []string
	lk := strings.ToLower(key)
	for _, k := range keys {
		lc := strings.ToLower(k)
		if strings.Contains(lc, lk) || strings.Contains(lk, lc) {
			near = append(near, k)
		}
	}
	switch {
	case len(near) > 0:
		return "; did you mean " + strings.Join(near, ", ") + "?"
	case len(keys) <= 40:
		return "; valid keys: " + strings.Join(keys, ", ")
	}
	return fmt.Sprintf("; %d keys exist: \"app config get <version> <config> --all\" lists them", len(keys))
}

func appConfigTypesCmd() *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "types <version-id|app-id>",
		Short: "Show the configuration types of an app version and their defaults",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			vid, _, err := appVersionArg(cmd, client, args[0], version)
			if err != nil {
				return err
			}
			raw, err := client.Get(cmd.Context(), "/api/v1/app/version/"+vid+"/config/types", nil)
			if err != nil {
				return err
			}
			return render(cmd, raw, nil)
		},
	}
	appVersionFlags(cmd, &version, nil)
	return cmd
}

func appConfigCreateCmd() *cobra.Command {
	var version, configType, data string
	var fields []string
	cmd := &cobra.Command{
		Use:   "create <version-id|app-id>",
		Short: "Create a configuration for an app version",
		Long: `Create a configuration for an app version. The JSON body takes name
(unique per type), settings (for INSTALL: {type: INSTALL, required, hideApp,
silentInstall, managed, autoUpdate, priority: HIGH|MEDIUM|LOW,
scheduleInstall}), groupIds, and accountIds. --type sets the configuration
type (default INSTALL). Prints the new configuration id.`,
		Example: `  ivanti-neurons app config create 121646 -f name=ForDocs -f groupIds='[50918]' \
    -f settings='{"type":"INSTALL","required":true,"autoUpdate":true,"priority":"LOW"}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := appBody(cmd, data, fields, nil)
			if err != nil {
				return err
			}
			if configType != "" || body["type"] == nil {
				body["type"] = strings.ToUpper(cmp.Or(configType, "INSTALL"))
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			vid, _, err := appVersionArg(cmd, client, args[0], version)
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodPost, "/api/v1/app/version/"+vid+"/config", nil, body)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Created %v configuration %s for app version %s", body["type"], raw, vid))
		},
	}
	appVersionFlags(cmd, &version, &configType, "configuration type to create (default INSTALL)")
	appDataFlags(cmd, &data, &fields)
	return cmd
}

func appConfigUpdateCmd() *cobra.Command {
	var version, configType, data string
	var fields []string
	cmd := &cobra.Command{
		Use:   "update <version-id|app-id> <config-id>",
		Short: "Replace a configuration with a JSON body",
		Long: `Replace a configuration of an app version (name, priority, settings, ...)
with the given JSON body. id and applicationVersionId always come from the
arguments; type comes from --type, else the body, else the current
configuration. The assignments (groupIds, accountIds, deviceIds,
ruleGroupIds) are kept unless the body sets them: the API replaces them on
every update, so leaving them out would unassign the configuration. To
change single keys of a managed configuration, use "app config set"; for a
full edit start from "app config get ... -o json".`,
		Example: `  ivanti-neurons app config get 121646 276183 -o json > cfg.json   # edit, then:
  ivanti-neurons app config update 121646 276183 --data @cfg.json
  ivanti-neurons app config update 121646 276183 -f name=ForDocs -f priority=1 \
    -f settings='{"type":"INSTALL","required":true,"autoUpdate":true,"priority":"LOW"}'`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgID, err := parseID(args[1], false)
			if err != nil {
				return err
			}
			if data == "" && len(fields) == 0 {
				return fmt.Errorf("nothing to send: pass --data or -f (the body replaces the configuration)")
			}
			body, err := appBody(cmd, data, fields, nil)
			if err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			vid, types, err := appVersionArg(cmd, client, args[0], version)
			if err != nil {
				return err
			}
			cur, err := appConfig(cmd, client, vid, cfgID, types)
			if err != nil {
				return err
			}
			// keep what the body leaves out of the type and the assignments: the
			// PUT replaces the assignments, a body without them unassigns
			for _, k := range append([]string{"type"}, appAssignmentKeys...) {
				if _, ok := body[k]; !ok {
					body[k] = cur[k]
				}
			}
			if configType != "" {
				body["type"] = strings.ToUpper(configType)
			}
			// the arguments win, so a copied config JSON cannot redirect the update
			body["id"], body["applicationVersionId"] = json.Number(cfgID), json.Number(vid)
			if body, err = appConfigPutBody(body); err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodPut, "/api/v1/app/version/"+vid+"/config/"+cfgID, nil, body)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Updated configuration %s of app version %s", cfgID, vid))
		},
	}
	appVersionFlags(cmd, &version, &configType, "configuration type to send (default: the body's, else the current one)")
	appDataFlags(cmd, &data, &fields)
	return cmd
}

func newAppAttrsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attrs",
		Short: "Manage custom attributes of app versions and apps",
		Long: `Manage custom attributes. Attributes are key=value arguments; repeat a
key for several values. Commands act on an app version id; update and
delete take --app to act on an app id instead, or on several
comma-separated app ids at once.

An update on an app version can remove the attributes it does not name:
pass every existing key ("app attrs get <version-id>" lists them).`,
	}
	cmd.AddCommand(
		appGetCmd("get <version-id|app-id>", "Show an app version's custom attributes", "/api/v1/app/version/{id}/customattributes", nil),
		appAttrsCmd("set", http.MethodPost, "Replace an app version's custom attributes"),
		appAttrsCmd("update", http.MethodPatch, "Add or change custom attributes (on a version, name every existing key)"),
		appAttrsCmd("delete", http.MethodDelete, "Remove custom attributes"),
	)
	return cmd
}

// appAttrsCmd builds the attribute writes: <id> key=value... on an app
// version, or with --app on one app or (several ids) the bulk endpoint.
func appAttrsCmd(verb, method, short string) *cobra.Command {
	var app, overwrite bool
	cmd := &cobra.Command{
		Use:   verb + " <id> <key=value>...",
		Short: short,
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseIDs(args[:1])
			if err != nil {
				return err
			}
			if len(ids) > 1 && !app {
				return fmt.Errorf("several ids need --app (update and delete only)")
			}
			attrs, err := parseAttrs(args[1:])
			if err != nil {
				return err
			}
			id := ids[0]
			path, what := "/api/v1/app/version/"+id+"/customattributes", "app version "+id
			body := map[string]any{"attrs": attrs}
			switch {
			case len(ids) > 1:
				path, what = "/api/v1/app/customattributes", fmt.Sprintf("%d apps", len(ids))
				// The bulk PATCH nests the attrs object; the bulk DELETE takes
				// attribute names only.
				body = map[string]any{"ids": jsonNumbers(ids), "attrs": body}
				if method == http.MethodDelete {
					body = map[string]any{"ids": jsonNumbers(ids), "attrKeys": slices.Sorted(maps.Keys(attrs))}
				}
			case app:
				path, what = "/api/v1/app/"+id+"/customattributes", "app "+id
			}
			done := "Updated"
			if method == http.MethodPatch {
				body["forceOverwrite"] = overwrite
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			switch method {
			case http.MethodDelete:
				if err := confirm(cmd, "remove custom attributes of "+what); err != nil {
					return err
				}
				done = "Removed"
			case http.MethodPost: // replaces every attribute of the version
				if err := confirm(cmd, "replace all custom attributes of "+what); err != nil {
					return err
				}
				done = "Replaced"
			}
			raw, err := client.Call(cmd.Context(), method, path, nil, body)
			if err != nil {
				return err
			}
			return report(cmd, raw, done+" custom attributes of "+what)
		},
	}
	if method != http.MethodPost {
		cmd.Flags().BoolVar(&app, "app", false, "the id is an app id (comma-separate several for a bulk change)")
	}
	if method == http.MethodPatch {
		cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace the current values of the given keys (without it, keys that already have a value keep it)")
	}
	return cmd
}
