package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/api"
	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Log in to an Ivanti Neurons for MDM tenant and inspect auth status",
	}
	cmd.AddCommand(newAuthLoginCmd(), newAuthStatusCmd(), newAuthLogoutCmd())
	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	var inst config.Instance
	var nameFlag string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate against an Ivanti Neurons for MDM tenant",
		Long: `Authenticate against an Ivanti Neurons for MDM tenant and save the
credentials to the config file (0600).

Two credential types are supported:

  Basic auth (default): a local admin user with the "API" role plus the
  roles for what you want to do (e.g. Device Management, User Management,
  App & Content Management, or the Read Only variants). Create it in the
  admin portal under Admin > Users > Add > Single User, then assign roles.

  OAuth2 client credentials (--client-id/--client-secret): required when
  the API user has MFA enabled (Basic auth then fails). Create an OIDC
  client with "ivanti-neurons oidc create" (or the admin portal) and pass
  its client_id and client_secret.

The URL is your tenant's admin portal host (default: the EU data center,
https://eu1.mobileiron.com; US tenants use e.g. https://na1.mobileiron.com).
A pasted login page URL (https://eu1.mobileiron.com/login.html#!/) works too.`,
		Example: `  ivanti-neurons auth login
  ivanti-neurons auth login --username api@example.com
  ivanti-neurons auth login --url https://na1.mobileiron.com --client-id ... --client-secret ... --name us`,
		RunE: func(cmd *cobra.Command, args []string) error {
			in := bufio.NewReader(cmd.InOrStdin())
			out := cmd.OutOrStdout()
			prompt := func(label string, hidden bool) (string, error) {
				fmt.Fprint(out, label)
				if hidden && term.IsTerminal(int(os.Stdin.Fd())) {
					b, err := term.ReadPassword(int(os.Stdin.Fd()))
					fmt.Fprintln(out)
					return strings.TrimSpace(string(b)), err
				}
				line, err := in.ReadString('\n')
				if errors.Is(err, io.EOF) && line == "" {
					return "", fmt.Errorf("no input to prompt from: run in a terminal, or pass --url with --username/--password (or --client-id/--client-secret)")
				}
				return strings.TrimSpace(line), err
			}

			var err error
			if inst.URL == "" {
				if inst.URL, err = prompt("Ivanti Neurons for MDM URL ["+authDefaultURL+"]: ", false); err != nil {
					return err
				}
			}
			if inst.URL == "" {
				inst.URL = authDefaultURL
			}
			if inst.URL, err = authBaseURL(inst.URL); err != nil {
				return err
			}

			if inst.ClientID != "" {
				if inst.ClientSecret == "" {
					if inst.ClientSecret, err = prompt("Client secret (input hidden): ", true); err != nil {
						return err
					}
				}
			} else {
				if inst.Username == "" {
					if inst.Username, err = prompt("API username: ", false); err != nil {
						return err
					}
				}
				if inst.Password == "" {
					if inst.Password, err = prompt("Password (input hidden): ", true); err != nil {
						return err
					}
				}
			}
			if !inst.HasCredentials() {
				return fmt.Errorf("username+password or client id+secret are required")
			}

			client, err := api.New(inst.URL, api.Auth{
				Username: inst.Username, Password: inst.Password,
				ClientID: inst.ClientID, ClientSecret: inst.ClientSecret,
			})
			if err != nil {
				return err
			}
			client.Verbose = flagVerbose
			client.OnTokenRefresh = func(token string, expiry time.Time) {
				inst.AccessToken, inst.TokenExpiry = token, expiry
			}
			meta, err := client.TenantMetadata(cmd.Context())
			if err != nil {
				return fmt.Errorf("credential check against %s failed: %w", inst.URL, err)
			}
			inst.Space, inst.ContentSpace = meta.DefaultDmPartitionID, meta.DefaultCmPartitionID

			cfg, err := config.Load()
			if err != nil {
				return err
			}
			name := nameFlag
			if name == "" {
				name = "default"
			}
			cfg.Instances[name] = inst
			if cfg.Default == "" {
				cfg.Default = name
			}
			if err := config.Save(cfg); err != nil {
				return err
			}
			p, err := config.Path()
			if err != nil {
				p = "(unknown path)"
			}
			fmt.Fprintf(out, "Logged in to %s (tenant %s, default device space %d)\nConfig written to %s (instance %q)\n",
				inst.URL, meta.TenantGUID, meta.DefaultDmPartitionID, p, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&inst.URL, "url", "", "tenant URL (prompted if omitted)")
	cmd.Flags().StringVar(&inst.Username, "username", "", "API user name (Basic auth; prompted if omitted)")
	cmd.Flags().StringVar(&inst.Password, "password", "", "API user password (Basic auth; prompted if omitted)")
	cmd.Flags().StringVar(&inst.ClientID, "client-id", "", "OIDC client id (OAuth2 client credentials)")
	cmd.Flags().StringVar(&inst.ClientSecret, "client-secret", "", "OIDC client secret (prompted if omitted with --client-id)")
	cmd.Flags().StringVar(&nameFlag, "name", "", `instance name in the config (default "default")`)
	cmd.MarkFlagsMutuallyExclusive("username", "client-id")
	return cmd
}

// authDefaultURL is the tenant host used when none is given (EU data center).
const authDefaultURL = "https://eu1.mobileiron.com"

// authBaseURL reduces a tenant URL to scheme://host, so a pasted login page
// URL (https://eu1.mobileiron.com/login.html#!/) or a bare host works.
func authBaseURL(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid tenant URL %q", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the active instance and verify its credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			meta, err := client.TenantMetadata(cmd.Context())
			if err != nil {
				return fmt.Errorf("instance %s (%s): credential check failed: %w", active.name, active.inst.URL, err)
			}
			who := active.inst.Username
			if who == "" {
				who = "OIDC client " + active.inst.ClientID
			}
			space, err := spaceID(cmd, client)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Instance: %s (%s)\nTenant:   %s (version %s)\nAuth:     %s\nSpace:    %s\n",
				active.name, active.inst.URL, meta.TenantGUID, meta.WebAppVersion.Version, who, space)
			return nil
		},
	}
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the active instance from the config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			name := config.ActiveName(cfg, flagInstance)
			if name == "" {
				return fmt.Errorf("no instance to log out from")
			}
			if _, ok := cfg.Instances[name]; !ok {
				return fmt.Errorf("unknown instance %q", name)
			}
			delete(cfg.Instances, name)
			if cfg.Default == name {
				cfg.Default = ""
				for k := range cfg.Instances {
					cfg.Default = k
					break
				}
			}
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed instance %q from config\n", name)
			return nil
		},
	}
}
