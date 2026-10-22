package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/api"
	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/output"
	"github.com/spf13/cobra"
)

// actionSpec describes one remote device action. Every action is
// PUT /api/v1/device/<path> with the device ids and its parameters as a form
// body; most have a viability check on the same path with
// checkViability=true. One builder (actionCmd) turns a spec into a command.
type actionSpec struct {
	use, short, long, example string
	path                      string       // action path under /api/v1/device/
	check                     string       // HTTP method of the viability check; "" = none
	guids                     bool         // GUID arguments are sent as guids= (else rejected)
	idsKey                    string       // query key for the ids; "" = "ids"
	space                     bool         // send dmPartitionId
	base                      url.Values   // parameters always sent
	flags                     []actionFlag // typed flags mapped to query parameters
	oneOf                     []string     // at least one of these flags is required
	confirm                   string       // destructive: confirm() verb; the check then runs first
	boolResult                bool         // result is 1 (success) / 0 (failure), not a device count
	done                      string       // report format taking the device count
}

// actionFlag maps a typed flag to an API parameter.
type actionFlag struct {
	name, param, usage string
	kind               any    // flag type by zero value: nil string, false bool, 0 int
	implies            string // "key=value" also sent when the flag is set
}

// actionRejectedCols are the columns for devices a viability check rejects.
var actionRejectedCols = []string{"id", "deviceName", "platformType", "actionExceptionCodes"}

var actionSpecs = []actionSpec{
	{
		use:   "os-update <id>...",
		short: "Update the OS of iOS devices to a version",
		long: `Schedule an OS update to the given version (iOS only). Result 1 means the
update was scheduled. --check only runs the viability check.`,
		example: `  ivanti-neurons device os-update 22002 --version 17.5
  ivanti-neurons device os-update 22002 22015 --check`,
		path:  "scheduleOSUpdate",
		check: http.MethodGet,
		flags: []actionFlag{{name: "version", param: "version", usage: `OS version to update to, e.g. "17.5"`}},
		oneOf: []string{"version", "check"}, boolResult: true,
		done: "Scheduled OS update on %d device(s)",
	},
	{
		use:        "restart <id>...",
		short:      "Restart supervised iOS devices",
		long:       "Restart devices (supervised iOS only).",
		path:       "restart",
		boolResult: true,
		done:       "Restarted %d device(s)",
	},
	{
		use:   "checkin <id|guid>...",
		short: "Force devices to check in now",
		long: `Force devices to check in immediately. Accepts numeric device ids and
GUIDs (the viability check takes numeric ids only).`,
		example: `  ivanti-neurons device checkin 22062 22196
  ivanti-neurons device checkin 57d9903c-aadf-4b40-aef3-e1f24302f180`,
		path:  "forceCheckin",
		check: http.MethodGet,
		guids: true,
		done:  "Forced check-in on %d device(s)",
	},
	{
		use:   "lock <id|guid>...",
		short: "Lock devices",
		long: `Lock devices. Accepts numeric device ids and GUIDs (the viability check
takes numeric ids only). --message and --phone are shown on the lock screen
where the platform supports it; --pin sets the macOS Find My lock PIN
(6 digits). Unlock with "device unlock".`,
		example: `  ivanti-neurons device lock 22000
  ivanti-neurons device lock 22000 --message "Return to IT" --phone 555-555-5555`,
		path:  "lock",
		check: http.MethodGet,
		guids: true,
		flags: []actionFlag{
			{name: "message", param: "message", usage: "message shown on the lock screen"},
			{name: "phone", param: "phoneNumber", usage: "phone number shown on the lock screen"},
			{name: "pin", param: "osxLockPin", usage: "macOS lock PIN (6 digits)"},
		},
		done: "Locked %d device(s)",
	},
	{
		use:   "unlock <id>...",
		short: "Unlock devices",
		long: `Unlock devices. --pin is the alphanumeric unlock PIN (6-8 letters or
digits), used by devices whose Ivanti client is version 92 or later; older
clients are unlocked with the legacy code 0000.`,
		example: `  ivanti-neurons device unlock 22000 --pin 123ab400`,
		path:    "unlock",
		flags:   []actionFlag{{name: "pin", param: "deviceUnlockPin", usage: "unlock PIN, 6-8 letters or digits"}},
		done:    "Unlocked %d device(s)",
	},
	{
		use:   "wipe <id>...",
		short: "Factory-reset devices (irreversible)",
		long: `Wipe devices: a factory reset that erases all data. Irreversible once the
device receives it; "device wipe-cancel" cancels a wipe that is still pending.

Before sending, the viability check runs: if the API reports any device as
not eligible, nothing is sent and the rejected devices are listed. Then
asks for confirmation (--yes skips it). --check only runs the check.
Requires Tenant Admin credentials. More options (e.g. preserveApps) via
-f key=value.`,
		example: `  ivanti-neurons device wipe 25652 --check
  ivanti-neurons device wipe 25652 --message Decommissioning --yes
  ivanti-neurons device wipe 32362 --return-to-service --wifi-policy 144616`,
		path:    "wipe",
		check:   http.MethodGet,
		confirm: "wipe",
		flags: []actionFlag{
			{name: "message", param: "message", usage: "message shown on the device upon wipe"},
			{name: "pin", param: "osxWipePin", usage: "macOS Find My PIN (6 characters)"},
			{name: "preserve-data-plan", param: "preserveDataPlan", usage: "keep the cellular data plan (iOS 11+, Android 15+)", kind: false},
			{name: "disallow-proximity-setup", param: "iosDisallowProximitySetup", usage: "disallow proximity setup after the wipe (iOS 11.3+)", kind: false},
			{name: "return-to-service", param: "returnToServiceEnabled", usage: "re-enroll automatically after the wipe (Return to Service)", kind: false},
			{name: "wifi-policy", param: "wifiPolicyId", usage: "policy id of the Wi-Fi config (assigned to the device) to join for Return to Service", kind: 0},
		},
		done: "Wiped %d device(s)",
	},
	{
		use:   "wipe-cancel <id>...",
		short: "Cancel a pending wipe",
		path:  "wipeCancel",
		done:  "Cancelled wipe on %d device(s)",
	},
	{
		use:   "retire <id>...",
		short: "Retire devices (unenroll them from management)",
		long: `Retire devices: remove them from management, including the managed
configurations and apps. "device retire-cancel" cancels a pending retire.

Before sending, the viability check runs: if the API reports any device as
not eligible, nothing is sent and the rejected devices are listed. Then
asks for confirmation (--yes skips it). --check only runs the check.
Requires Tenant Admin credentials. More options (message, osxWipePin,
relinquishOwnership, silentRetireOrWipeWorkProfile) via -f key=value.`,
		example: `  ivanti-neurons device retire 1007966 --yes`,
		path:    "retire",
		check:   http.MethodGet,
		confirm: "retire",
		done:    "Retired %d device(s)",
	},
	{
		use:   "retire-cancel <id>...",
		short: "Cancel a pending retire",
		path:  "retireCancel",
		done:  "Cancelled retire on %d device(s)",
	},
	{
		use:   "logout <id>...",
		short: "Force log out the current users of devices",
		long: `Force log out the current user of each device (shared devices). Runs
the viability check first and refuses if any device does not support it;
asks for confirmation (--yes skips it). --check only runs the check.`,
		example: `  ivanti-neurons device logout 22002 --yes`,
		path:    "logoutUser",
		check:   http.MethodGet,
		confirm: "log out the current user of",
		done:    "Logged out the user on %d device(s)",
	},
	{
		use:   "message <id>...",
		short: "Send a push notification or email to devices",
		long: `Send devices a push notification (--push) and/or an email to the device
user (--email, with --subject). At least one of --push and --email is
required.`,
		example: `  ivanti-neurons device message 370872 383279 --push "Please update iOS today"
  ivanti-neurons device message 370872 --email "Your device is out of compliance." --subject "Compliance"`,
		path:   "message",
		idsKey: "deviceIds",
		space:  true,
		// sendPushNotification and sendEmail default to true server-side.
		base: url.Values{"sendPushNotification": {"false"}, "sendEmail": {"false"}, "emailSubject": {""}, "emailBody": {""}},
		flags: []actionFlag{
			{name: "push", param: "pushNotificationMessage", usage: "push notification text", implies: "sendPushNotification=true"},
			{name: "email", param: "emailBody", usage: "email body", implies: "sendEmail=true"},
			{name: "subject", param: "emailSubject", usage: "email subject"},
			{name: "badge", param: "badge", usage: "app badge number to set", kind: 0},
		},
		oneOf: []string{"push", "email"},
		done:  "Sent message to %d device(s)",
	},
	{
		use:   "enter-kiosk <id>...",
		short: "Put Android devices into kiosk mode",
		long:  "Put devices into kiosk mode (Android only). --check only runs the viability check.",
		path:  "enterKioskMode",
		check: http.MethodGet,
		done:  "Entered kiosk mode on %d device(s)",
	},
	{
		use:   "exit-kiosk <id>...",
		short: "Take Android devices out of kiosk mode",
		long:  "Take devices out of kiosk mode (Android only).",
		path:  "exitKioskMode",
		done:  "Exited kiosk mode on %d device(s)",
	},
	{
		use:   "enable-lost-mode <id>...",
		short: "Enable lost mode on iOS devices",
		long: `Enable lost mode: the device locks and shows --message (required) and
--phone. --footnote sets the lock screen footnote (15 characters max).
Requires a supervised iOS device and the Device Management role. The API has
no viability check for enabling; "device view <id>" shows "supervised", and
"device disable-lost-mode <id> --check" reports DEVICE_NOT_SUPERVISED for
devices lost mode cannot work on.`,
		example: `  ivanti-neurons device enable-lost-mode 121073 --message "This device has been lost. Please call the number below." --phone 555-555-5555 --footnote "Lost Phone"`,
		path:    "enableLostMode",
		flags: []actionFlag{
			{name: "message", param: "message", usage: "message shown on the device"},
			{name: "phone", param: "phoneNumber", usage: "phone number shown on the device"},
			{name: "footnote", param: "footnote", usage: "lock screen footnote (15 characters max)", implies: "changeFootnote=true"},
		},
		oneOf: []string{"message"},
		done:  "Enabled lost mode on %d device(s)",
	},
	{
		use:   "disable-lost-mode <id>...",
		short: "Disable lost mode on iOS devices",
		long: `Disable lost mode (supervised iOS only), restoring the device into service.
--check only runs the viability check: it lists ineligible devices with the
reason (e.g. DEVICE_NOT_SUPERVISED, not in lost mode). Requires the Device
Management role.`,
		path:  "disableLostMode",
		check: http.MethodGet,
		done:  "Disabled lost mode on %d device(s)",
	},
	{
		use:   "relinquish <id>...",
		short: "Relinquish ownership of company-owned Android devices",
		long: `Relinquish ownership (Android only): removes the work profile and all
policies from a company-owned device, releasing it for personal use.
"device relinquish-cancel" cancels a pending relinquish.

Before sending, the viability check runs (it returns the number of eligible
devices): if any device is not eligible, nothing is sent. Then asks for
confirmation (--yes skips it). --check only runs the check; unlike the other
actions' checks it is a PUT request (with checkViability=true).`,
		example: `  ivanti-neurons device relinquish 909 --check
  ivanti-neurons device relinquish 909 --yes`,
		path:    "relinquishOwnership",
		check:   http.MethodPut,
		confirm: "relinquish",
		done:    "Relinquished %d device(s)",
	},
	{
		use:   "relinquish-cancel <id>...",
		short: "Cancel a pending relinquish",
		path:  "relinquishOwnershipCancel",
		done:  "Cancelled relinquish on %d device(s)",
	},
}

// deviceActionCmds returns the remote device action commands attached under
// "device".
func deviceActionCmds() []*cobra.Command {
	cmds := []*cobra.Command{actionKioskUsersCmd()}
	for _, s := range actionSpecs {
		cmds = append(cmds, actionCmd(s))
	}
	return cmds
}

func actionCmd(s actionSpec) *cobra.Command {
	var check bool
	var fields []string
	cmd := &cobra.Command{
		Use:     s.use,
		Short:   s.short,
		Long:    s.long,
		Example: s.example,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, guids, err := actionIDs(cmd, s, args)
			if err != nil {
				return err
			}
			n := len(ids) + len(guids)
			extra, err := fieldValues(fields)
			if err != nil {
				return err
			}
			for _, k := range []string{"ids", "guids", "deviceIds", "checkViability"} {
				if extra.Has(k) {
					return fmt.Errorf("-f %s is not allowed: pass device ids as arguments, --check for the viability check", k)
				}
			}
			q := url.Values{}
			for k, v := range s.base {
				q[k] = v
			}
			for k, v := range extra {
				q[k] = v
			}
			for _, f := range s.flags {
				if cmd.Flags().Changed(f.name) {
					q.Set(f.param, cmd.Flags().Lookup(f.name).Value.String())
					if k, v, ok := strings.Cut(f.implies, "="); ok {
						q.Set(k, v)
					}
				}
			}
			client, err := newClient()
			if err != nil {
				return err
			}

			if check || s.confirm != "" && s.check != "" {
				if len(guids) > 0 {
					return fmt.Errorf("the viability check takes numeric device ids, not GUIDs: \"ivanti-neurons device view <guid>\" shows the id")
				}
				raw, rejected, bad, err := actionCheck(cmd, client, s, ids)
				if err != nil {
					return err
				}
				if check {
					return actionCheckReport(cmd, raw, rejected, bad, n)
				}
				if bad > 0 {
					if len(rejected) > 0 {
						output.Objects(cmd.ErrOrStderr(), rejected, actionRejectedCols)
					}
					return fmt.Errorf("refusing to %s: %d of %d device(s) not eligible, nothing was sent", cmd.Name(), bad, n)
				}
			}
			if s.confirm != "" {
				if err := confirm(cmd, fmt.Sprintf("%s %d device(s)", s.confirm, n)); err != nil {
					return err
				}
			}

			if s.space {
				space, err := spaceID(cmd, client)
				if err != nil {
					return err
				}
				q.Set("dmPartitionId", space)
			}
			key := s.idsKey
			if key == "" {
				key = "ids"
			}
			if len(ids) > 0 {
				q[key] = ids
			}
			if len(guids) > 0 {
				q["guids"] = guids
			}
			// Form body, as in the guide's examples: keeps ids out of URL
			// length limits and PINs out of access logs.
			raw, err := client.Call(cmd.Context(), http.MethodPut, "/api/v1/device/"+s.path, nil, q)
			if err != nil {
				return err
			}
			return actionReport(cmd, s, raw, n)
		},
	}
	for _, f := range s.flags {
		switch def := f.kind.(type) {
		case bool:
			cmd.Flags().Bool(f.name, def, f.usage)
		case int:
			cmd.Flags().Int(f.name, def, f.usage)
		default:
			cmd.Flags().String(f.name, "", f.usage)
		}
	}
	if s.check != "" {
		cmd.Flags().BoolVar(&check, "check", false, "only run the viability check: list devices that are not eligible (exit 1 if any)")
	}
	cmd.Flags().StringArrayVarP(&fields, "field", "f", nil, "extra API parameter key=value (repeatable)")
	cmd.MarkFlagsOneRequired(s.oneOf...)
	return cmd
}

// actionIDs splits device arguments into numeric ids and GUIDs (GUIDs only
// where the endpoint accepts them), dropping duplicates.
func actionIDs(cmd *cobra.Command, s actionSpec, args []string) (ids, guids []string, err error) {
	seen := map[string]bool{}
	for _, a := range splitArgs(args) {
		if !isGUID(a) {
			if a, err = parseID(a, false); err != nil {
				return nil, nil, err
			}
		}
		switch {
		case seen[a]:
		case !isGUID(a):
			ids = append(ids, a)
		case s.guids:
			guids = append(guids, a)
		default:
			return nil, nil, fmt.Errorf("%s takes numeric device ids, not GUIDs: %s", cmd.Name(), a)
		}
		seen[a] = true
	}
	if len(ids)+len(guids) == 0 {
		return nil, nil, fmt.Errorf("no device ids given")
	}
	return ids, guids, nil
}

// actionCheck runs the viability check and returns the raw result, the
// rejected devices, and how many are not eligible. Most checks list the
// ineligible devices as a search page; relinquish returns the eligible count.
func actionCheck(cmd *cobra.Command, c *api.Client, s actionSpec, ids []string) (raw json.RawMessage, rejected []json.RawMessage, bad int, err error) {
	q := url.Values{"checkViability": {"true"}, "ids": ids}
	if raw, err = c.Call(cmd.Context(), s.check, "/api/v1/device/"+s.path, q, nil); err != nil {
		return nil, nil, 0, err
	}
	var eligible int
	if json.Unmarshal(raw, &eligible) == nil {
		return raw, nil, len(ids) - eligible, nil
	}
	if rejected, bad, err = parsePage(raw); err != nil {
		return nil, nil, 0, fmt.Errorf("viability check: %w: nothing was sent", err)
	}
	return raw, rejected, bad, nil
}

// actionCheckReport prints a --check result; any ineligible device is an error.
func actionCheckReport(cmd *cobra.Command, raw json.RawMessage, rejected []json.RawMessage, bad, n int) error {
	switch {
	case flagOutput == "json":
		if err := output.JSON(cmd.OutOrStdout(), raw); err != nil {
			return err
		}
	case bad == 0:
		fmt.Fprintf(cmd.OutOrStdout(), "All %d device(s) eligible for %s\n", n, cmd.Name())
	case len(rejected) > 0:
		output.Objects(cmd.OutOrStdout(), rejected, actionRejectedCols)
	}
	if bad > 0 {
		return fmt.Errorf("%d of %d device(s) not eligible for %s", bad, n, cmd.Name())
	}
	return nil
}

// actionReport prints an action result: a device count, true, or a 1/0
// success flag (then the requested count is reported).
func actionReport(cmd *cobra.Command, s actionSpec, raw json.RawMessage, n int) error {
	r := string(bytes.TrimSpace(raw))
	if r == "false" || r == "0" {
		return fmt.Errorf("%s failed: the API returned %s", cmd.Name(), r)
	}
	if count, err := strconv.Atoi(r); err == nil && !s.boolResult {
		if count < n {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: the API acted on %d of %d device(s)\n", count, n)
		}
		n = count
	}
	return report(cmd, raw, fmt.Sprintf(s.done, n))
}

func actionKioskUsersCmd() *cobra.Command {
	var opts searchOpts
	var fields []string
	cmd := &cobra.Command{
		Use:   "kiosk-users <id>",
		Short: "List the users last logged in to a shared kiosk device",
		Long: `List the users who logged in to a shared kiosk device, with login and
logout times and session length. Filter by login time with
-f loginRangeStartTime=<epoch ms> -f loginRangeEndTime=<epoch ms>.`,
		Example: `  ivanti-neurons device kiosk-users 22001 --sort loggedInAt:desc
  ivanti-neurons device kiosk-users 22001 -f loginRangeStartTime=1760344499977 -f loginRangeEndTime=1760344599988`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], false)
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
			return runSearch(cmd, client, "/api/v1/device/"+id+"/kioskUsers", &opts, extra,
				[]string{"displayName", "uid", "loggedInAt", "loggedOutAt", "lastSessionTime"})
		},
	}
	opts.register(cmd)
	cmd.Flags().StringArrayVarP(&fields, "field", "f", nil, "extra query parameter key=value (repeatable)")
	return cmd
}
