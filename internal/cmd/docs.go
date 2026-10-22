package cmd

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// newDocsCmd prints the CLI reference in one shot. Intended for piping into
// LLM/agent context: `ivanti-neurons docs` yields everything needed to
// operate the tool without further --help calls; `docs <command>` narrows
// it to one command group.
func newDocsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "docs [command...]",
		Short: "Print the CLI reference (optimized for LLMs/agents); docs <command> for one group",
		Example: `  ivanti-neurons docs              # everything: concepts, ids, recipes, all commands
  ivanti-neurons docs device       # only the device commands
  ivanti-neurons docs app config`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			root := cmd.Root()
			if len(args) > 0 {
				sub, rest, err := root.Find(args)
				if err != nil || len(rest) > 0 || sub == root {
					return fmt.Errorf("no command %q: run \"ivanti-neurons docs\" for all commands", strings.Join(args, " "))
				}
				printCmdDocs(out, sub)
				return nil
			}
			fmt.Fprint(out, docsHeader)
			printCmdDocs(out, root)
			fmt.Fprint(out, docsFooter)
			return nil
		},
	}
}

const docsHeader = `# ivanti-neurons CLI reference

Command-line client for Ivanti Neurons for MDM (formerly MobileIron Cloud).
Commands are non-interactive (except a bare "auth login" and the confirmation
prompt of destructive commands, skipped with --yes); output goes to stdout,
errors to stderr with exit code 1.

## Setup

Authentication (pick one):
- Config file: "ivanti-neurons auth login --url <URL> --username <U> --password <P>"
  (or --client-id/--client-secret for OAuth2) once; "auth status" checks it.
- Environment: IVANTI_URL plus IVANTI_USERNAME and IVANTI_PASSWORD, or
  IVANTI_CLIENT_ID and IVANTI_CLIENT_SECRET (overrides the config file).
The API user needs the "API" role plus the roles for the calls it makes.
Basic auth fails for users with MFA; use an OIDC client instead.

## Safety tags

Every command below is tagged [read] (no changes), [write] (changes the
tenant or notifies users), or [destructive] (irreversible or removes data;
asks for confirmation, --yes skips it). On device actions, --check runs only
the viability check: read-only, except "device relinquish --check" (a PUT).

## Conventions

- Output: -o json prints raw API objects; list commands print one JSON array
  of all fetched results, other commands the API's "result" object. Tables
  show timestamps as UTC dates (columns marked "(UTC)"), nested objects as
  names or key=value pairs, and truncate long cells: use -o json to parse.
- Lists page automatically (the API caps pages at 500 rows). Shared list
  flags: -q/--query (full text), --fq (filter, below), --sort (a field of
  the -o json output, ":desc" to reverse), --limit (default 50, 0 = all).
  "Showing N of M" on stderr means more exist; "No results." goes to stderr
  while -o json prints [].
- Device space: most calls are scoped to a device space (API: dmPartitionId).
  The tenant default is used unless --space <id|name> or IVANTI_SPACE is set
  ("space list"). The App API works on the default space only.
- Several ids: space- or comma-separated. Ivanti answers HTTP 403 (not 404)
  for many ids that do not exist; check the id kind first (table below).
- --verbose logs each HTTP request to stderr.

## Ids: where they come from, what takes them

  device id          device list (ID)             device view/actions/config/attrs/certs, app inventory device
  device GUID        device view (guid)           device view, device attrs, device checkin/lock (not --check), app install devices
  account id         user list (ID)               user view/update/delete/invite/pin/attrs/add-to-group, device assign --to, device list --owner-id
  UID                user list (UID, the email)   user view/update/exists, device list --user
  user group id      group list (ID)              group members, user add-to-group --group-id
  device group id    device-group list/find       device-group view/devices/members, config assign, app available device-group
  space id           space list (ID)              --space (a space name works too)
  app id             app list (ID)                app view/delete, app attrs --app; app config/distribution resolve it to the latest version
  app version id     app view (APPLICATIONVERSIONID)  app config, app distribution, app attrs, app inhouse, app install dist
  app config id      app config list (ID)         app config get/set/update
  configuration id   config list (ID); device config list (ID, UUID)  config assign; device config exclude/include/reinstall; clear-error takes the UUID
  bulk profile id    bulk list (CSVID)            bulk get/info/update/device/...
  attribute def id   attribute list (ID)          attribute update/delete

## Common questions

  Stale devices (no check-in for 30 days)   device list --not-checked-in-within 30d
  A user's devices                          user list -q <name>, then device list --user <UID> (or --owner-id <account id>)
  Everything about one device               device view <id>; device config list <id>; device compliance status <id>; app inventory device <id>
  Members of a user group                   user list --fq "ACCOUNTGROUP='<group name>'"
  Devices in a device group                 device-group devices <id|name>
  Who gets an app                           app distribution <app-id>
  What a managed app config pushes          app config list <app-id> --type ANDROID_FOR_WORK, then app config get <app-id> <config-id>
  Can an action run on a device             device <action> <id> --check (lists ineligible devices with the reason)
  Tenant overview                           tenant metadata; tenant license; space list; attribute list

## Filter syntax (--fq)

field='value' clauses joined with AND / OR, parentheses for grouping.
Operators: =, !=, <, >, EQ. Fields are the -o json keys (case-insensitive);
not every key is filterable (e.g. deviceName: use -q instead).
  platformType='IOS' AND registrationState='ACTIVE'
  ownerId='10542407'                   (devices of an account id)
  lastCheckin<'01:00:00'               (checked in within the last hour; hh:mm:ss or d:hh:mm:ss)
  lastCheckin>'30:00:00:00'            (not checked in for over 30 days)
  CFDEVICE[cityId]='SF'                (custom device attribute; numeric ones take a "_#" name suffix)
  CFLDAP[department]='Sales'           (LDAP attribute)
  UID='jdoe@example.com'               (users; also EMAILADDRESS, ACCOUNTGROUP='<group name>')
Values are not escaped: a ' inside a value breaks the filter.

## Commands

`

const docsFooter = `
## Raw API access

"ivanti-neurons api METHOD /api/v1/..." reaches any endpoint with
authentication handled. -f key=value pairs become query parameters
(repeatable; Ivanti write endpoints accept them like form fields). --data
sends a JSON body (inline, @file, or - for stdin). The full response
envelope is printed. Endpoint reference:
https://help.ivanti.com/mi/help/en_us/CLD/8x/api/default.htm
`

// docsReadOnly and docsDestructive classify commands for the safety tags;
// everything else is [write]. A test checks that every path exists.
var (
	docsReadOnly = []string{
		"auth status", "docs",
		"tenant metadata", "tenant status", "tenant license", "space list",
		"config list", "config sentry", "connector list", "attribute list",
		"cert crl", "cert revocation-pending", "cert revoked-on", "dep profiles",
		"device list", "device with-app", "device view", "device attrs get", "device config list",
		"device compliance status", "device compliance policies", "device certs", "device kiosk-users",
		"user list", "user view", "user exists", "user me", "user metadata", "user search",
		"user pin view", "user report list", "user report download", "user ldap-import-status",
		"group list", "group check-delete",
		"device-group list", "device-group view", "device-group find", "device-group devices",
		"app list", "app view", "app categories", "app distribution", "app filters",
		"app available user", "app available device", "app available device-group", "app eligible",
		"app inventory list", "app inventory device", "app store search", "app store view", "app store tracks",
		"app inhouse profiles", "app task list", "app task status", "app review list",
		"app config list", "app config get", "app config types", "app attrs get",
		"bulk list", "bulk get", "bulk info", "bulk download", "bulk qrcode", "bulk device list",
	}
	docsDestructive = []string{
		"device delete", "device wipe", "device retire", "device relinquish", "device logout", "device assign",
		"device attrs replace", "device attrs remove", "device attrs remove-keys",
		"user delete", "user remove-from-group", "user attrs delete", "user attrs clear",
		"group delete", "group members set", "device-group members set", "device-group members remove",
		"attribute delete", "app delete", "app review delete", "app attrs set", "app attrs delete",
		"bulk delete", "bulk device delete", "bulk refresh-token", "cert clear-keys", "oidc reset-secret",
	}
)

// docsTag returns a command's safety tag.
func docsTag(c *cobra.Command) string {
	path := strings.TrimPrefix(c.CommandPath(), c.Root().Name()+" ")
	tag := "write"
	switch {
	case path == "api":
		return "[read or write: by HTTP method]"
	case slices.Contains(docsReadOnly, path):
		tag = "read"
	case slices.Contains(docsDestructive, path):
		tag = "destructive"
	}
	if c.Flags().Lookup("check") != nil {
		if path == "device relinquish" {
			return "[" + tag + "; --check is a PUT]"
		}
		return "[" + tag + "; --check is read-only]"
	}
	return "[" + tag + "]"
}

// docsListFlags are the shared list flags, explained once in Conventions.
var docsListFlags = []string{"query", "fq", "sort", "limit"}

// printCmdDocs walks the command tree and prints group descriptions and,
// for every command, its tag, usage, description, examples, and flags.
func printCmdDocs(w io.Writer, c *cobra.Command) {
	if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
		return
	}
	switch {
	case c.HasSubCommands():
		if c.Long != "" && c.HasParent() {
			// group commands carry the concepts their subcommands share
			fmt.Fprintf(w, "### %s (group)\n%s\n\n", c.CommandPath(), c.Long)
		}
	case c.Runnable() && c.Name() != "docs":
		fmt.Fprintf(w, "### %s %s\n", c.CommandPath(), docsTag(c))
		fmt.Fprintf(w, "Usage: %s\n", c.UseLine())
		desc := c.Long
		if desc == "" {
			desc = c.Short
		}
		if desc != "" {
			fmt.Fprintln(w, desc)
		}
		if c.Example != "" {
			fmt.Fprintf(w, "Examples:\n%s\n", c.Example)
		}
		flags := c.NonInheritedFlags()
		list := true
		for _, name := range docsListFlags {
			list = list && flags.Lookup(name) != nil
		}
		own := pflag.NewFlagSet(c.Name(), pflag.ContinueOnError)
		flags.VisitAll(func(f *pflag.Flag) {
			if !list || !slices.Contains(docsListFlags, f.Name) {
				own.AddFlag(f)
			}
		})
		if list {
			fmt.Fprintln(w, "List flags: -q/--query, --fq, --sort, --limit (see Conventions)")
		}
		if own.HasAvailableFlags() {
			fmt.Fprintf(w, "Flags:\n%s", own.FlagUsages())
		}
		fmt.Fprintln(w)
	}
	for _, sub := range c.Commands() {
		printCmdDocs(w, sub)
	}
}
