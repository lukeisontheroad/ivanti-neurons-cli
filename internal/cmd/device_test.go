package cmd

import (
	"net/url"
	"strings"
	"testing"
)

const deviceSearchFixture = `{"errors":null,"result":{"totalCount":2,"searchResults":[
{"id":22004,"guid":"50459445-0f9e-456a-b485-534842acdf9f","deviceModel":"Pixel 2","prettyModel":"Pixel 2","platformType":"ANDROID","platformVersion":"9.0","lastCheckin":1546491367278,"registrationState":"RETIRED","uid":"a1@example.com"},
{"id":92752,"guid":"ed6b6e18-892f-4059-b605-def424308c4d","deviceModel":"iPad4,5","prettyModel":"iPad Mini 2nd gen","platformType":"IOS","platformVersion":"12.4.4","lastCheckin":1578404119952,"registrationState":"ACTIVE","uid":"praveen@example.com"}]}}`

const deviceFixture = `{"errors":null,"result":{"id":92752,"guid":"ed6b6e18-892f-4059-b605-def424308c4d",
"mdmChannelId":"850a62ec-c29a-42ff-b9e2-16846cf5eb2d","phoneNumber":null,"deviceModel":"iPad4,5","deviceName":"iPad",
"platformType":"IOS","platformVersion":"12.4.4","lastCheckin":1578404119952,"registrationState":"ACTIVE",
"uid":"praveen@example.com","serialNumber":"DLXMF0URFLMK","supervised":false,"prettyModel":"iPad Mini 2nd gen",
"ownerId":514976,"customAttributes":{"attrs":{"cityId":["SF"]}}}}`

func TestDeviceListFilters(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/device", deviceSearchFixture)
	out := m.run(t, "device", "list", "--platform", "ios", "--status", "active", "--user", "jdoe",
		"--email", "jdoe@example.com", "--attr", "cityId=SF", "--checked-in-within", "2h",
		"--registered-within", "7d", "--fq", "a='1' OR b='2'")
	mustContain(t, out, "PRETTYMODEL", "iPad Mini 2nd gen", "2020-01-07 13:35", "praveen@example.com")
	q := m.last(t, "GET", "/api/v1/device").Query
	want := `(a='1' OR b='2') AND platformType='IOS' AND registrationState='ACTIVE' AND UID EQ 'jdoe' AND ` +
		`EMAILADDRESS EQ 'jdoe@example.com' AND CFDEVICE[cityId]='SF' AND lastCheckin<'02:00:00' AND ` +
		`lastRegistrationTime<'7:00:00:00'`
	if got := q.Get("fq"); got != want {
		t.Errorf("fq = %q\nwant %q", got, want)
	}
	for k, v := range map[string]string{"dmPartitionId": "23000", "rows": "50", "start": "0"} {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if q.Has("sortFields[0].name") {
		t.Errorf("capped search should keep the API's default order: %v", q)
	}
}

func TestDeviceListAllSortsByID(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/device", deviceSearchFixture)
	m.run(t, "device", "list", "--limit", "0")
	q := m.last(t, "GET", "/api/v1/device").Query
	if q.Get("sortFields[0].name") != "id" || q.Get("sortFields[0].order") != "ASC" || q.Get("rows") != "500" {
		t.Fatalf("unlimited search must sort by id with 500 rows: %v", q)
	}
}

func TestDeviceWithin(t *testing.T) {
	for _, tc := range []struct{ field, op, dur, want string }{
		{"lastCheckin", "<", "90s", "lastCheckin<'00:01:30'"},
		{"lastCheckin", "<", "1d12h", "lastCheckin<'1:12:00:00'"},
		{"lastCheckin", ">", "30d", "lastCheckin>'30:00:00:00'"},
		{"lastRegistrationTime", "<", "3m6s", "lastRegistrationTime<'0:00:03:06'"},
		{"lastRegistrationTime", "<", "30d", "lastRegistrationTime<'30:00:00:00'"},
	} {
		if got, err := deviceWithin(tc.field, tc.op, tc.dur); err != nil || got != tc.want {
			t.Errorf("deviceWithin(%s, %s, %s) = %q, %v; want %q", tc.field, tc.op, tc.dur, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "d", "7x", "1.5d", "-1h", "0s"} {
		if _, err := deviceWithin("lastCheckin", "<", bad); err == nil {
			t.Errorf("deviceWithin(%q) accepted", bad)
		}
	}
}

func TestDeviceWithApp(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/device", deviceSearchFixture)
	m.run(t, "device", "with-app", "com.android.mi.email", "--platform", "android", "--version", "303,304",
		"--signature", "52825fa3", "--installation-type", "INSTALLED")
	got := m.last(t, "GET", "/api/v1/device").Query
	want := url.Values{
		"fq":               {"bundleId='com.android.mi.email' AND (version='303' OR version='304')"},
		"type":             {"APP_INVENTORY"},
		"platformType":     {"ANDROID"},
		"signature":        {"52825fa3"},
		"installationType": {"INSTALLED"},
		"dmPartitionId":    {"23000"},
		"rows":             {"50"},
		"start":            {"0"},
	}
	if got.Encode() != want.Encode() {
		t.Fatalf("query = %v\nwant %v", got, want)
	}
}

func TestDeviceView(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/device/{id}", deviceFixture)
	out := m.run(t, "device", "view", "92752")
	mustContain(t, out, "prettyModel:", "iPad Mini 2nd gen", "lastCheckin:", "2020-01-07 13:35", "cityId=SF")
	if strings.Contains(out, "mdmChannelId") {
		t.Errorf("default view should print key fields only:\n%s", out)
	}
	mustContain(t, m.run(t, "device", "view", "ed6b6e18-892f-4059-b605-def424308c4d", "--all"), "mdmChannelId")
	m.last(t, "GET", "/api/v1/device/ed6b6e18-892f-4059-b605-def424308c4d")
}

func TestDeviceReads(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/device/{id}", deviceFixture)
	m.on("GET /api/v1/device/22000/customattributes", `{"attrs":{"cityId":["SF"],"carrier":["att","tmo"]}}`)
	m.on("GET /api/v1/device/22200/configs", `{"errors":null,"result":{"totalCount":1,"queryTime":0,"searchResults":[
{"id":35046,"name":"Passcode Config","policyType":"PASSCODE","uuid":"bd330bef-1ae8-4fe4-bdb7-dfbea2193aa1","enabled":true,"status":"INSTALLED"}],"offset":0,"limit":1}}`)
	m.on("GET /api/v1/device/22000/policies", `{"errors":null,"result":{"totalCount":1,"searchResults":[
{"id":35010,"name":"International Roaming Devices","policyType":"POLICY","policyRuleType":"ROAMING","enabled":true,"compliant":false,"pendingCompliant":false}]}}`)
	m.on("GET /api/v2/policy/filter", `{"errors":null,"result":{"totalCount":1,"searchResults":[
{"id":35150,"dmPartitionId":23003,"name":"Compromised Devices","policyType":"POLICY","policyRuleType":"COMPROMISED","enabled":true,"violationDeviceCount":3}]}}`)
	m.on("GET /api/v1/device/92752/certs/search", `{"errors":null,"result":{"totalCount":1,"searchResults":[
{"caName":"AppStoreFrontCA","subject":"CN=556f0f59","certState":"Active","certSerialNumberString":"26736","notAfter":1701472062000,"deviceId":92752}]}}`)

	mustContain(t, m.run(t, "device", "attrs", "get", "22000"), "cityId:", "SF", "carrier:", "att, tmo")
	mustContain(t, m.run(t, "device", "config", "list", "22200"), "Passcode Config", "bd330bef-1ae8-4fe4-bdb7-dfbea2193aa1", "INSTALLED")
	mustContain(t, m.run(t, "device", "compliance", "status", "22000"), "International Roaming Devices", "ROAMING", "false")

	mustContain(t, m.run(t, "device", "compliance", "policies"), "Compromised Devices", "3")
	q := m.last(t, "GET", "/api/v2/policy/filter").Query
	if q.Get("tag") != "RULE" || q.Get("dmPartitionId") != "23000" {
		t.Errorf("policy filter query: %v", q)
	}

	mustContain(t, m.run(t, "device", "certs", "92752"), "AppStoreFrontCA", "Active", "26736")
	if got := m.last(t, "GET", "/api/v1/device/92752/certs/search").Query.Get("accountId"); got != "514976" {
		t.Errorf("certs accountId = %q, want the device owner 514976", got)
	}
}

func TestDeviceWrites(t *testing.T) {
	const okInt, okTrue = `{"errors":null,"result":1}`, `{"errors":null,"result":true}`
	for _, tc := range []struct {
		args         []string
		method, path string
		resp         string
		query        url.Values
		body, out    string
	}{
		{
			args: []string{"assign", "73542", "--to", "10542407", "--yes"}, method: "PUT", path: "/api/v1/device/updateAccount",
			resp:  `{"errors":null,"result":{"searchResults":[],"totalCount":0,"offset":0,"limit":0}}`,
			query: url.Values{"accountId": {"10542407"}, "deviceIds": {"73542"}}, out: "Reassignment initiated for 1 of 1 device(s)",
		},
		{
			args: []string{"attrs", "set", "22000", "cityId=SF", "cityId=NY"}, method: "PATCH", path: "/api/v1/device/22000/customattributes",
			resp: okInt, body: `{"attrs":{"cityId":["SF","NY"]},"forceOverwrite":true}`, out: "Set 1 attribute(s) on device 22000",
		},
		{
			args:   []string{"attrs", "set", "57d9903c-aadf-4b40-aef3-e1f24302f180", "c=value_c_added", "--append"},
			method: "PATCH", path: "/api/v1/device/57d9903c-aadf-4b40-aef3-e1f24302f180/customattributes",
			resp: okInt, body: `{"attrs":{"c":["value_c_added"]},"forceOverwrite":false}`,
		},
		{
			args: []string{"attrs", "replace", "22000", "tlark=1", "--yes"}, method: "POST", path: "/api/v1/device/22000/customattributes",
			resp: okInt, body: `{"attrs":{"tlark":["1"]}}`, out: "Replaced all attributes of device 22000 with 1 attribute(s)",
		},
		{
			args:   []string{"attrs", "remove", "d67f2d3b-4ea7-44c6-9fb7-5f4107184e34", "attribute1=1", "--yes"},
			method: "DELETE", path: "/api/v1/device/d67f2d3b-4ea7-44c6-9fb7-5f4107184e34/customattributes",
			resp: okInt, body: `{"attrs":{"attribute1":["1"]}}`,
		},
		{
			args:   []string{"attrs", "remove-keys", "73542", "--key", "ForDocs001,forDocs002", "--yes"},
			method: "DELETE", path: "/api/v1/device/customattributes",
			resp: okInt, body: `{"attrKeys":["ForDocs001","forDocs002"],"ids":["73542"]}`, out: "Removed 2 attribute(s) from 1 device(s)",
		},
		{
			args: []string{"config", "exclude", "197483", "47269"}, method: "PUT", path: "/api/v1/device/197483/uninstallconfigs",
			resp: okTrue, query: url.Values{"policyIds": {"47269"}}, out: "Excluded 1 configuration(s) on device 197483",
		},
		{
			args: []string{"config", "include", "197483", "47269,47270"}, method: "DELETE", path: "/api/v1/device/197483/uninstallconfigs",
			resp: okTrue, query: url.Values{"policyIds": {"47269", "47270"}},
		},
		{
			args: []string{"config", "reinstall", "67648", "42492"}, method: "PUT", path: "/api/v1/device/67648/reinstallconfigs",
			resp: okTrue, query: url.Values{"policyIds": {"42492"}},
		},
		{
			args:   []string{"config", "clear-error", "3588337", "84aaf9be-d443-4920-a526-9c408ff672b9"},
			method: "PUT", path: "/api/v1/device/clearConfigError", resp: okTrue,
			query: url.Values{"deviceId": {"3588337"}, "policyUuid": {"84aaf9be-d443-4920-a526-9c408ff672b9"}, "dmPartitionId": {"23000"}},
		},
		{
			args: []string{"compliance", "sync", "800", "802", "--vendor", "GoogleBC"}, method: "POST", path: "/api/v1/device/compliance/sync",
			resp: okInt, query: url.Values{"vendor": {"GoogleBC"}, "ids": {"800", "802"}},
		},
		{
			args:   []string{"predicate", "create", "--name", "predicate_1", "--description", "sample predicate", "--query", "@status(device.model.family) =='iPad'"},
			method: "POST", path: "/api/v1/predicate", resp: okInt,
			body: `[{"predicate":{"description":"sample predicate","name":"predicate_1","query":"@status(device.model.family) =='iPad'"}}]`,
		},
		{
			args:   []string{"predicate", "update", "65", "--name", "test1", "--query", "@status(device.model.family) =='iPad'"},
			method: "PUT", path: "/api/v1/predicate", resp: okInt,
			body: `[{"predicate":{"description":"","id":65,"name":"test1","query":"@status(device.model.family) =='iPad'"}}]`,
		},
		{
			args: []string{"ldap-sync", "32001"}, method: "PUT", path: "/api/v1/ldapserver/32001/sync",
			resp: `{"errors":null,"result":{"id":32001,"name":"STGLDAP","dataSyncState":"SUCCESS"}}`, out: "LDAP sync started for server 32001",
		},
		{
			args: []string{"teamviewer", "3090537"}, method: "POST", path: "/api/v1/teamviewer/3090537",
			resp: `{"errors":null,"result":{"redirectUri":"https://tv.example/oauth2/authorize?client_id=abc"}}`,
			out:  "https://tv.example/oauth2/authorize?client_id=abc",
		},
	} {
		t.Run(strings.Join(tc.args[:2], " "), func(t *testing.T) {
			m := newMock(t)
			m.on(tc.method+" "+tc.path, tc.resp)
			out := m.run(t, append([]string{"device"}, tc.args...)...)
			mustContain(t, out, tc.out)
			req := m.last(t, tc.method, tc.path)
			if got := req.params(); got.Encode() != tc.query.Encode() {
				t.Errorf("params = %v, want %v", got, tc.query)
			}
			if tc.body != "" || req.Body != "" {
				jsonEq(t, req.Body, tc.body)
			}
		})
	}
}

func TestDeviceDelete(t *testing.T) {
	m := newMock(t)
	m.on("DELETE /api/v1/device", `{"errors":null,"result":1}`)
	// The report follows the API's count, not the ids passed.
	mustContain(t, m.run(t, "device", "delete", "1007966,1007967", "--yes"),
		"Warning: the API acted on 1 of 2 device(s)", "Deleted 1 device(s)")
	if got := m.last(t, "DELETE", "/api/v1/device").params()["deviceIds"]; strings.Join(got, ",") != "1007966,1007967" {
		t.Fatalf("deviceIds = %v", got)
	}

	m = newMock(t)
	m.on("DELETE /api/v1/device", `{"errors":null,"result":0}`)
	if _, err := m.runErr(t, "device", "delete", "1007966", "--yes"); err == nil || !strings.Contains(err.Error(), "delete failed") {
		t.Fatalf("want an error when nothing was deleted, got %v", err)
	}
}

func TestDeviceDestructiveNeedsConfirm(t *testing.T) {
	m := newMock(t)
	for _, args := range [][]string{
		{"device", "delete", "1007966"},
		{"device", "assign", "73542", "--to", "10542407"},
		{"device", "attrs", "replace", "22000", "a=b"},
		{"device", "attrs", "remove", "22000", "a=b"},
		{"device", "attrs", "remove-keys", "22000", "--key", "a"},
	} {
		if _, err := m.runErr(t, args...); err == nil || !strings.Contains(err.Error(), "--yes") {
			t.Errorf("%v: expected refusal without --yes, got %v", args, err)
		}
	}
	if len(m.reqs) != 0 {
		t.Fatalf("refused commands sent requests: %+v", m.reqs)
	}
}

func TestDeviceRejectsBadArgs(t *testing.T) {
	m := newMock(t)
	for _, args := range [][]string{
		{"device", "view", "1/../2"},
		{"device", "config", "exclude", "1", "abc"},
		{"device", "config", "clear-error", "1", "not-a-uuid"},
		{"device", "list", "--checked-in-within", "7x"},
		{"device", "list", "--attr", "novalue"},
		{"device", "assign", "1", "--to", "jdoe", "--yes"},
		{"device", "attrs", "remove-keys", "1", "--key", "", "--yes"}, // empty "$VAR" must not mean "none"
		{"device", "attrs", "remove-keys", "1", "--key", ",", "--yes"},
		{"device", "with-app", "com.example.app", "--platform", "ios", "--version", ""},
	} {
		if _, err := m.runErr(t, args...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
	if len(m.reqs) != 0 {
		t.Fatalf("invalid arguments reached the API: %+v", m.reqs)
	}
}
