package cmd

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/output"
	"github.com/spf13/cobra"
)

// newAPICmd is the raw API escape hatch.
func newAPICmd() *cobra.Command {
	var fields []string
	var data string
	cmd := &cobra.Command{
		Use:   "api <method> <path>",
		Short: "Make a raw Ivanti Neurons for MDM API request",
		Long: `Make an authenticated request against any API endpoint and print the
response (the full {"errors", "result"} envelope, unmodified).

--field key=value pairs become query parameters for every method (Ivanti
write endpoints bind query parameters like form fields; repeat a key for
list parameters such as ids). --data sends a JSON body: inline JSON,
@file.json, or "-" for stdin. Non-JSON responses (CSV, files) are written
as-is. The device space is not added automatically: pass
-f dmPartitionId=<id> where the endpoint needs it.`,
		Example: `  ivanti-neurons api GET /api/v1/metadata/tenant
  ivanti-neurons api GET /api/v1/device -f dmPartitionId=23000 -f rows=10 -f fq="platformType='IOS'"
  ivanti-neurons api PUT /api/v1/device/forceCheckin -f ids=22062 -f ids=22196
  ivanti-neurons api PATCH /api/v1/device/22000/customattributes --data '{"attrs":{"cityId":["SF"]}}'`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			method := strings.ToUpper(args[0])
			path := args[1]
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			query, err := fieldValues(fields)
			if err != nil {
				return err
			}
			var body any
			if data != "" {
				if body, err = readData(cmd, data); err != nil {
					return err
				}
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			resp, err := client.Raw(cmd.Context(), method, path, query, body)
			if err != nil {
				return err
			}
			if !json.Valid(bytes.TrimSpace(resp)) || len(bytes.TrimSpace(resp)) == 0 {
				_, err = cmd.OutOrStdout().Write(resp)
				return err
			}
			return output.JSON(cmd.OutOrStdout(), json.RawMessage(resp))
		},
	}
	cmd.Flags().StringArrayVarP(&fields, "field", "f", nil, "key=value query parameter (repeatable)")
	cmd.Flags().StringVarP(&data, "data", "d", "", `JSON request body: inline, @file, or "-" for stdin`)
	return cmd
}
