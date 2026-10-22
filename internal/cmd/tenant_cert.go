package cmd

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/api"
	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func certCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "cert", Short: "Download CRLs and revocation lists; upload and clear user-provided certificates"}
	cmd.AddCommand(certCRLCmd(), certPendingCmd(), certRevokedOnCmd(), certUploadCmd(), certClearKeysCmd())
	return cmd
}

// certSegment rejects values that would alter the URL path when used as a
// path segment (tenant id, CA name).
func certSegment(vals ...string) error {
	for _, v := range vals {
		if v == "" || v == "." || v == ".." || strings.Contains(v, "/") {
			return fmt.Errorf("invalid path value %q", v)
		}
	}
	return nil
}

func certCRLCmd() *cobra.Command {
	var pem bool
	var out string
	cmd := &cobra.Command{
		Use:   "crl <tenant-id> <ca-name>",
		Short: "Download a certificate authority's CRL",
		Long: `Download the Certificate Revocation List of a certificate authority. The
tenant id is the number in the CA's CRL URL (/c/ca/<tenant-id>/<ca-name>.crl);
pass the plain CA name ("AWS CA", not "AWS%20CA"). With --space (or
IVANTI_SPACE) the space-scoped CRL /c/ca/<tenant-id>/<space-id>/<ca-name>.crl
is fetched.
DER by default, --pem for PEM. Writes to --out (a new file; existing files
are never overwritten) or stdout.`,
		Example: `  ivanti-neurons cert crl 42591 "AWS CA" --out aws-ca.crl
  ivanti-neurons cert crl 42591 "AWS CA" --pem > aws-ca.pem`,
		Args: cobra.ExactArgs(2),
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, args []string) error {
			if err := certSegment(args...); err != nil {
				return err
			}
			path := "/c/ca/" + args[0] + "/"
			if flagSpace != "" || os.Getenv(config.EnvSpace) != "" {
				space, err := spaceID(cmd, c)
				if err != nil {
					return err
				}
				path += space + "/"
			}
			var q url.Values
			if pem {
				q = url.Values{"pem": {"true"}}
			}
			data, err := c.Raw(cmd.Context(), http.MethodGet, path+args[1]+".crl", q, nil)
			if err != nil {
				return err
			}
			return saveFile(cmd, data, out)
		}),
	}
	cmd.Flags().BoolVar(&pem, "pem", false, "PEM instead of DER")
	cmd.Flags().StringVar(&out, "out", "", "write to this new file instead of stdout")
	return cmd
}

func certPendingCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "revocation-pending <ca-name>",
		Short: "List a CA's certificates that must be revoked manually (CSV)",
		Long: `List the certificates issued by an external CA that cannot be revoked
automatically and must be revoked manually (store state Manual_Revoke), as
CSV: caID, caName, certHash, mdmChannelId, notAfter, notBefore,
serialNumber, storeState, subject, usageType. The CA name is the Name
column under Admin > Certificate Authority. Writes to --out (a new file) or
stdout; an empty list is just the header.`,
		Example: `  ivanti-neurons cert revocation-pending attca --out pending.csv`,
		Args:    cobra.ExactArgs(1),
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, args []string) error {
			if err := certSegment(args[0]); err != nil {
				return err
			}
			data, err := c.Raw(cmd.Context(), http.MethodGet, "/api/v1/ca/getCertStatus/"+args[0], params("format", "csv"), nil)
			if err != nil {
				return err
			}
			return saveFile(cmd, data, out)
		}),
	}
	cmd.Flags().StringVar(&out, "out", "", "write to this new file instead of stdout")
	return cmd
}

func certRevokedOnCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "revoked-on <dd-mm-yyyy>",
		Short: "List certificates manually revoked on a date (CSV)",
		Long: `List the certificates manually revoked on a date (dd-mm-yyyy), as CSV with
the same columns as "cert revocation-pending". Writes to --out (a new file)
or stdout.`,
		Example: `  ivanti-neurons cert revoked-on 09-12-2021`,
		Args:    cobra.ExactArgs(1),
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, args []string) error {
			if _, err := time.Parse("02-01-2006", args[0]); err != nil {
				return fmt.Errorf("invalid date %q (want dd-mm-yyyy)", args[0])
			}
			data, err := c.Raw(cmd.Context(), http.MethodGet, "/api/v1/ca/getCertStatusOnDate",
				params("format", "csv", "manuallyRevokeDate", args[0]), nil)
			if err != nil {
				return err
			}
			return saveFile(cmd, data, out)
		}),
	}
	cmd.Flags().StringVar(&out, "out", "", "write to this new file instead of stdout")
	return cmd
}

func certUploadCmd() *cobra.Command {
	var user, password string
	var policy int64
	var fields []string
	cmd := &cobra.Command{
		Use:   "upload <p12-file>",
		Short: "Upload a user-provided certificate (PKCS#12) for a user",
		Long: `Upload a user's certificate package (PKCS#12 .p12 with private key) for an
Identity Certificate configuration of the user-provided type, so the user
does not have to upload it in the self-service portal. --policy is that
configuration's id ("config list"), --user the user's UID (usually the
email), --password the .p12 password (asked for, hidden, when omitted on a
terminal). -f adds other API fields, e.g. -f accountId=10200 instead of
--user.`,
		Example: `  ivanti-neurons cert upload smes.p12 --user jdoe@example.com --policy 35037`,
		Args:    cobra.ExactArgs(1),
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, args []string) error {
			form, err := fieldValues(fields)
			if err != nil {
				return err
			}
			if password == "" && term.IsTerminal(int(os.Stdin.Fd())) {
				fmt.Fprint(cmd.ErrOrStderr(), "Password of the .p12 file (input hidden): ")
				b, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				password = string(b)
			}
			for k, v := range params("accountUid", user, "password", password, "policyId", strconv.FormatInt(policy, 10)) {
				form[k] = v
			}
			body := &api.Multipart{Fields: form, Files: map[string]string{"certData": args[0]}}
			raw, err := c.Call(cmd.Context(), http.MethodPost, "/api/v1/user_provided", nil, body)
			if err != nil {
				return err
			}
			return report(cmd, raw, "Uploaded "+args[0])
		}),
	}
	cmd.Flags().StringVar(&user, "user", "", "UID of the user the certificate belongs to (API: accountUid)")
	cmd.Flags().Int64Var(&policy, "policy", 0, "Identity Certificate (user-provided) configuration id (required)")
	cmd.Flags().StringVar(&password, "password", "", "password of the .p12 file (prompted on a terminal when omitted)")
	cmd.Flags().StringArrayVarP(&fields, "field", "f", nil, "extra key=value API field (repeatable)")
	_ = cmd.MarkFlagRequired("policy")
	return cmd
}

func certClearKeysCmd() *cobra.Command {
	var csvFile string
	var accounts, policies []int64
	cmd := &cobra.Command{
		Use:   "clear-keys",
		Short: "Delete the stored private keys of user-provided certificates",
		Long: `Delete the stored private keys and passwords of user-provided certificate
packages, either by certificate id from a CSV file (--csv: header line
"certificateIds", then one id per line, at most 1000) or by account and
Identity Certificate (user-provided) configuration (--account and --policy,
each repeatable or comma-separated; every account is combined with every
configuration). Irreversible; asks for confirmation. Prints how many keys
were cleared and which ids were invalid.`,
		Example: `  ivanti-neurons cert clear-keys --csv cert-ids.csv
  ivanti-neurons cert clear-keys --account 10200 --policy 35497,35498 --yes`,
		Args: cobra.NoArgs,
		RunE: tenantRun(func(cmd *cobra.Command, c *api.Client, _ []string) error {
			var raw []byte
			var err error
			switch {
			case csvFile != "" && len(accounts)+len(policies) > 0:
				return fmt.Errorf("use either --csv or --account/--policy")
			case csvFile != "":
				if err := confirm(cmd, "clear the private keys of the certificates listed in "+csvFile); err != nil {
					return err
				}
				raw, err = c.Call(cmd.Context(), http.MethodDelete, "/api/v1/user_provided/clearPrivateKey/csv", nil,
					&api.Multipart{Files: map[string]string{"csv-data": csvFile}})
			case len(accounts) > 0 && len(policies) > 0:
				wrappers := make([]map[string]any, len(accounts))
				for i, a := range accounts {
					wrappers[i] = map[string]any{"accountId": a, "userProvidedCertificatePolicyIds": policies}
				}
				if err := confirm(cmd, fmt.Sprintf("clear the user-provided certificate private keys of %d account(s)", len(accounts))); err != nil {
					return err
				}
				raw, err = c.Call(cmd.Context(), http.MethodDelete, "/api/v1/user_provided/clearPrivateKey", nil,
					map[string]any{"accountAndUPPolicyWrappers": wrappers})
			default:
				return fmt.Errorf("pass --csv <file>, or --account and --policy")
			}
			if err != nil {
				return err
			}
			return render(cmd, raw, nil)
		}),
	}
	cmd.Flags().StringVar(&csvFile, "csv", "", "CSV file of certificate ids (header: certificateIds)")
	cmd.Flags().Int64SliceVar(&accounts, "account", nil, "account id (repeatable)")
	cmd.Flags().Int64SliceVar(&policies, "policy", nil, "Identity Certificate (user-provided) configuration id (repeatable)")
	return cmd
}
