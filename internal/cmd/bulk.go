package cmd

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/api"
	"github.com/spf13/cobra"
)

const bulkPath = "/api/v1/bulkenrollment/profile"

var (
	bulkProfileCols = []string{"csvId", "csvName", "description", "devicesCount", "userName", "withoutToken", "modifiedAt"}
	bulkConfigCols  = []string{"csvId", "enrollmentToken", "tokenExpiry", "serverUrl", "deviceCount", "userName", "userEmailAddress", "userId", "errorMessage", "provisioningConfig"}
	bulkDeviceCols  = []string{"id", "imei", "serialNumber", "manufacturer", "state", "userName", "customAttributes"}
)

func newBulkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bulk",
		Short: "Manage bulk enrollment profiles",
		Long: `Manage Android bulk enrollment profiles. A profile pre-registers devices
(IMEI, serial number, manufacturer, custom attributes) from a CSV file;
the devices then enroll with the profile's enrollment token or QR code
("bulk qrcode"). Profiles are tenant-wide (no device space). All calls
require Tenant Admin credentials.`,
	}
	cmd.AddCommand(bulkListCmd(), bulkCreateCmd(), bulkUpdateCmd(), bulkDownloadCmd(),
		bulkConfigureCmd(), bulkSetExpiryCmd(), bulkSetUserCmd(), bulkDeviceCmd())
	for _, c := range bulkCalls {
		cmd.AddCommand(c.command())
	}
	return cmd
}

// bulkCall is a profile command that makes one bodyless call on
// /profile/{id}<suffix>.
type bulkCall struct {
	use, short, long, example, method, suffix string
	confirm                                   string // confirmation prompt (%s = id); empty: none
	done                                      string // report message (%s = id); empty: render the result
	cols                                      []string
}

var bulkCalls = []bulkCall{
	{use: "get <id>", short: "Show a profile's enrollment configuration (token, expiry, server URL)", method: http.MethodGet, cols: bulkConfigCols},
	{use: "info <id>", short: "Show a profile's name, token, and device counts by state", method: http.MethodGet, suffix: "/info"},
	{
		use: "qrcode <id>", short: "Print the content of a profile's enrollment QR code", method: http.MethodGet, suffix: "/qrcode/generate",
		long: `Print the Android provisioning JSON a device scans to enroll (agent
download location, server, enrollment token, locale, time zone). The API
returns the QR code content, not an image: encode it with any QR tool.
With -o json it is printed as the API returns it (a JSON string).`,
		example: `  ivanti-neurons bulk qrcode 3 | qrencode -o bulk-3.png`,
	},
	{
		use: "refresh-token <id>", short: "Replace a profile's enrollment token with a new one", method: http.MethodPut, suffix: "/refreshtoken",
		long:    "Generate a new enrollment token. The old token and QR code stop working for devices that have not enrolled yet. The token expiry resets to 7 days from now (observed); change it with \"bulk set-expiry\".",
		confirm: "replace the enrollment token of bulk profile %s",
	},
	{
		use: "delete <id>", short: "Delete a bulk enrollment profile", method: http.MethodDelete,
		confirm: "delete bulk enrollment profile %s", done: "Deleted bulk enrollment profile %s",
	},
}

func (b bulkCall) command() *cobra.Command {
	return &cobra.Command{
		Use:     b.use,
		Short:   b.short,
		Long:    b.long,
		Example: b.example,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, client, err := bulkStart(args[0])
			if err != nil {
				return err
			}
			if b.confirm != "" {
				if err := confirm(cmd, fmt.Sprintf(b.confirm, id)); err != nil {
					return err
				}
			}
			raw, err := client.Call(cmd.Context(), b.method, bulkPath+"/"+id+b.suffix, nil, nil)
			if err != nil {
				return err
			}
			if b.done != "" {
				return report(cmd, raw, fmt.Sprintf(b.done, id))
			}
			return render(cmd, raw, b.cols)
		},
	}
}

func bulkListCmd() *cobra.Command {
	var s searchOpts
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List bulk enrollment profiles",
		Example: `  ivanti-neurons bulk list
  ivanti-neurons bulk list -q warehouse --sort modifiedAt:desc`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			return runSearch(cmd, client, bulkPath, &s, nil, bulkProfileCols)
		},
	}
	s.register(cmd)
	return cmd
}

func bulkCreateCmd() *cobra.Command {
	var name, desc, profileType string
	var withoutToken bool
	cmd := &cobra.Command{
		Use:   "create <csv-file>",
		Short: "Create a bulk enrollment profile from a CSV file of devices",
		Long: `Create a bulk enrollment profile by uploading a CSV file of device records.
The CSV header is ("bulk download" returns the same format):
  imei,manufacturer,serial_number,custom_attributes,username
Rows with other headers are silently skipped. Prints the new profile id
(csvId) and its enrollment token.`,
		Example: `  ivanti-neurons bulk create devices.csv --description "Warehouse scanners"
  ivanti-neurons bulk create devices.csv --name scanners --without-token`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				name = filepath.Base(args[0])
			}
			fields := url.Values{"name": {name}, "description": {desc}, "withoutToken": {strconv.FormatBool(withoutToken)}}
			if profileType != "" {
				fields.Set("profileType", profileType)
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodPost, bulkPath+"/upload", nil,
				&api.Multipart{Fields: fields, Files: map[string]string{"csv-data": args[0]}})
			if err != nil {
				return err
			}
			return render(cmd, raw, bulkConfigCols)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "profile name (default: the file name)")
	cmd.Flags().StringVar(&desc, "description", "", "profile description")
	cmd.Flags().BoolVar(&withoutToken, "without-token", false, "devices enroll without an enrollment token")
	cmd.Flags().StringVar(&profileType, "type", "", "profile type: SELF_MAINTAINED (default) or SELF_MAINTAINED_DEVICE_USER")
	return cmd
}

func bulkUpdateCmd() *cobra.Command {
	var name, desc string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Rename a bulk enrollment profile or change its description",
		Long: `Change a profile's name or description (PUT {id, name, description},
observed). The call replaces both fields, so the current values are
loaded and sent back for whichever flag is not given.`,
		Example: `  ivanti-neurons bulk update 3 --name scanners --description "Warehouse scanners"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" && desc == "" && !cmd.Flags().Changed("description") {
				return fmt.Errorf("nothing to change: pass --name and/or --description")
			}
			id, client, err := bulkStart(args[0])
			if err != nil {
				return err
			}
			page, err := client.Search(cmd.Context(), bulkPath, nil, 0)
			if err != nil {
				return err
			}
			var cur struct {
				CSVID       json.Number `json:"csvId"`
				CSVName     string      `json:"csvName"`
				Description string      `json:"description"`
			}
			found := false
			for _, r := range page.SearchResults {
				if json.Unmarshal(r, &cur) == nil && cur.CSVID.String() == id {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("no bulk enrollment profile %s", id)
			}
			if name == "" {
				name = cur.CSVName
			}
			if !cmd.Flags().Changed("description") {
				desc = cur.Description
			}
			body := map[string]any{"id": json.Number(id), "name": name, "description": desc}
			raw, err := client.Call(cmd.Context(), http.MethodPut, bulkPath, nil, body)
			if err != nil {
				return err
			}
			return report(cmd, raw, "Updated bulk enrollment profile "+id)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new profile name")
	cmd.Flags().StringVar(&desc, "description", "", `new description ("" clears it)`)
	return cmd
}

func bulkConfigureCmd() *cobra.Command {
	var data string
	var fields []string
	cmd := &cobra.Command{
		Use:   "configure <id>",
		Short: "Modify a profile's enrollment (QR code) configuration",
		Long: `Modify the enrollment configuration that goes into the profile's QR code.
Unverified: the API guide documents no body schema, and every
shape tried ({"provisioningConfig": {...}}, top-level fields) was
accepted but changed nothing in "bulk qrcode". --data and -f send the body
as given.`,
		Example: `  ivanti-neurons bulk configure 1 --data @config.json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, client, err := bulkStart(args[0])
			if err != nil {
				return err
			}
			body, err := bulkObject(cmd, data, fields, nil)
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodPut, bulkPath+"/"+id+"/qrcode", nil, body)
			if err != nil {
				return err
			}
			return report(cmd, raw, "Updated the enrollment configuration of bulk profile "+id)
		},
	}
	cmd.Flags().StringArrayVarP(&fields, "field", "f", nil, "key=value body field (repeatable)")
	cmd.Flags().StringVarP(&data, "data", "d", "", `JSON object body: inline, @file, or "-" for stdin`)
	return cmd
}

func bulkDownloadCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:     "download <id>",
		Short:   "Download a profile's device records as a file",
		Long:    "Download a profile's device records as the file the API returns (CSV), to stdout or --out.",
		Example: `  ivanti-neurons bulk download 3 --out scanners.csv`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, client, err := bulkStart(args[0])
			if err != nil {
				return err
			}
			data, err := client.Raw(cmd.Context(), http.MethodGet, bulkPath+"/"+id+"/download", nil, nil)
			if err != nil {
				return err
			}
			return saveFile(cmd, data, out)
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "write to this file instead of stdout")
	return cmd
}

func bulkSetExpiryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-expiry <id> <time>",
		Short: "Set when a profile's enrollment token expires",
		Long: `Set the expiry of a profile's enrollment token. <time> is a date
(midnight UTC), an RFC 3339 timestamp, or epoch milliseconds.`,
		Example: `  ivanti-neurons bulk set-expiry 3 2027-12-31
  ivanti-neurons bulk set-expiry 3 2027-12-31T18:00:00+01:00`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, client, err := bulkStart(args[0])
			if err != nil {
				return err
			}
			ms, err := bulkParseTime(args[1])
			if err != nil {
				return err
			}
			// an empty userId keeps the associated user
			raw, err := client.Call(cmd.Context(), http.MethodPut, bulkPath+"/"+id, params("action", "refreshToken"),
				map[string]any{"userId": "", "tokenExpiry": ms})
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Token of bulk profile %s expires %s", id, time.UnixMilli(ms).UTC().Format("2006-01-02 15:04 UTC")))
		},
	}
}

func bulkSetUserCmd() *cobra.Command {
	var expiry string
	cmd := &cobra.Command{
		Use:   "set-user <id> <user-id>",
		Short: "Change the user associated with a bulk profile",
		Long: `Associate a bulk profile with a user. <user-id> is the numeric account id
(find it with the user search commands). The API sets the token expiry in
the same call: --expiry changes it, otherwise the current expiry is kept.`,
		Example: `  ivanti-neurons bulk set-user 3 10052
  ivanti-neurons bulk set-user 3 10052 --expiry 2027-12-31`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, client, err := bulkStart(args[0])
			if err != nil {
				return err
			}
			userID, err := parseID(args[1], false)
			if err != nil {
				return err
			}
			body := map[string]any{"userId": json.Number(userID)}
			if expiry != "" {
				if body["tokenExpiry"], err = bulkParseTime(expiry); err != nil {
					return err
				}
			} else {
				raw, err := client.Get(cmd.Context(), bulkPath+"/"+id, nil)
				if err != nil {
					return err
				}
				var cur map[string]json.RawMessage
				if err := json.Unmarshal(raw, &cur); err != nil {
					return fmt.Errorf("read current token expiry: %w", err)
				}
				if exp := cur["tokenExpiry"]; exp != nil && string(exp) != "null" {
					body["tokenExpiry"] = exp
				}
			}
			raw, err := client.Call(cmd.Context(), http.MethodPut, bulkPath+"/"+id, params("action", "addUser"), body)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Associated bulk profile %s with user %s", id, userID))
		},
	}
	cmd.Flags().StringVar(&expiry, "expiry", "", "also set the token expiry (date, RFC 3339, or epoch ms)")
	return cmd
}

func bulkDeviceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "device",
		Short: "List, add, edit, and delete the device records of a bulk profile",
	}
	cmd.AddCommand(bulkDeviceListCmd(), bulkDeviceAddCmd(), bulkDeviceUploadCmd(), bulkDeviceEditCmd(), bulkDeviceDeleteCmd())
	return cmd
}

func bulkDeviceListCmd() *cobra.Command {
	var s searchOpts
	cmd := &cobra.Command{
		Use:   "list <id>",
		Short: "List or search the device records of a bulk profile",
		Example: `  ivanti-neurons bulk device list 3
  ivanti-neurons bulk device list 3 -q RZ8K314XFY75`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, client, err := bulkStart(args[0])
			if err != nil {
				return err
			}
			return runSearch(cmd, client, bulkPath+"/"+id+"/devices", &s, params("nav", "true"), bulkDeviceCols)
		},
	}
	s.register(cmd)
	return cmd
}

// bulkDevice holds the device record flags of "device add" and "device edit".
type bulkDevice struct{ imei, serial, manufacturer, attrs, data string }

func (d *bulkDevice) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&d.imei, "imei", "", "IMEI")
	cmd.Flags().StringVar(&d.serial, "serial", "", "serial number")
	cmd.Flags().StringVar(&d.manufacturer, "manufacturer", "", `manufacturer, e.g. "Samsung"`)
	cmd.Flags().StringVar(&d.attrs, "attrs", "", `custom attributes: "key=value;key2=value2"`)
	cmd.Flags().StringVarP(&d.data, "data", "d", "", `JSON device record: inline, @file, or "-" for stdin (flags override its fields)`)
}

// body returns the {"devices": [record]} request body; extra fields (the
// record id) are added to the record.
func (d *bulkDevice) body(cmd *cobra.Command, extra map[string]any) (map[string]any, error) {
	rec, err := bulkObject(cmd, d.data, nil, map[string]string{
		"imei": d.imei, "serialNumber": d.serial, "manufacturer": d.manufacturer, "customAttributes": d.attrs,
	})
	if err != nil {
		return nil, err
	}
	for k, v := range extra {
		rec[k] = v
	}
	return map[string]any{"devices": []any{rec}}, nil
}

func bulkDeviceAddCmd() *cobra.Command {
	var d bulkDevice
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Add a device record to a bulk profile",
		Long:  `Add one device record to a profile. For many devices, use "bulk device upload" with a CSV file.`,
		Example: `  ivanti-neurons bulk device add 3 --serial 987634832832 --imei 045923498 \
    --manufacturer "Alcatel Lucent inc" --attrs "isBulk=false;numkey=655734"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, client, err := bulkStart(args[0])
			if err != nil {
				return err
			}
			body, err := d.body(cmd, nil)
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodPost, bulkPath+"/"+id, nil, body)
			if err != nil {
				return err
			}
			return report(cmd, raw, "Added the device record to bulk profile "+id)
		},
	}
	d.register(cmd)
	return cmd
}

func bulkDeviceUploadCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "upload <id> <csv-file>",
		Short:   "Add the devices of a CSV file to a bulk profile",
		Example: `  ivanti-neurons bulk device upload 9 more-devices.csv`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, client, err := bulkStart(args[0])
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodPost, bulkPath+"/"+id+"/upload", nil,
				&api.Multipart{Files: map[string]string{"csv-data": args[1]}})
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Uploaded %s to bulk profile %s", args[1], id))
		},
	}
}

// bulkDeviceRecord returns the editable fields of one device record of a
// profile, in the form the write calls take (customAttributes as
// "key=value;key2=value2"; the list returns them as an object).
func bulkDeviceRecord(cmd *cobra.Command, c *api.Client, profile, recID string) (map[string]any, error) {
	page, err := c.Search(cmd.Context(), bulkPath+"/"+profile+"/devices", params("nav", "true"), 0)
	if err != nil {
		return nil, err
	}
	for _, r := range page.SearchResults {
		var rec struct {
			ID               json.Number       `json:"id"`
			IMEI             string            `json:"imei"`
			SerialNumber     string            `json:"serialNumber"`
			Manufacturer     string            `json:"manufacturer"`
			CustomAttributes map[string]string `json:"customAttributes"`
		}
		if json.Unmarshal(r, &rec) != nil || rec.ID.String() != recID {
			continue
		}
		var attrs []string
		for _, k := range slices.Sorted(maps.Keys(rec.CustomAttributes)) {
			attrs = append(attrs, k+"="+rec.CustomAttributes[k])
		}
		return map[string]any{"imei": rec.IMEI, "serialNumber": rec.SerialNumber,
			"manufacturer": rec.Manufacturer, "customAttributes": strings.Join(attrs, ";")}, nil
	}
	return nil, fmt.Errorf("no device record %s in bulk profile %s", recID, profile)
}

func bulkDeviceEditCmd() *cobra.Command {
	var d bulkDevice
	cmd := &cobra.Command{
		Use:   "edit <id> <record-id>",
		Short: "Edit a device record in a bulk profile",
		Long: `Edit a device record. <record-id> is the id column of "bulk device list".
The PUT replaces the whole record (observed), so this loads the current
record and sends it back with only the given fields changed.`,
		Example: `  ivanti-neurons bulk device edit 1 109 --serial RZ8K314XFY76 --attrs "site=berlin"`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, client, err := bulkStart(args[0])
			if err != nil {
				return err
			}
			recID, err := parseID(args[1], false)
			if err != nil {
				return err
			}
			current, err := bulkDeviceRecord(cmd, client, id, recID)
			if err != nil {
				return err
			}
			body, err := d.body(cmd, map[string]any{"id": json.Number(recID)})
			if err != nil {
				return err
			}
			rec := body["devices"].([]any)[0].(map[string]any)
			for k, v := range current {
				if _, set := rec[k]; !set {
					rec[k] = v
				}
			}
			raw, err := client.Call(cmd.Context(), http.MethodPut, bulkPath+"/"+id+"/devices", nil, body)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Updated device record %s in bulk profile %s", recID, id))
		},
	}
	d.register(cmd)
	return cmd
}

func bulkDeviceDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <id> <record-id>...",
		Short:   "Delete device records from a bulk profile",
		Long:    `Delete device records. <record-id> is the id column of "bulk device list" (not the IMEI or serial number).`,
		Example: `  ivanti-neurons bulk device delete 1 109 124 --yes`,
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, client, err := bulkStart(args[0])
			if err != nil {
				return err
			}
			ids, err := parseIDs(args[1:])
			if err != nil {
				return err
			}
			if err := confirm(cmd, fmt.Sprintf("delete %d device records from bulk profile %s", len(ids), id)); err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodDelete, bulkPath+"/"+id+"/devices", url.Values{"ids": ids}, nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Deleted %d device records from bulk profile %s", len(ids), id))
		},
	}
}

// bulkStart is the common prologue of profile commands: check -o, validate
// the profile id, and build the client (no request is sent yet).
func bulkStart(arg string) (string, *api.Client, error) {
	id, err := parseID(arg, false)
	if err != nil {
		return "", nil, err
	}
	client, err := newClient()
	return id, client, err
}

// bulkObject builds a JSON object body: the --data object, then -f
// key=value fields, then the non-empty typed flag values on top.
func bulkObject(cmd *cobra.Command, data string, fields []string, typed map[string]string) (map[string]any, error) {
	obj, err := jsonBody(cmd, data, fields)
	if err != nil {
		return nil, err
	}
	for k, v := range typed {
		if v != "" {
			obj[k] = v
		}
	}
	if len(obj) == 0 {
		return nil, fmt.Errorf("nothing to send: pass field flags or --data")
	}
	return obj, nil
}

// bulkParseTime parses epoch milliseconds, an RFC 3339 timestamp, or a
// date (midnight UTC) into epoch milliseconds. Past times are refused:
// they expire the token at once (epoch seconds read as ms land in 1970).
func bulkParseTime(s string) (int64, error) {
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t, terr := time.Parse(time.RFC3339, s)
		if terr != nil {
			t, terr = time.Parse(time.DateOnly, s)
		}
		if terr != nil {
			return 0, fmt.Errorf("invalid time %q (want 2027-12-31, RFC 3339, or epoch milliseconds)", s)
		}
		ms = t.UnixMilli()
	}
	if ms <= time.Now().UnixMilli() {
		return 0, fmt.Errorf("time %q is in the past (%s); epoch values are milliseconds", s, time.UnixMilli(ms).UTC().Format("2006-01-02 15:04 UTC"))
	}
	return ms, nil
}
