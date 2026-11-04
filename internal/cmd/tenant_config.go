package cmd

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"

	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/api"
	"github.com/spf13/cobra"
)

func configViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "view <config-id>",
		Short: "Show a device configuration",
		Long: `Show one device configuration: name, type, distribution type, and whether
it can be deleted. -o json has the full object, e.g. the certificate details
(subject, issuer, validity) of a CERTIFICATE configuration under "views".
Configuration ids: "config list". Who gets it: "config distribution".`,
		Example: `  ivanti-neurons config view 35140`,
		Args:    cobra.ExactArgs(1),
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, args []string) error {
			id, space, err := configIDAndSpace(cmd, c, args[0])
			if err != nil {
				return err
			}
			raw, err := c.Get(cmd.Context(), "/api/v1/policy/"+id, params("dmPartitionId", space))
			if err != nil {
				return err
			}
			return render(cmd, raw, []string{"policyId", "name", "policyType", "description", "enabled",
				"dmPartitionDistributionType", "dmPartitionName", "deletable", "modifiedAt"})
		}),
	}
}

func configDistributionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "distribution <config-id>",
		Short: "Show the devices, device groups, users and user groups a configuration is distributed to",
		Long: `Show the explicit distribution of a device configuration: single devices,
device groups (ruleGroups), users (accounts) and user groups. A
configuration distributed to all devices ("config view":
dmPartitionDistributionType ALL) reaches every device of the space on top of
these.`,
		Example: `  ivanti-neurons config distribution 35140 -o json`,
		Args:    cobra.ExactArgs(1),
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, args []string) error {
			id, space, err := configIDAndSpace(cmd, c, args[0])
			if err != nil {
				return err
			}
			raw, err := c.Get(cmd.Context(), "/api/v1/policy/"+id+"/distribution", params("dmPartitionId", space))
			if err != nil {
				return err
			}
			return render(cmd, raw, []string{"policyId", "devices", "ruleGroups", "accounts", "groups"})
		}),
	}
}

func configCreateCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "create", Short: "Create device configurations"}
	var name, file, description string
	cert := &cobra.Command{
		Use:   "certificate",
		Short: "Create a CERTIFICATE configuration that installs a certificate (e.g. a CA) on devices",
		Long: `Create a device configuration of type CERTIFICATE from a certificate file
(--file, PEM or DER: .cer, .crt, .pem, .der) holding exactly one X.509
certificate, typically a CA certificate devices should trust. The
configuration is created enabled and distributed to no devices; distribute it
with "config distribute" (single devices, users, groups) or "config assign"
(device groups). Prints the new configuration; policyId is its configuration
id.`,
		Example: `  ivanti-neurons config create certificate --name "Corporate Root CA" --file root-ca.cer
  ivanti-neurons config create certificate --name "Corporate Root CA" --file root-ca.cer -o json`,
		Args: cobra.NoArgs,
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, _ []string) error {
			if err := checkCertFile(file); err != nil {
				return err
			}
			space, err := spaceID(cmd, c)
			if err != nil {
				return err
			}
			form := url.Values{"dmPartitionId": {space}, "name": {name}, "description": {description}, "enabled": {"true"},
				"dmPartitionDistributionType": {"NONE"}, "delegatedDistMutableInSpace": {"false"}}
			raw, err := c.Call(cmd.Context(), http.MethodPost, "/api/v1/policy", params("policyType", "CERTIFICATE"),
				&api.Multipart{Fields: form, Files: map[string]string{"certs[0].certData": file}})
			if err != nil {
				return err
			}
			return render(cmd, raw, []string{"policyId", "name", "policyType", "dmPartitionDistributionType"})
		}),
	}
	cert.Flags().StringVar(&name, "name", "", "configuration name (required)")
	cert.Flags().StringVar(&file, "file", "", "certificate file, PEM or DER (required)")
	cert.Flags().StringVar(&description, "description", "", "configuration description")
	_ = cert.MarkFlagRequired("name")
	_ = cert.MarkFlagRequired("file")
	cmd.AddCommand(cert)
	return cmd
}

// checkCertFile accepts exactly one X.509 certificate, PEM or DER, so that
// nothing else in the file (a private key, say) is uploaded.
func checkCertFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if block, rest := pem.Decode(data); block != nil {
		if block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) > 0 {
			return fmt.Errorf("%s must hold exactly one certificate and nothing else (first PEM block: %s)", path, block.Type)
		}
		data = block.Bytes
	}
	if _, err := x509.ParseCertificate(data); err != nil {
		return fmt.Errorf("%s is not a PEM or DER certificate: %w", path, err)
	}
	return nil
}

// distLists are the id lists of a configuration's distribution: the
// "config distribute" flag, the PUT key, and the GET key (objects with "id").
var distLists = []struct{ flag, put, get, what string }{
	{"device", "deviceIds", "devices", `device ids ("device list")`},
	{"device-group", "ruleGroupIds", "ruleGroups", `device group ids ("device-group list")`},
	{"user", "accountIds", "accounts", `account ids ("user list")`},
	{"group", "groupIds", "groups", `user group ids ("group list")`},
}

func configDistributeCmd() *cobra.Command {
	var replace bool
	add := make([][]string, len(distLists))
	cmd := &cobra.Command{
		Use:   "distribute <config-id>",
		Short: "Distribute a device configuration to single devices, device groups, users or user groups",
		Long: `Distribute a device configuration to single devices (--device), device
groups (--device-group), users (--user, account ids) or user groups
(--group). The ids are added to the current distribution ("config
distribution"); --replace makes them the whole distribution instead,
removing everything else, and asks for confirmation. The configuration's
distribution type (dmPartitionDistributionType, e.g. ALL) is not changed.`,
		Example: `  ivanti-neurons config distribute 35140 --device 22184
  ivanti-neurons config distribute 35140 --device-group 38002 --user 10200,10201
  ivanti-neurons config distribute 35140 --device 22184 --replace --yes`,
		Args: cobra.ExactArgs(1),
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, args []string) error {
			id, space, err := configIDAndSpace(cmd, c, args[0])
			if err != nil {
				return err
			}
			lists := make([][]string, len(distLists))
			given := 0
			for i := range distLists {
				if lists[i], err = parseIDs(add[i]); err != nil {
					return err
				}
				given += len(lists[i])
			}
			if given == 0 {
				return fmt.Errorf("nothing to distribute: pass --device, --device-group, --user or --group ids")
			}
			if replace {
				if err := confirm(cmd, "replace the whole distribution of configuration "+id); err != nil {
					return err
				}
			} else if err := mergeDistribution(cmd, c, id, space, lists); err != nil {
				return err
			}
			body := map[string]any{"policyId": json.Number(id), "dmPartitionId": json.Number(space)}
			for i, l := range distLists {
				body[l.put] = jsonNumbers(lists[i])
			}
			raw, err := c.Call(cmd.Context(), http.MethodPut, "/api/v1/policy/distribution", nil, body)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Configuration %s now goes to %d device(s), %d device group(s), %d user(s), %d user group(s)",
				id, len(lists[0]), len(lists[1]), len(lists[2]), len(lists[3])))
		}),
	}
	for i, l := range distLists {
		cmd.Flags().StringSliceVar(&add[i], l.flag, nil, l.what+" to add (repeatable or comma-separated)")
	}
	cmd.Flags().BoolVar(&replace, "replace", false, "make the given ids the whole distribution, removing all others")
	return cmd
}

// mergeDistribution adds the configuration's current distribution to lists.
// The PUT replaces the whole distribution, so an unreadable one stops the
// write rather than dropping existing recipients.
func mergeDistribution(cmd *cobra.Command, c *api.Client, id, space string, lists [][]string) error {
	raw, err := c.Get(cmd.Context(), "/api/v1/policy/"+id+"/distribution", params("dmPartitionId", space))
	if err != nil {
		return err
	}
	var dist map[string]json.RawMessage
	_ = json.Unmarshal(raw, &dist)
	for i, l := range distLists {
		var current []struct {
			ID json.Number `json:"id"`
		}
		if err := json.Unmarshal(dist[l.get], &current); err != nil {
			return fmt.Errorf("cannot read the current distribution of configuration %s (%s): check the id with \"config view\", or pass --replace to set it completely", id, l.get)
		}
		for _, o := range current {
			if !slices.Contains(lists[i], o.ID.String()) {
				lists[i] = append(lists[i], o.ID.String())
			}
		}
	}
	return nil
}

func configDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <config-id>",
		Short: "Delete a device configuration",
		Long: `Delete a device configuration and its distribution. System configurations
cannot be deleted ("config view": deletable false). Irreversible; asks for
confirmation.`,
		Example: `  ivanti-neurons config delete 35140 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, args []string) error {
			id, space, err := configIDAndSpace(cmd, c, args[0])
			if err != nil {
				return err
			}
			if err := confirm(cmd, "delete configuration "+id); err != nil {
				return err
			}
			raw, err := c.Call(cmd.Context(), http.MethodDelete, "/api/v1/policy/"+id, params("dmPartitionId", space), nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, "Deleted configuration "+id)
		}),
	}
}

// configIDAndSpace validates a configuration id argument and resolves the
// device space the configuration calls are scoped to.
func configIDAndSpace(cmd *cobra.Command, c *api.Client, arg string) (string, string, error) {
	id, err := parseID(arg, false)
	if err != nil {
		return "", "", err
	}
	space, err := spaceID(cmd, c)
	return id, space, err
}
