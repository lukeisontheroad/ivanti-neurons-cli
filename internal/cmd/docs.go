package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// newDocsCmd prints the complete CLI reference in one shot. Intended for
// piping into LLM/agent context: `ivanti-neurons docs` yields everything
// needed to operate the tool without further --help calls.
func newDocsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "docs",
		Short: "Print the complete CLI reference (optimized for LLMs/agents)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprint(out, docsHeader)
			printCmdDocs(out, cmd.Root())
			fmt.Fprint(out, docsFooter)
			return nil
		},
	}
}

const docsHeader = `# ivanti-neurons CLI reference

Command-line client for Ivanti Neurons for MDM (formerly MobileIron Cloud).
All commands are non-interactive (except a bare "auth login" and the
confirmation prompt of destructive commands, skipped with --yes) and print
to stdout; errors go to stderr with exit code 1, success is exit code 0.

## Setup

Authentication (pick one):
- Config file: run "ivanti-neurons auth login --url <URL> --username <U> --password <P>"
  (or --client-id/--client-secret for OAuth2) once.
- Environment: set IVANTI_URL plus IVANTI_USERNAME and IVANTI_PASSWORD, or
  IVANTI_CLIENT_ID and IVANTI_CLIENT_SECRET (overrides the config file).
The API user needs the "API" role plus the roles for the calls it makes
(Device Management, User Management, App & Content Management, System
Management, or their Read Only variants). Basic auth fails for users with
MFA; use an OIDC client (OAuth2 client credentials) instead.

## Conventions

- Device space: most calls are scoped to a device space (API parameter
  dmPartitionId). The tenant default is used unless --space <id|name> or
  IVANTI_SPACE is set. "ivanti-neurons space list" shows all spaces.
- Every read command supports "-o json", printing the raw API objects
  (the "result" member of the API's {"errors", "result"} envelope; list and
  search commands print a JSON array of all fetched results). Prefer it
  when consuming output programmatically; tables truncate long values and
  show epoch-millisecond timestamps as UTC dates.
- List/search commands page automatically (the API caps rows at 500 per
  request); --limit caps the total (default 50, 0 = all). A "Showing N of
  M" note goes to stderr when results were cut.
- Device and user arguments accept numeric ids or GUIDs where the API does.
  Commands taking several ids accept them space- or comma-separated.
- Destructive commands (wipe, retire, delete, ...) prompt for confirmation;
  pass --yes (-y) in scripts. Without a terminal they refuse unless --yes.
- "--verbose" logs each HTTP request to stderr for debugging.

## Search and filter syntax (--query, --fq)

--query/-q is a free-text term matched across the main fields.
--fq is a filter expression: field='value' clauses joined with AND/OR, e.g.
  platformType='IOS' AND registrationState='ACTIVE'
  lastCheckin<'01:00:00'             (checked in within the last hour: hh:mm:ss)
  lastRegistrationTime<'7:00:00:00'  (registered within 7 days: d:hh:mm:ss)
  CFDEVICE[cityId]='SF'              (custom device attribute)
  CFLDAP[department]='Sales'         (LDAP attribute)
  UID='jdoe@example.com'             (users)
Numeric custom attributes take a "_#" suffix on the name.
--sort field[:asc|desc] sorts results (e.g. --sort lastCheckin:desc).

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

// printCmdDocs walks the command tree and prints usage, description,
// examples, and flags for every runnable command.
func printCmdDocs(w io.Writer, c *cobra.Command) {
	if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
		return
	}
	if c.Runnable() && c.Name() != "docs" {
		fmt.Fprintf(w, "### %s\n", c.CommandPath())
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
		if flags.HasAvailableFlags() {
			fmt.Fprintf(w, "Flags:\n%s", flags.FlagUsages())
		}
		fmt.Fprintln(w)
	}
	for _, sub := range c.Commands() {
		printCmdDocs(w, sub)
	}
}
