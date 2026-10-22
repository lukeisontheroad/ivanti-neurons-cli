package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// deviceCols are the table columns of device searches.
var deviceCols = []string{"id", "prettyModel", "platformType", "platformVersion", "uid", "registrationState", "lastCheckin"}

// deviceViewCols are the fields "device view" prints without --all.
var deviceViewCols = []string{
	"id", "guid", "deviceName", "prettyModel", "deviceModel", "manufacturer",
	"platformType", "platformVersion", "osBuildVersion", "serialNumber", "imei",
	"udid", "wifiMacAddress", "phoneNumber", "registrationState", "complianceState",
	"policyViolationCount", "violatedPolicies", "ownershipType", "supervised",
	"jailbroken", "quarantined", "lostModeEnabled", "kioskState", "displayName",
	"uid", "emailAddress", "ownerId", "lastCheckin", "lastRegistrationTime",
	"dmPartitionName", "customAttributes.attrs",
}

func newDeviceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "device", Short: "Search, inspect, and manage devices"}
	cmd.AddCommand(
		deviceListCmd(),
		deviceWithAppCmd(),
		deviceViewCmd(),
		deviceDeleteCmd(),
		deviceAssignCmd(),
		deviceAttrsCmd(),
		deviceConfigCmd(),
		deviceComplianceCmd(),
		deviceCertsCmd(),
		devicePredicateCmd(),
		deviceLdapSyncCmd(),
		deviceTeamViewerCmd(),
	)
	cmd.AddCommand(deviceActionCmds()...)
	return cmd
}

// deviceGet fetches /api/v1/device/<id><suffix> and renders it with cols.
func deviceGet(cmd *cobra.Command, arg, suffix string, guid bool, cols []string) error {
	id, err := parseID(arg, guid)
	if err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	raw, err := client.Get(cmd.Context(), "/api/v1/device/"+id+suffix, nil)
	if err != nil {
		return err
	}
	return render(cmd, raw, cols)
}

// deviceFq ANDs typed filter clauses onto a --fq expression, which is
// parenthesized so its ORs keep their scope.
func deviceFq(fq string, clauses []string) string {
	if fq != "" {
		if len(clauses) > 0 {
			fq = "(" + fq + ")"
		}
		clauses = append([]string{fq}, clauses...)
	}
	return strings.Join(clauses, " AND ")
}

// commaList splits repeatable, comma-separated flag values (not on spaces:
// attribute names may hold them). A flag given only empty values, such as
// --key "" from an unset "$VAR", is an error, never "none".
func commaList(flag string, vals []string) ([]string, error) {
	var out []string
	for _, v := range vals {
		out = append(out, strings.FieldsFunc(v, func(r rune) bool { return r == ',' })...)
	}
	if len(out) == 0 && len(vals) > 0 {
		return nil, fmt.Errorf("--%s: no values in %q", flag, strings.Join(vals, " "))
	}
	return out, nil
}

// deviceWithin builds a "within the last dur" clause in the guide's relative
// time syntax: lastCheckin<'hh:mm:ss' (example '00:01:00') and
// lastRegistrationTime<'d:hh:mm:ss' (example '0:0:03:06'). The day form is
// used from one day on. dur is a Go duration with an optional leading day
// count: 30m, 12h, 7d, 1d12h.
// deviceWithin builds a relative time clause: op "<" = within dur ("time
// ago" smaller), ">" = longer ago than dur.
func deviceWithin(field, op, dur string) (string, error) {
	days, rest, ok := strings.Cut(dur, "d")
	if !ok {
		days, rest = "0", dur
	}
	n, err := strconv.Atoi(days)
	var d time.Duration
	if err == nil && rest != "" {
		d, err = time.ParseDuration(rest)
	}
	d += time.Duration(n) * 24 * time.Hour
	if err != nil || d < time.Second {
		return "", fmt.Errorf("invalid duration %q (want e.g. 30m, 12h, 7d, 1d12h)", dur)
	}
	s := int64(d / time.Second)
	hms := fmt.Sprintf("%02d:%02d:%02d", s/3600%24, s/60%60, s%60)
	if field == "lastCheckin" && s < 86400 { // the guide shows lastRegistrationTime in day form only
		return fmt.Sprintf("%s%s'%s'", field, op, hms), nil
	}
	return fmt.Sprintf("%s%s'%d:%s'", field, op, s/86400, hms), nil
}

// deviceSearch runs a GET /api/v1/device search in the device space.
func deviceSearch(cmd *cobra.Command, opts *searchOpts, extra url.Values) error {
	opts.sortForLargeFetch()
	client, err := newClient()
	if err != nil {
		return err
	}
	space, err := spaceID(cmd, client)
	if err != nil {
		return err
	}
	extra.Set("dmPartitionId", space)
	return runSearch(cmd, client, "/api/v1/device", opts, extra, deviceCols)
}

func deviceListCmd() *cobra.Command {
	var opts searchOpts
	var platform, status, user, email, ownerID, checkedIn, notCheckedIn, registered string
	var attrs []string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls", "search"},
		Short:   "Search devices by platform, status, user, custom attribute, or check-in time",
		Long: `Search devices in the device space (GET /api/v1/device).

The typed filters below are ANDed with each other and with --fq:
  --platform ios                 platformType='IOS'
  --status active                registrationState='ACTIVE'
  --user jdoe@example.com        UID EQ '...'   (the owner's UID column of "user list")
  --owner-id 10542407            ownerId='...'  (the owner's ID column of "user list")
  --email jdoe@example.com       EMAILADDRESS EQ '...'
  --attr cityId=SF               CFDEVICE[cityId]='SF' (custom device attribute)
  --checked-in-within 2h         lastCheckin<'02:00:00'       (checked in recently)
  --not-checked-in-within 30d    lastCheckin>'30:00:00:00'    (stale devices)
  --registered-within 7d         lastRegistrationTime<'7:00:00:00'

Relative time filters compare against a "time ago" literal, hh:mm:ss or
d:hh:mm:ss: "<" means more recent, ">" longer ago. Durations: 30m, 12h,
7d, 1d12h.

Numeric custom attributes need a "_#" suffix on the name
(--attr 'rollNumber_#=42'). LDAP attributes go through --fq:
"CFLDAP[department]='Sales'".

Fetching more than 10000 devices (--limit 0 or above 10000) sorts by id
unless --sort is given, so paging can switch to searchAfterValues; with
another sort field the API refuses offsets of 10000 or more.`,
		Example: `  ivanti-neurons device list --platform ios --status active
  ivanti-neurons device list --not-checked-in-within 30d
  ivanti-neurons device list --email jdoe@example.com
  ivanti-neurons device list --attr cityId=SF --checked-in-within 7d
  ivanti-neurons device list --registered-within 1d --sort lastRegistrationTime:desc
  ivanti-neurons device list --fq "CFLDAP[department]='Sales'" --limit 0 -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// shortcut: values are not escaped (the guide documents no escape syntax); a ' in a value breaks the filter.
			var clauses []string
			for _, c := range [][2]string{
				{"platformType=", strings.ToUpper(platform)},
				{"registrationState=", strings.ToUpper(status)},
				{"UID EQ ", user},
				{"ownerId=", ownerID},
				{"EMAILADDRESS EQ ", email},
			} {
				if c[1] != "" {
					clauses = append(clauses, c[0]+"'"+c[1]+"'")
				}
			}
			for _, a := range attrs {
				k, v, ok := strings.Cut(a, "=")
				if !ok || k == "" {
					return fmt.Errorf("invalid --attr %q (want name=value)", a)
				}
				clauses = append(clauses, "CFDEVICE["+k+"]='"+v+"'")
			}
			if ownerID != "" {
				if _, err := parseID(ownerID, false); err != nil {
					return fmt.Errorf("--owner-id: %w", err)
				}
			}
			if user != "" && strings.Trim(user, "0123456789") == "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "Note: --user matches the UID (usually the email); for the account id use --owner-id %s\n", user)
			}
			for _, w := range [][3]string{{"lastCheckin", "<", checkedIn}, {"lastCheckin", ">", notCheckedIn}, {"lastRegistrationTime", "<", registered}} {
				if w[2] == "" {
					continue
				}
				c, err := deviceWithin(w[0], w[1], w[2])
				if err != nil {
					return err
				}
				clauses = append(clauses, c)
			}
			opts.fq = deviceFq(opts.fq, clauses)
			return deviceSearch(cmd, &opts, url.Values{})
		},
	}
	opts.register(cmd)
	f := cmd.Flags()
	f.StringVar(&platform, "platform", "", "platform type, e.g. IOS or ANDROID")
	f.StringVar(&status, "status", "", "registration state, e.g. ACTIVE, RETIRED, WIPE_PENDING")
	f.StringVar(&user, "user", "", "owner UID (usually the email; the UID column of \"user list\")")
	f.StringVar(&ownerID, "owner-id", "", "owner account id (the ID column of \"user list\")")
	f.StringVar(&email, "email", "", "owner email address")
	f.StringArrayVar(&attrs, "attr", nil, "custom device attribute name=value (repeatable)")
	f.StringVar(&checkedIn, "checked-in-within", "", "checked in within this duration (30m, 12h, 7d)")
	f.StringVar(&notCheckedIn, "not-checked-in-within", "", "not checked in for longer than this duration (stale devices; 30d)")
	f.StringVar(&registered, "registered-within", "", "registered within this duration (30m, 12h, 7d)")
	return cmd
}

func deviceWithAppCmd() *cobra.Command {
	var opts searchOpts
	var platform, signature, installType string
	var versions []string
	cmd := &cobra.Command{
		Use:   "with-app <bundle-id>",
		Short: "List devices that have an app in their app inventory",
		Long: `List devices whose app inventory contains the app with this bundle or
package id (GET /api/v1/device with type=APP_INVENTORY). --version narrows to
app versions (repeatable, ORed).

--signature matches one exact app build: the SHA-256 signature of an app
inventory entry, computed from identifier, platform, version, display
version, source, install type, and custom attributes (plus name, files, and
size for in-house apps). --installation-type filters by install type, e.g.
INSTALLED.

Here --fq filters app inventory fields (version, bundleId, displayVersion,
...) and is ANDed with the bundle id and versions.`,
		Example: `  ivanti-neurons device with-app com.android.mi.email --platform android --version 303
  ivanti-neurons device with-app com.example.app --platform ios --installation-type INSTALLED --limit 0 -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			versions, err := commaList("version", versions)
			if err != nil {
				return err
			}
			clauses := []string{"bundleId='" + args[0] + "'"}
			if len(versions) > 0 {
				vs := make([]string, len(versions))
				for i, v := range versions {
					vs[i] = "version='" + v + "'"
				}
				clauses = append(clauses, "("+strings.Join(vs, " OR ")+")")
			}
			opts.fq = deviceFq(opts.fq, clauses)
			return deviceSearch(cmd, &opts, params("type", "APP_INVENTORY", "platformType", strings.ToUpper(platform),
				"signature", signature, "installationType", installType))
		},
	}
	opts.register(cmd)
	f := cmd.Flags()
	f.StringVar(&platform, "platform", "", "app platform: IOS or ANDROID (required)")
	f.StringArrayVar(&versions, "version", nil, "app version (repeatable or comma-separated)")
	f.StringVar(&signature, "signature", "", "app inventory signature (SHA-256 hex) of one exact app build")
	f.StringVar(&installType, "installation-type", "", "installation type, e.g. INSTALLED")
	_ = cmd.MarkFlagRequired("platform")
	return cmd
}

func deviceViewCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "view <id|guid>",
		Aliases: []string{"get", "show"},
		Short:   "Show a device's details",
		Long: `Show a device by numeric id or GUID: key fields by default, every field
(device groups, location, battery, ...) with --all.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cols := deviceViewCols
			if all {
				cols = nil
			}
			return deviceGet(cmd, args[0], "", true, cols)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "print every field")
	return cmd
}

func deviceDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>...",
		Short: "Delete retired devices",
		Long: `Delete device records permanently. Retire the devices first: the API
refuses devices in the ACTIVE or WIPE_PENDING state. Irreversible; asks for
confirmation unless --yes.`,
		Example: `  ivanti-neurons device delete 1007966 1007967 --yes`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseIDs(args)
			if err != nil {
				return err
			}
			if err := confirm(cmd, fmt.Sprintf("delete %d device(s)", len(ids))); err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodDelete, "/api/v1/device", url.Values{"deviceIds": ids}, nil)
			if err != nil {
				return err
			}
			// The result is the deleted count, read like an action's.
			return actionReport(cmd, actionSpec{done: "Deleted %d device(s)"}, raw, len(ids))
		},
	}
}

func deviceAssignCmd() *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "assign <device-id>... --to <user-id>",
		Short: "Reassign devices to another user",
		Long: `Assign devices to another user (account id). Supported on supervised iOS
devices and on Android Enterprise devices with secure reassignment (needs
SUEM-Premium), which clears the previous user's app data; hence the
confirmation prompt.

Reassignment is asynchronous: the device's "Last Reassignment Status" goes
from INITIATED to SUCCESS or FAILED. Ineligible devices are rejected; -o
json lists them.`,
		Example: `  ivanti-neurons device assign 73542 --to 10542407 --yes`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseIDs(args)
			if err != nil {
				return err
			}
			account, err := parseID(to, false)
			if err != nil {
				return err
			}
			if err := confirm(cmd, fmt.Sprintf("reassign %d device(s) to user %s", len(ids), account)); err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodPut, "/api/v1/device/updateAccount",
				url.Values{"accountId": {account}, "deviceIds": ids}, nil)
			if err != nil {
				return err
			}
			_, rejected, err := parsePage(raw)
			if err != nil {
				return fmt.Errorf("device assign: %w", err)
			}
			msg := fmt.Sprintf("Reassignment initiated for %d of %d device(s)", len(ids)-rejected, len(ids))
			if rejected > 0 {
				msg += fmt.Sprintf("; %d rejected as ineligible (-o json lists them)", rejected)
			}
			return report(cmd, raw, msg)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "user (account) id to assign the devices to (required)")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func deviceAttrsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "attrs",
		Aliases: []string{"attributes"},
		Short:   "Get and change a device's custom attributes",
		Long: `Custom device attributes are multi-valued: pass "name=value", repeating a
name for several values. Numeric attributes need a "_#" suffix on the name
("rollNumber_#=42"). Devices are given by numeric id or GUID.`,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "get <id|guid>",
			Short: "Show a device's custom attributes",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				id, err := parseID(args[0], true)
				if err != nil {
					return err
				}
				client, err := newClient()
				if err != nil {
					return err
				}
				raw, err := client.Get(cmd.Context(), "/api/v1/device/"+id+"/customattributes", nil)
				if err != nil {
					return err
				}
				if flagOutput != "json" {
					// One "name: values" line per attribute.
					var v struct {
						Attrs json.RawMessage `json:"attrs"`
					}
					if json.Unmarshal(raw, &v) == nil && v.Attrs != nil {
						raw = v.Attrs
					}
				}
				return render(cmd, raw, nil)
			},
		},
		deviceAttrsWriteCmd(&cobra.Command{
			Use:   "set",
			Short: "Set custom attributes, keeping the others",
			Long: `Set the given custom attributes on a device (PATCH): their values are
replaced (--append adds to them instead); attributes not named are kept.`,
			Example: `  ivanti-neurons device attrs set 22000 cityId=SF carrier=att
  ivanti-neurons device attrs set 57d9903c-aadf-4b40-aef3-e1f24302f180 tag=a tag=b --append`,
		}, http.MethodPatch, "Set %d attribute(s) on device %s"),
		deviceAttrsWriteCmd(&cobra.Command{
			Use:   "replace",
			Short: "Replace all custom attributes of a device",
			Long: `Replace all custom attributes of a device with the given ones (POST):
attributes not named are deleted. Asks for confirmation unless --yes.`,
			Example: `  ivanti-neurons device attrs replace 22000 cityId=SF --yes`,
		}, http.MethodPost, "Replaced all attributes of device %[2]s with %[1]d attribute(s)"),
		deviceAttrsWriteCmd(&cobra.Command{
			Use:   "remove",
			Short: "Remove custom attribute values from a device",
			Long: `Remove the given attribute values from a device; others stay. Asks for
confirmation unless --yes. To drop whole attributes from several devices,
use "device attrs remove-keys".`,
			Example: `  ivanti-neurons device attrs remove d67f2d3b-4ea7-44c6-9fb7-5f4107184e34 attribute1=1 --yes`,
		}, http.MethodDelete, "Removed %d attribute(s) from device %s"),
		deviceAttrsRemoveKeysCmd(),
	)
	return cmd
}

// deviceAttrsWriteCmd completes a custom attribute write command: the
// {"attrs": {...}} body goes to /api/v1/device/{id|guid}/customattributes
// with method. done formats the success message (count, device).
func deviceAttrsWriteCmd(cmd *cobra.Command, method, done string) *cobra.Command {
	var appendValues bool
	cmd.Use += " <id|guid> name=value..."
	cmd.Args = cobra.MinimumNArgs(2)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], true)
		if err != nil {
			return err
		}
		attrs, err := parseAttrs(args[1:])
		if err != nil {
			return err
		}
		body := map[string]any{"attrs": attrs}
		if method == http.MethodPatch {
			body["forceOverwrite"] = !appendValues
		} else if err := confirm(cmd, fmt.Sprintf("%s custom attributes of device %s", cmd.Name(), id)); err != nil {
			return err
		}
		client, err := newClient()
		if err != nil {
			return err
		}
		raw, err := client.Call(cmd.Context(), method, "/api/v1/device/"+id+"/customattributes", nil, body)
		if err != nil {
			return err
		}
		return report(cmd, raw, fmt.Sprintf(done, len(attrs), id))
	}
	if method == http.MethodPatch {
		cmd.Flags().BoolVar(&appendValues, "append", false, "append the values to existing ones instead of replacing them")
	}
	return cmd
}

func deviceAttrsRemoveKeysCmd() *cobra.Command {
	var keys []string
	cmd := &cobra.Command{
		Use:   "remove-keys <device-id>... --key <name>",
		Short: "Remove whole custom attributes from several devices",
		Long: `Remove custom attributes (all their values) from devices given by numeric
id. Asks for confirmation unless --yes.`,
		Example: `  ivanti-neurons device attrs remove-keys 73542 73543 --key ForDocs001 --key forDocs002 --yes`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseIDs(args)
			if err != nil {
				return err
			}
			keys, err := commaList("key", keys)
			if err != nil {
				return err
			}
			if err := confirm(cmd, fmt.Sprintf("remove %d attribute(s) from %d device(s)", len(keys), len(ids))); err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodDelete, "/api/v1/device/customattributes", nil,
				map[string]any{"ids": ids, "attrKeys": keys})
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Removed %d attribute(s) from %d device(s)", len(keys), len(ids)))
		},
	}
	cmd.Flags().StringArrayVar(&keys, "key", nil, "attribute name to remove (repeatable or comma-separated)")
	_ = cmd.MarkFlagRequired("key")
	return cmd
}

func deviceConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "config",
		Aliases: []string{"configs"},
		Short:   "List a device's configurations; exclude, re-push, reinstall, clear errors",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list <device-id>",
		Short: "List the configurations assigned to a device and their install status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return deviceGet(cmd, args[0], "/configs", false, []string{"id", "uuid", "name", "policyType", "status"})
		},
	})
	for _, c := range []struct {
		cmd          *cobra.Command
		method, path string
		done         string
	}{
		{&cobra.Command{
			Use:   "exclude",
			Short: "Exclude configurations from a device (uninstalls them or makes them inactive)",
			Long: `Exclude configurations from a device: they are uninstalled or become
inactive. "device config include" pushes them again.`,
		}, http.MethodPut, "uninstallconfigs", "Excluded"},
		{&cobra.Command{
			Use:   "include",
			Short: "Push excluded configurations to a device again",
		}, http.MethodDelete, "uninstallconfigs", "Re-included"},
		{&cobra.Command{
			Use:   "reinstall",
			Short: "Reinstall configurations on a device",
		}, http.MethodPut, "reinstallconfigs", "Reinstalling"},
	} {
		c.cmd.Use += " <device-id> <config-id>..."
		c.cmd.Args = cobra.MinimumNArgs(2)
		c.cmd.RunE = func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], false)
			if err != nil {
				return err
			}
			configs, err := parseIDs(args[1:])
			if err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), c.method, "/api/v1/device/"+id+"/"+c.path, url.Values{"policyIds": configs}, nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("%s %d configuration(s) on device %s", c.done, len(configs), id))
		}
		cmd.AddCommand(c.cmd)
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "clear-error <device-id> <config-uuid>",
		Short: "Clear a configuration's error state on a device",
		Long: `Clear the error state of a configuration on a device. The configuration is
given by its uuid ("device config list" shows it).`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], false)
			if err != nil {
				return err
			}
			if !isGUID(args[1]) {
				return fmt.Errorf("invalid configuration uuid %q", args[1])
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			space, err := spaceID(cmd, client)
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodPut, "/api/v1/device/clearConfigError",
				params("deviceId", id, "policyUuid", args[1], "dmPartitionId", space), nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, "Cleared configuration error on device "+id)
		},
	})
	return cmd
}

func deviceComplianceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compliance",
		Short: "Device compliance status, compliance policies, and vendor compliance sync",
	}
	var opts searchOpts
	policies := &cobra.Command{
		Use:     "policies",
		Short:   "List compliance policies with their violating device counts",
		Example: `  ivanti-neurons device compliance policies --sort violationDeviceCount:desc`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			space, err := spaceID(cmd, client)
			if err != nil {
				return err
			}
			return runSearch(cmd, client, "/api/v2/policy/filter", &opts, params("tag", "RULE", "dmPartitionId", space),
				[]string{"id", "name", "policyRuleType", "enabled", "violationDeviceCount"})
		},
	}
	opts.register(policies)

	var vendor string
	sync := &cobra.Command{
		Use:     "sync <device-id>... --vendor <GoogleBC|Intune>",
		Short:   "Sync devices' compliance status to Google BeyondCorp or Microsoft Intune",
		Example: `  ivanti-neurons device compliance sync 800 802 --vendor Intune`,
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
			raw, err := client.Call(cmd.Context(), http.MethodPost, "/api/v1/device/compliance/sync",
				url.Values{"vendor": {vendor}, "ids": ids}, nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Compliance sync with %s requested for %d device(s)", vendor, len(ids)))
		},
	}
	sync.Flags().StringVar(&vendor, "vendor", "", "compliance vendor: GoogleBC or Intune (required)")
	_ = sync.MarkFlagRequired("vendor")

	cmd.AddCommand(&cobra.Command{
		Use:   "status <device-id>",
		Short: "Show a device's compliance with each compliance policy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return deviceGet(cmd, args[0], "/policies", false, []string{"id", "name", "policyRuleType", "compliant", "pendingCompliant"})
		},
	}, policies, sync)
	return cmd
}

func deviceCertsCmd() *cobra.Command {
	var opts searchOpts
	cmd := &cobra.Command{
		Use:   "certs <device-id>",
		Short: "List certificates issued to a device",
		Long: `List the certificates issued to a device. The API scopes the search to the
device owner, so the device is looked up first for its owner id.`,
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
			raw, err := client.Get(cmd.Context(), "/api/v1/device/"+id, nil)
			if err != nil {
				return err
			}
			var dev struct {
				OwnerID json.Number `json:"ownerId"`
			}
			if err := json.Unmarshal(raw, &dev); err != nil {
				return fmt.Errorf("decode device: %w", err)
			}
			return runSearch(cmd, client, "/api/v1/device/"+id+"/certs/search", &opts, params("accountId", dev.OwnerID.String()),
				[]string{"caName", "subject", "certState", "certSerialNumberString", "notAfter"})
		},
	}
	opts.register(cmd)
	return cmd
}

func devicePredicateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "predicate",
		Short: "Create and update device predicates",
		Long: `Predicates are named device conditions written in the predicate query
language, e.g. @status(device.model.family) == 'iPad'.`,
	}
	cmd.AddCommand(devicePredicateWriteCmd(false), devicePredicateWriteCmd(true))
	return cmd
}

func devicePredicateWriteCmd(update bool) *cobra.Command {
	var name, description, query string
	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Create a predicate",
		Example: `  ivanti-neurons device predicate create --name ipads --query "@status(device.model.family) == 'iPad'"`,
		Args:    cobra.NoArgs,
	}
	method, verb := http.MethodPost, "Created"
	if update {
		cmd.Use, cmd.Short, cmd.Args = "update <id>", "Update a predicate (send every field)", cobra.ExactArgs(1)
		cmd.Example = `  ivanti-neurons device predicate update 65 --name ipads --query "@status(device.model.family) == 'iPad'"`
		method, verb = http.MethodPut, "Updated"
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		p := map[string]any{"name": name, "description": description, "query": query}
		if update {
			id, err := parseID(args[0], false)
			if err != nil {
				return err
			}
			p["id"] = json.Number(id)
		}
		client, err := newClient()
		if err != nil {
			return err
		}
		raw, err := client.Call(cmd.Context(), method, "/api/v1/predicate", nil, []map[string]any{{"predicate": p}})
		if err != nil {
			return err
		}
		return report(cmd, raw, verb+" predicate "+name)
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "predicate name (required)")
	f.StringVar(&query, "query", "", "predicate expression (required)")
	f.StringVar(&description, "description", "", "description")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("query")
	return cmd
}

func deviceLdapSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ldap-sync <ldap-server-id>",
		Short: "Sync an LDAP server",
		Long: `Trigger a sync with an LDAP server (PUT /api/v1/ldapserver/{id}/sync).
Pass the LDAP server id (not a device id). -o json prints the server
settings as returned, which can include the bind password.`,
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
			raw, err := client.Call(cmd.Context(), http.MethodPut, "/api/v1/ldapserver/"+id+"/sync", nil, nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, "LDAP sync started for server "+id)
		},
	}
}

func deviceTeamViewerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "teamviewer <device-id>",
		Short: "Add a device to the TeamViewer account",
		Long: `Add a device to the tenant's TeamViewer account. Prints the authorization
URL the API returns; open it in a browser to complete the connection.`,
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
			raw, err := client.Call(cmd.Context(), http.MethodPost, "/api/v1/teamviewer/"+id, nil, nil)
			if err != nil {
				return err
			}
			var r struct {
				RedirectURI string `json:"redirectUri"`
			}
			_ = json.Unmarshal(raw, &r)
			return report(cmd, raw, r.RedirectURI)
		},
	}
}
