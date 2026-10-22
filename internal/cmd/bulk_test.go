package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const bulkCSVData = "imei,serialNumber\n045923498,987634832832\n"

const bulkConfigJSON = `{"errors":null,"result":{"csvId":3,"tenantId":42000,"enrollmentToken":"ttzzel34","tokenExpiry":1700215169002,"userName":null,"userEmailAddress":null,"userId":null,"serverUrl":"https://example.mobileiron.com","errorMessage":null,"deviceCount":50,"provisioningConfig":null}}`

// bulkMock answers every bulk enrollment route with the guide's examples.
func bulkMock(t *testing.T) *mock {
	m := newMock(t)
	p := "/api/v1/bulkenrollment/profile"
	m.on("GET "+p, `{"errors":null,"result":{"searchResults":[{"csvId":10,"csvName":"a1","description":null,"modifiedAt":1712505201621,"devicesCount":0,"errorMessage":null,"userName":null,"userEmailAddress":null,"withoutToken":true,"userId":0},{"csvId":9,"csvName":"a4","description":null,"modifiedAt":1709790700477,"devicesCount":0,"errorMessage":null,"userName":null,"userEmailAddress":null,"withoutToken":true,"userId":0}],"totalCount":2,"offset":0,"limit":20}}`)
	m.on("PUT "+p, `{"errors":null,"result":1}`)
	m.on("POST "+p+"/upload", bulkConfigJSON)
	m.on("GET "+p+"/{id}", bulkConfigJSON)
	m.on("PUT "+p+"/{id}", `{"errors":null,"result":52}`)
	m.on("POST "+p+"/{id}", `{"errors":null,"result":1}`)
	m.on("DELETE "+p+"/{id}", `{"errors":null,"result":1}`)
	m.on("GET "+p+"/{id}/info", `{"errors":null,"result":{"csvName":"apiTestcsvUpdate","enrollmentToken":"su8eubjg3","tokenExpiry":1701094674198,"deviceStateToCount":{"TOTAL":51,"PENDING_REG":51}}}`)
	m.on("GET "+p+"/{id}/devices", `{"errors":null,"result":{"searchResults":[{"id":101,"imei":"3575370xxxxxxxx","tenantId":null,"manufacturer":"Samsung","serialNumber":"RAATYPXFY3399","state":"PENDING_REG","customAttributes":null,"userName":null}],"totalCount":1,"offset":0,"limit":20}}`)
	m.on("PUT "+p+"/{id}/devices", `{"errors":null,"result":1}`)
	m.on("DELETE "+p+"/{id}/devices", `{"errors":null,"result":2}`)
	m.on("POST "+p+"/{id}/upload", `{"errors":null,"result":true}`)
	m.on("PUT "+p+"/{id}/qrcode", `{"errors":null,"result":1}`)
	m.on("PUT "+p+"/{id}/refreshtoken", `{"errors":null,"result":{"enrollmentToken":"su8eubjg3","tokenExpiry":1700224007451}}`)
	m.on("GET "+p+"/{id}/qrcode/generate", `{"errors":null,"result":"{\"android.app.extra.PROVISIONING_ADMIN_EXTRAS_BUNDLE\":{\"server\":\"host\",\"qrCode\":true,\"token\":\"jtstg43j\"},\"android.app.extra.PROVISIONING_LOCALE\":\"en_US\"}"}`)
	m.on("GET "+p+"/{id}/download", "imei,manufacturer,serial_number,custom_attributes,username\n045923498,Samsung,987634832832,,\n")
	return m
}

func TestBulkList(t *testing.T) {
	m := bulkMock(t)
	out := m.run(t, "bulk", "list", "--sort", "csvName")
	mustContain(t, out, "CSVID", "a1", "a4", "2024-04-07 15:53")
	q := m.last(t, "GET", "/api/v1/bulkenrollment/profile").Query
	if q.Get("rows") != "50" || q.Get("start") != "0" || q.Get("sortFields[0].name") != "csvName" || q.Has("dmPartitionId") {
		t.Fatalf("unexpected query: %v", q)
	}
}

func TestBulkDeviceList(t *testing.T) {
	m := bulkMock(t)
	out := m.run(t, "bulk", "device", "list", "3", "-q", "RAATYPXFY3399")
	mustContain(t, out, "RAATYPXFY3399", "PENDING_REG")
	q := m.last(t, "GET", "/api/v1/bulkenrollment/profile/3/devices").Query
	if q.Get("q") != "RAATYPXFY3399" || q.Get("nav") != "true" || q.Get("rows") != "50" {
		t.Fatalf("unexpected query: %v", q)
	}
}

func TestBulkReads(t *testing.T) {
	m := bulkMock(t)
	mustContain(t, m.run(t, "bulk", "get", "3"), "enrollmentToken:", "ttzzel34", "2023-11-17 09:59")
	mustContain(t, m.run(t, "bulk", "info", "3"), "apiTestcsvUpdate", `{"PENDING_REG":51,"TOTAL":51}`)
	// the QR content arrives as a JSON string; print it unescaped
	mustContain(t, m.run(t, "bulk", "qrcode", "3"), `{"android.app.extra.PROVISIONING_ADMIN_EXTRAS_BUNDLE":{"server":"host"`)
	if _, err := m.runErr(t, "bulk", "get", "../tenant"); err == nil || !strings.Contains(err.Error(), "invalid id") {
		t.Fatalf("expected invalid id error, got %v", err)
	}
}

func TestBulkCreateMultipart(t *testing.T) {
	m := bulkMock(t)
	out := m.run(t, "bulk", "create", tempFile(t, "devices.csv", bulkCSVData), "--description", "Warehouse", "--without-token", "--type", "SELF_MAINTAINED")
	mustContain(t, out, "ttzzel34")
	req := m.last(t, "POST", "/api/v1/bulkenrollment/profile/upload")
	if !strings.HasPrefix(req.Header.Get("Content-Type"), "multipart/form-data") {
		t.Fatalf("not multipart: %q", req.Header.Get("Content-Type"))
	}
	mustContain(t, req.Body, `name="csv-data"; filename="devices.csv"`, "045923498,987634832832",
		`name="name"`+"\r\n\r\ndevices.csv", `name="description"`+"\r\n\r\nWarehouse",
		`name="withoutToken"`+"\r\n\r\ntrue", `name="profileType"`+"\r\n\r\nSELF_MAINTAINED")
}

func TestBulkDeviceUpload(t *testing.T) {
	m := bulkMock(t)
	m.run(t, "bulk", "device", "upload", "9", tempFile(t, "devices.csv", bulkCSVData))
	req := m.last(t, "POST", "/api/v1/bulkenrollment/profile/9/upload")
	mustContain(t, req.Body, `name="csv-data"; filename="devices.csv"`, "045923498,987634832832")
}

func TestBulkJSONWrites(t *testing.T) {
	for _, tc := range []struct {
		args         []string
		method, path string
		action, body string
	}{
		// name and description are both replaced: the unchanged one is resent
		{[]string{"update", "10", "--name", "scanners"}, "PUT", "", "",
			`{"description":"","id":10,"name":"scanners"}`},
		{[]string{"update", "9", "--description", "Warehouse"}, "PUT", "", "",
			`{"description":"Warehouse","id":9,"name":"a4"}`},
		{[]string{"configure", "1", "--data", `{"provisioningConfig":{"locale":"en_US"}}`}, "PUT", "/1/qrcode", "",
			`{"provisioningConfig":{"locale":"en_US"}}`},
		{[]string{"device", "add", "3", "--imei", "045923498", "--serial", "987634832832", "--manufacturer", "Alcatel Lucent inc", "--attrs", "isBulk=false;numkey=655734"}, "POST", "/3", "",
			`{"devices":[{"customAttributes":"isBulk=false;numkey=655734","imei":"045923498","manufacturer":"Alcatel Lucent inc","serialNumber":"987634832832"}]}`},
		// the PUT replaces the record: unchanged fields come from the current one
		{[]string{"device", "edit", "1", "101", "--data", `{"imei":"1","state":"PENDING_REG"}`, "--imei", "2"}, "PUT", "/1/devices", "",
			`{"devices":[{"customAttributes":"","id":101,"imei":"2","manufacturer":"Samsung","serialNumber":"RAATYPXFY3399","state":"PENDING_REG"}]}`},
		{[]string{"set-expiry", "3", "2099-12-31"}, "PUT", "/3", "refreshToken",
			`{"tokenExpiry":4102358400000,"userId":""}`},
		{[]string{"set-expiry", "3", "2099-12-31T01:00:00+01:00"}, "PUT", "/3", "refreshToken",
			`{"tokenExpiry":4102358400000,"userId":""}`},
		{[]string{"set-user", "3", "10052", "--expiry", "4102358400000"}, "PUT", "/3", "addUser",
			`{"tokenExpiry":4102358400000,"userId":10052}`},
		// without --expiry the current expiry (GET /3) is sent back
		{[]string{"set-user", "3", "10052"}, "PUT", "/3", "addUser",
			`{"tokenExpiry":1700215169002,"userId":10052}`},
	} {
		m := bulkMock(t)
		m.run(t, append([]string{"bulk"}, tc.args...)...)
		req := m.last(t, tc.method, "/api/v1/bulkenrollment/profile"+tc.path)
		if req.Body != tc.body || req.Query.Get("action") != tc.action {
			t.Errorf("%v: body %s query %v", tc.args, req.Body, req.Query)
		}
	}
}

func TestBulkWriteNeedsFields(t *testing.T) {
	m := bulkMock(t)
	if _, err := m.runErr(t, "bulk", "device", "add", "3"); err == nil || !strings.Contains(err.Error(), "nothing to send") {
		t.Fatalf("expected missing fields error, got %v", err)
	}
	if _, err := m.runErr(t, "bulk", "set-expiry", "3", "tomorrow"); err == nil || !strings.Contains(err.Error(), "invalid time") {
		t.Fatalf("expected invalid time error, got %v", err)
	}
	// epoch seconds read as milliseconds would expire the token at once
	if _, err := m.runErr(t, "bulk", "set-expiry", "3", "1798675200"); err == nil || !strings.Contains(err.Error(), "in the past") {
		t.Fatalf("expected past time error, got %v", err)
	}
	if _, err := m.runErr(t, "bulk", "device", "delete", "1", "", "--yes"); err == nil || !strings.Contains(err.Error(), "no ids") {
		t.Fatalf("expected no ids error, got %v", err)
	}
	if len(m.reqs) != 0 {
		t.Fatalf("invalid input sent requests: %v", m.reqs)
	}
}

// A profile without a token has no expiry: set-user must not send null.
func TestBulkSetUserWithoutExpiry(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/bulkenrollment/profile/{id}", `{"errors":null,"result":{"csvId":3,"tokenExpiry":null}}`)
	m.on("PUT /api/v1/bulkenrollment/profile/{id}", `{"errors":null,"result":1}`)
	m.run(t, "bulk", "set-user", "3", "10052")
	jsonEq(t, m.last(t, "PUT", "/api/v1/bulkenrollment/profile/3").Body, `{"userId":10052}`)
}

func TestBulkDestructiveConfirm(t *testing.T) {
	m := bulkMock(t)
	for _, args := range [][]string{
		{"bulk", "delete", "10"},
		{"bulk", "refresh-token", "3"},
		{"bulk", "device", "delete", "1", "109,124"},
	} {
		if _, err := m.runErr(t, args...); err == nil || !strings.Contains(err.Error(), "--yes") {
			t.Fatalf("%v: expected refusal without --yes, got %v", args, err)
		}
	}
	if len(m.reqs) != 0 {
		t.Fatalf("requests sent without confirmation: %v", m.reqs)
	}

	mustContain(t, m.run(t, "bulk", "delete", "10", "--yes"), "Deleted bulk enrollment profile 10")
	mustContain(t, m.run(t, "bulk", "refresh-token", "3", "--yes"), "su8eubjg3")
	m.run(t, "bulk", "device", "delete", "1", "109,124", "--yes")
	if ids := m.last(t, "DELETE", "/api/v1/bulkenrollment/profile/1/devices").Query["ids"]; strings.Join(ids, ",") != "109,124" {
		t.Fatalf("ids not sent as repeated query params: %v", ids)
	}
}

func TestBulkDownload(t *testing.T) {
	m := bulkMock(t)
	csv := "imei,manufacturer,serial_number,custom_attributes,username\n045923498,Samsung,987634832832,,\n"
	if out := m.run(t, "bulk", "download", "3"); out != csv {
		t.Fatalf("stdout = %q", out)
	}
	path := filepath.Join(t.TempDir(), "out.csv")
	mustContain(t, m.run(t, "bulk", "download", "3", "--out", path), "Saved "+path)
	if data, err := os.ReadFile(path); err != nil || string(data) != csv {
		t.Fatalf("file = %q, %v", data, err)
	}
}
