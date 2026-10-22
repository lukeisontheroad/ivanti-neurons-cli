package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/api"
	"github.com/spf13/cobra"
)

func newAppInhouseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inhouse",
		Short: "Update in-house apps: details, screenshots, icon, version metadata, profiles",
		Long: `Update in-house apps. Upload binaries with "app create"; list in-house
apps with "app list --type IN_HOUSE".`,
	}
	cmd.AddCommand(newAppInhouseUpdateCmd(), newAppInhouseScreenshotsCmd(), newAppInhouseIconCmd(), newAppInhouseCopyCmd(), newAppInhouseProfilesCmd())
	return cmd
}

func newAppInhouseUpdateCmd() *cobra.Command {
	var data string
	var fields []string
	var repush bool
	cmd := &cobra.Command{
		Use:   "update <version-id>",
		Short: "Update an in-house app version's name, version, categories, and texts",
		Long: `Update an in-house app version's details: name, displayVersion,
categoryIds, whatsNew, description, overrideUrl, distributionSettings.

The body replaces the version's details, so send the mandatory fields too:
id (the app id), name, displayVersion, primaryVersion, platformAppId,
enableReviews, enableRatings (take them from "app view <app-id> -o json").
dmPartitionId (current device space) and appType IN_HOUSE are filled in
unless given. --repush pushes the app to devices again.`,
		Example: `  ivanti-neurons app inhouse update 54710 --data @app.json
  ivanti-neurons app inhouse update 54710 --data @app.json -f whatsNew="Better memory management."`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := parseID(args[0], false); err != nil {
				return err
			}
			if data == "" && len(fields) == 0 {
				return fmt.Errorf("nothing to send: pass --data or -f (the body replaces the version's details)")
			}
			body, err := appBody(cmd, data, fields, map[string]any{"appType": "IN_HOUSE"})
			if err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			if err := appSetSpace(cmd, client, body); err != nil {
				return err
			}
			q := params("repushApp", strconv.FormatBool(repush))
			raw, err := client.Call(cmd.Context(), http.MethodPut, "/api/v2/app/inhouse/"+args[0], q, body)
			if err != nil {
				return err
			}
			return report(cmd, raw, "Updated app version "+args[0])
		},
	}
	appDataFlags(cmd, &data, &fields)
	cmd.Flags().BoolVar(&repush, "repush", false, "push the app to devices again")
	return cmd
}

func newAppInhouseScreenshotsCmd() *cobra.Command {
	var phone, tablet []string
	cmd := &cobra.Command{
		Use:   "screenshots <version-id>",
		Short: "Replace an in-house app version's screenshots",
		Long: `Upload phone and tablet screenshots for an in-house app version. The
upload replaces all existing screenshots.`,
		Example: `  ivanti-neurons app inhouse screenshots 52161 --phone home.png --phone detail.png --tablet ipad.png`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := parseID(args[0], false); err != nil {
				return err
			}
			if len(phone)+len(tablet) == 0 {
				return fmt.Errorf("pass at least one --phone or --tablet screenshot")
			}
			files := map[string]string{}
			for i, f := range phone {
				files[fmt.Sprintf("phoneScreenshots[%d].file", i)] = f
			}
			for i, f := range tablet {
				files[fmt.Sprintf("tabletScreenshots[%d].file", i)] = f
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodPost, "/api/v1/app/version/"+args[0]+"/screenshots", nil, &api.Multipart{Files: files})
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Uploaded %d screenshot(s) for app version %s", len(files), args[0]))
		},
	}
	cmd.Flags().StringArrayVar(&phone, "phone", nil, "phone screenshot image file (repeatable, in order)")
	cmd.Flags().StringArrayVar(&tablet, "tablet", nil, "tablet screenshot image file (repeatable, in order)")
	return cmd
}

func newAppInhouseIconCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "icon <version-id> <image-file>",
		Short: "Replace an in-house iOS or macOS app version's icon",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := parseID(args[0], false); err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			q := url.Values{"preview": {"true"}, "dataUri": {"true"}}
			body := &api.Multipart{Files: map[string]string{"image-data": args[1]}}
			raw, err := client.Call(cmd.Context(), http.MethodPost, "/api/v2/app/inhouse/"+args[0]+"/icon", q, body)
			if err != nil {
				return err
			}
			// The response is the icon as a data: URI, not JSON.
			if !json.Valid(raw) {
				if raw, err = json.Marshal(string(raw)); err != nil {
					return err
				}
			}
			return report(cmd, raw, "Updated icon of app version "+args[0])
		},
	}
}

func newAppInhouseCopyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "copy <target-version-id>",
		Short: "Copy the previous version's metadata to a new in-house app version",
		Long: `Copy the description, screenshots, what's new, distribution, app device
filters, and featured settings of the previous version to a newly uploaded
version ("app create <file> --new-version" prints its
applicationVersionId).`,
		Example: `  ivanti-neurons app inhouse copy 17002`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := parseID(args[0], false); err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodPut, "/api/v1/app", params("targetApplicationVersionId", args[0]), nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, "Copied previous version metadata to app version "+args[0])
		},
	}
}

func newAppInhouseProfilesCmd() *cobra.Command {
	var opts searchOpts
	cmd := &cobra.Command{
		Use:   "profiles",
		Short: "List in-house apps with their provisioning profiles and expiry",
		Long: `List in-house apps of the device space with the Apple provisioning
profile attached to each, to spot profiles that need renewal.`,
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
			cols := []string{"applicationVersionId", "name", "bundleId", "displayVersion", "provisioningProfile.devTeamName", "provisioningProfile.expirationDate"}
			return runSearch(cmd, client, "/api/v1/app/inhouse/profiles", &opts, params("dmPartitionId", space), cols)
		},
	}
	opts.register(cmd)
	return cmd
}
