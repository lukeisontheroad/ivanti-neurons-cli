package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

var appTaskCols = []string{"taskId", "taskType", "taskStatus", "createdAt", "description"}

func newAppInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install or update an app on iOS devices",
		Long: `Trigger app installs or updates on iOS devices (the API supports iOS
only), either on devices given by GUID or on the devices matched by
distribution criteria. --async queues the installs and returns a task to
follow with "app task list"; Ivanti recommends it for performance. The
async calls need the App & Content Management role.`,
	}
	cmd.AddCommand(newAppInstallDevicesCmd(), newAppInstallDistCmd())
	return cmd
}

func newAppInstallDevicesCmd() *cobra.Command {
	var version string
	var async bool
	cmd := &cobra.Command{
		Use:   "devices <bundle-id> <device-guid>...",
		Short: "Install an app on devices by GUID (iOS only)",
		Long: `Install an app on iOS devices given by GUID. Devices not eligible under
the app's distribution are skipped and listed in the result ("app
eligible <bundle-id>" lists the eligible ones).`,
		Example: `  ivanti-neurons app install devices com.eeenmachine.pocketplanes b8b55026-502b-4f64-a422-f1f094aec6ba --async
  ivanti-neurons app eligible com.example.app | xargs ivanti-neurons app install devices com.example.app`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			guids := splitArgs(args[1:])
			if len(guids) == 0 {
				return fmt.Errorf("missing device GUID")
			}
			for _, g := range guids {
				if !isGUID(g) {
					return fmt.Errorf("invalid device GUID %q", g)
				}
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			space, err := spaceID(cmd, client)
			if err != nil {
				return err
			}
			body := map[string]any{"guids": guids, "dmPartitionId": json.Number(space), "bundleId": args[0], "platformType": "IOS"}
			if version != "" {
				body["appVersion"] = version
			}
			path := "/api/v2/app/install"
			if async {
				path += "/async"
			}
			raw, err := client.Call(cmd.Context(), http.MethodPut, path, nil, body)
			if err != nil {
				return err
			}
			return appRenderTasks(cmd, raw)
		},
	}
	cmd.Flags().StringVar(&version, "app-version", "", "specific app version to install")
	cmd.Flags().BoolVar(&async, "async", false, "queue the installs and return a task")
	return cmd
}

func newAppInstallDistCmd() *cobra.Command {
	var filter int64
	var users, userGroups, devices, deviceGroups []string
	var newInstall, update, everyone, async bool
	cmd := &cobra.Command{
		Use:   "dist <version-id>",
		Short: "Install or update an app version by distribution criteria (iOS only)",
		Long: `Install or update an app version on the iOS devices matched by an app
distribution filter ("app filters"), users, user groups, devices, or
device groups, or on every device that already has the app.

--new-install installs where the app is missing, --update updates older
installed versions; pass one or both.`,
		Example: `  ivanti-neurons app install dist 17000 --filter 4 --users 10050,10051 --user-groups 11000 --update
  ivanti-neurons app install dist 17003 --everyone-with-app --new-install --async`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], false)
			if err != nil {
				return err
			}
			args[0] = id
			if !newInstall && !update {
				return fmt.Errorf("pass --new-install, --update, or both")
			}
			if filter <= 0 && !everyone && len(users)+len(userGroups)+len(devices)+len(deviceGroups) == 0 {
				return fmt.Errorf("pass a target: --filter, --users, --user-groups, --devices, --device-groups, or --everyone-with-app")
			}
			body := map[string]any{
				"appVersionId":      json.Number(args[0]),
				"requestNewInstall": newInstall, "requestUpdate": update, "everyoneWithApp": everyone,
			}
			if filter > 0 {
				body["appDeviceFilterId"] = filter
			}
			for k, v := range map[string][]string{"accountIds": users, "accountGroupIds": userGroups, "deviceIds": devices, "ruleGroupIds": deviceGroups} {
				if len(v) > 0 {
					ids, err := parseIDs(v)
					if err != nil {
						return err
					}
					body[k] = jsonNumbers(ids)
				}
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			space, err := spaceID(cmd, client)
			if err != nil {
				return err
			}
			body["dmPartitionId"] = json.Number(space)
			path := "/api/v2/app/" + args[0] + "/install"
			if async {
				path += "/async"
			}
			raw, err := client.Call(cmd.Context(), http.MethodPut, path, nil, body)
			if err != nil {
				return err
			}
			return appRenderTasks(cmd, raw)
		},
	}
	f := cmd.Flags()
	f.Int64Var(&filter, "filter", 0, "app distribution filter `id` (see \"app filters\")")
	// StringArray, not StringSlice: --users "" must reach parseIDs and fail
	f.StringArrayVar(&users, "users", nil, "user (account) `ids`, comma-separated or repeated")
	f.StringArrayVar(&userGroups, "user-groups", nil, "user group `ids`, comma-separated or repeated")
	f.StringArrayVar(&devices, "devices", nil, "device `ids`, comma-separated or repeated")
	f.StringArrayVar(&deviceGroups, "device-groups", nil, "device group `ids`, comma-separated or repeated")
	f.BoolVar(&newInstall, "new-install", false, "install on devices without the app")
	f.BoolVar(&update, "update", false, "update devices with an older version")
	f.BoolVar(&everyone, "everyone-with-app", false, "target every device that has the app")
	f.BoolVar(&async, "async", false, "queue the installs and return a task")
	return cmd
}

// appRenderTasks prints an async task result as a task table; any other
// result (install counts, skipped devices) as key/value lines or a value.
func appRenderTasks(cmd *cobra.Command, raw json.RawMessage) error {
	var res struct {
		TaskList *[]json.RawMessage `json:"taskList"`
	}
	if flagOutput != "json" && json.Unmarshal(raw, &res) == nil && res.TaskList != nil {
		return renderItems(cmd, *res.TaskList, appTaskCols)
	}
	return render(cmd, raw, nil)
}

func newAppTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Follow asynchronous app tasks (async installs, config error retries)",
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List the API user's asynchronous tasks and their status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := client.Get(cmd.Context(), "/api/v1/asynctask/all", nil)
			if err != nil {
				return err
			}
			return appRenderTasks(cmd, raw)
		},
	}
	retry := &cobra.Command{
		Use:   "retry",
		Short: "Retry installing configurations that failed on devices",
		Long: `Queue a task that retries the installation of configurations in error
state on devices (task type CLEAR_CONFIG_ERROR). Follow it with "app task
status <taskUuid>". The guide documents the parameter as
taskGuid=CLEAR_CONFIG_ERROR; if your tenant rejects it, use "ivanti-neurons
api POST /api/v1/asynctask".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodPost, "/api/v1/asynctask", params("taskGuid", "CLEAR_CONFIG_ERROR"), nil)
			if err != nil {
				return err
			}
			return appReport(cmd, raw, "Queued configuration error retry")
		},
	}
	cmd.AddCommand(list, appGetCmd("status <task-guid>", "Show the status of an asynchronous task", "/api/v1/asynctask/status/{id}", nil), retry)
	return cmd
}
