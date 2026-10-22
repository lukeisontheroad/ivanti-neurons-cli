// Package cmd implements the ivanti-neurons command tree.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	flagInstance string
	flagOutput   string
	flagVerbose  bool
	flagSpace    string
	flagYes      bool
)

func Execute(version string) error {
	return newRootCmd(version).Execute()
}

// newRootCmd builds the full command tree (also used by tests).
func newRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "ivanti-neurons",
		Short: "Manage Ivanti Neurons for MDM (MobileIron Cloud) from the command line",
		Long: `Manage Ivanti Neurons for MDM (formerly MobileIron Cloud) from the command line:
devices, device actions, users, groups, apps, configurations, certificates,
bulk enrollment, and tenant settings.

Unofficial CLI — not affiliated with, endorsed by, or sponsored by Ivanti.

Authentication: run "ivanti-neurons auth login" once, or set IVANTI_URL plus
IVANTI_USERNAME and IVANTI_PASSWORD (Basic auth, API user with the "API"
role) or IVANTI_CLIENT_ID and IVANTI_CLIENT_SECRET (OAuth2 client
credentials; required when the API user has MFA).

Most calls are scoped to a device space (API: dmPartitionId). The default
space is detected at login; override per call with --space <id|name>.

Every read command supports "-o json" for machine-readable output (raw API
objects). Destructive commands (wipe, retire, delete, ...) ask for
confirmation; pass --yes to skip it in scripts.

Run "ivanti-neurons docs" for the complete reference in one page
(recommended for LLM/agent use).`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&flagInstance, "instance", "", "config instance (profile) to use")
	root.PersistentFlags().StringVarP(&flagOutput, "output", "o", "table", `output format: "table" (human) or "json" (raw API objects, for scripting)`)
	root.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "log HTTP requests to stderr")
	root.PersistentFlags().StringVar(&flagSpace, "space", "", "device space id or name (default: the tenant's default space)")
	root.PersistentFlags().BoolVarP(&flagYes, "yes", "y", false, "skip confirmation prompts for destructive actions")

	root.AddCommand(newAuthCmd())
	root.AddCommand(newDeviceCmd())
	root.AddCommand(newUserCmd())
	root.AddCommand(newGroupCmd())
	root.AddCommand(newDeviceGroupCmd())
	root.AddCommand(newAppCmd())
	root.AddCommand(tenantCmds()...)
	root.AddCommand(newBulkCmd())
	root.AddCommand(newAPICmd())
	root.AddCommand(newDocsCmd())

	return root
}

func validateOutput() error {
	if flagOutput != "table" && flagOutput != "json" {
		return fmt.Errorf("invalid --output %q (want table or json)", flagOutput)
	}
	return nil
}
