package cmd

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

func newAppConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage app configurations (install settings) of an app version",
		Long: `Manage the configurations of an app version. Ivanti supports type
INSTALL only through the API (required, hideApp, silentInstall, managed,
autoUpdate, priority, ... in "settings"); other types are internal.`,
	}
	var opts searchOpts
	var configType string
	list := &cobra.Command{
		Use:   "list <version-id>",
		Short: "List an app version's configurations",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := parseID(args[0], false); err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			cols := []string{"id", "name", "type", "priority", "defaultConfig", "userCounts", "groupCounts", "modifiedAt"}
			return runSearch(cmd, client, "/api/v1/app/version/"+args[0]+"/config", &opts, params("type", strings.ToUpper(configType)), cols)
		},
	}
	opts.register(list)
	list.Flags().StringVar(&configType, "type", "INSTALL", "configuration type")

	var data string
	var fields []string
	create := &cobra.Command{
		Use:   "create <version-id>",
		Short: "Create a configuration for an app version",
		Long: `Create a configuration for an app version. The JSON body takes name
(unique per type), settings {type: INSTALL, required, hideApp,
silentInstall, managed, autoUpdate, priority: HIGH|MEDIUM|LOW,
scheduleInstall}, groupIds, and accountIds. type defaults to INSTALL.
Prints the new configuration id.`,
		Example: `  ivanti-neurons app config create 121646 -f name=ForDocs -f groupIds='[50918]' \
    -f settings='{"type":"INSTALL","required":true,"autoUpdate":true,"priority":"LOW"}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := parseID(args[0], false); err != nil {
				return err
			}
			body, err := appBody(cmd, data, fields, map[string]any{"type": "INSTALL"})
			if err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodPost, "/api/v1/app/version/"+args[0]+"/config", nil, body)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Created app config %s", raw))
		},
	}
	appDataFlags(create, &data, &fields)

	var updData string
	var updFields []string
	update := &cobra.Command{
		Use:   "update <version-id> <config-id>",
		Short: "Update an app version's configuration",
		Long: `Update a configuration of an app version. The JSON body replaces the
configuration (name, priority, settings, ...). id and applicationVersionId
always come from the arguments; type INSTALL is filled in unless given.`,
		Example: `  ivanti-neurons app config update 121646 276183 -f name=ForDocs -f priority=1 \
    -f settings='{"type":"INSTALL","required":true,"autoUpdate":true,"priority":"LOW"}'`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			for i, a := range args {
				id, err := parseID(a, false)
				if err != nil {
					return err
				}
				args[i] = id
			}
			if updData == "" && len(updFields) == 0 {
				return fmt.Errorf("nothing to send: pass --data or -f (the body replaces the configuration)")
			}
			body, err := appBody(cmd, updData, updFields, map[string]any{"type": "INSTALL"})
			if err != nil {
				return err
			}
			// the arguments win, so a copied config JSON cannot redirect the update
			body["id"], body["applicationVersionId"] = json.Number(args[1]), json.Number(args[0])
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodPut, "/api/v1/app/version/"+args[0]+"/config/"+args[1], nil, body)
			if err != nil {
				return err
			}
			return report(cmd, raw, "Updated app config "+args[1])
		},
	}
	appDataFlags(update, &updData, &updFields)

	types := appGetCmd("types <version-id>", "Show the configuration types of an app version and their defaults", "/api/v1/app/version/{id}/config/types", nil)
	cmd.AddCommand(list, types, create, update)
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

Per the API guide, an update on an app version removes the version's
attributes that the call does not name: pass every existing key ("app attrs
get <version-id>").`,
	}
	cmd.AddCommand(
		appGetCmd("get <version-id>", "Show an app version's custom attributes", "/api/v1/app/version/{id}/customattributes", nil),
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
