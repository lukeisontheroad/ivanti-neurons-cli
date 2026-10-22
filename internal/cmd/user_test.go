package cmd

import (
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// userPage is an account search page; the second UID shares a prefix with
// the first so exact UID matching is exercised. The first account carries
// every field "user update" resends; the second is shaped like the guide's
// fq=UID response, which lacks passwordNeverExpire, edipi, ...
const userPage = `{"errors":null,"result":{"totalCount":2,"searchResults":[
 {"id":10022,"createdAt":1372188681178,"displayName":"John Doe","firstName":"John","lastName":"Doe","uid":"jdoe@example.com","emailAddress":"jdoe@example.com","accountSource":"LOCAL","accountType":"USER","enabled":true,"managedAppleId":"jdoe@appleid.example","passwordNeverExpire":true,"androidWorkDeviceAccountEnabled":false,"edipi":null,"gdprProfileId":null,"passwordHistory":"{}","groups":["All Users","Engineering"],"deviceCount":0},
 {"id":10023,"createdAt":1442949222375,"displayName":"r m","firstName":"r","lastName":"m","uid":"jdoe@example.com.au","emailAddress":"rm@example.com","accountSource":"LOCAL","accountType":"USER","enabled":false,"groups":["All Users"],"deviceCount":1}
],"offset":0}}`

const userOK = `{"errors":null,"result":1}`

// valuesEq fails unless got holds exactly the want parameters (query or form).
func valuesEq(t *testing.T, got, want url.Values) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("params = %v\nwant     %v", got, want)
	}
}

func TestUserList(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/account", userPage)
	out := m.run(t, "user", "list", "-q", "doe")
	mustContain(t, out, "UID", "DISPLAYNAME", "10022", "jdoe@example.com", "John Doe", "All Users, Engineering", "2013-06-25 19:31")
	valuesEq(t, m.last(t, "GET", "/api/v1/account").Query, url.Values{
		"q": {"doe"}, "rows": {"50"}, "start": {"0"},
		"sortFields[0].name": {"id"}, "sortFields[0].order": {"ASC"},
	})
}

func TestUserView(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/account", userPage)
	out := m.run(t, "user", "view", "jdoe@example.com.au")
	mustContain(t, out, "id:", "10023", "rm@example.com")
	if strings.Contains(out, "10022") {
		t.Errorf("view picked a non-matching UID:\n%s", out)
	}
	if got := m.last(t, "GET", "/api/v1/account").Query.Get("fq"); got != "UID=jdoe@example.com.au" {
		t.Errorf("fq = %q", got)
	}
	if _, err := m.runErr(t, "user", "view", "nobody@example.com"); err == nil || !strings.Contains(err.Error(), "no account") {
		t.Errorf("unknown UID: err = %v", err)
	}
}

func TestUserExists(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/account", `{"errors":null,"result":true}`)
	if out := m.run(t, "user", "exists", "jdoe@example.com"); out != "true\n" {
		t.Errorf("out = %q", out)
	}
	if got := m.last(t, "GET", "/api/v1/account").Query.Get("uid"); got != "jdoe@example.com" {
		t.Errorf("uid = %q", got)
	}
}

func TestUserSearch(t *testing.T) {
	m := newMock(t)
	m.on("POST /api/v1/account/search", userPage)
	out := m.run(t, "user", "search", "-q", "jdoe", "--limit", "10")
	mustContain(t, out, "10022", "jdoe@example.com.au")
	valuesEq(t, m.last(t, "POST", "/api/v1/account/search").Query, url.Values{"q": {"jdoe"}, "rows": {"10"}, "start": {"0"}})
}

func TestUserMeAndMetadata(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/user/account", `{"errors":null,"result":{"id":10001,"uid":"admin@example.com","displayName":"Admin","accountSource":"LOCAL"}}`)
	m.on("GET /api/v1/account/metadata", `{"errors":null,"result":{"accountId":10001,"emailAddress":"admin@example.com","defaultCmPartitionId":20000,"defaultDmPartitionId":23000,"globalAuthorities":["account.read"],"groupIdAllUsers":11000}}`)
	mustContain(t, m.run(t, "user", "me"), "uid:", "admin@example.com")
	out := m.run(t, "user", "metadata")
	mustContain(t, out, "defaultDmPartitionId:", "23000", "groupIdAllUsers:", "11000")
	if strings.Contains(out, "account.read") {
		t.Errorf("metadata table should not list authorities:\n%s", out)
	}
}

func TestUserCreateWithPassword(t *testing.T) {
	m := newMock(t)
	m.on("POST /api/v1/account", `{"errors":null,"result":{"id":10024,"displayName":"Cool Tester","uid":"cool@example.com","emailAddress":"cool@example.com","accountSource":"LOCAL","enabled":true}}`)
	out := m.run(t, "user", "create", "--email", "cool@example.com", "--first-name", "Cool", "--last-name", "Tester",
		"--display-name", "Cool Tester", "--password", "Testing!23", "--send-invite", "--group-id", "11124,11125")
	mustContain(t, out, "id:", "10024", "cool@example.com")
	if strings.Contains(out, "Testing!23") {
		t.Error("password echoed")
	}
	// account fields and the password go in the form body, never the URL
	req := m.last(t, "POST", "/api/v1/account")
	valuesEq(t, req.Query, url.Values{"sendInvite": {"true"}})
	valuesEq(t, req.Form, url.Values{
		"emailAddress": {"cool@example.com"}, "uid": {"cool@example.com"}, "firstName": {"Cool"}, "lastName": {"Tester"},
		"displayName": {"Cool Tester"}, "accountSource": {"LOCAL"}, "password": {"Testing!23"},
		"confirmPassword": {"Testing!23"}, "groupIds": {"11124", "11125"},
	})
}

func TestUserCreateWithoutPassword(t *testing.T) {
	m := newMock(t)
	m.on("POST /api/v1/accounts", `{"errors":null,"result":[{"id":10023,"uid":"new@example.com","emailAddress":"new@example.com","accountSource":"LOCAL","enabled":false}]}`)
	out := m.run(t, "user", "create", "--email", "new@example.com", "--ownership", "COMPANY", "-f", "managedAppleId=new@appleid.example")
	mustContain(t, out, "ID", "10023", "new@example.com")
	req := m.last(t, "POST", "/api/v1/accounts")
	valuesEq(t, req.Query, url.Values{"sendInvite": {"false"}, "deviceOwnershipType": {"COMPANY"}})
	valuesEq(t, req.Form, url.Values{
		"accounts[0].emailAddress": {"new@example.com"}, "accounts[0].uid": {"new@example.com"},
		"accounts[0].accountSource": {"LOCAL"}, "accounts[0].managedAppleId": {"new@appleid.example"},
	})
}

func TestUserUpdateKeepsFields(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/account", userPage)
	m.on("PUT /api/v1/account", userOK)
	out := m.run(t, "user", "update", "jdoe@example.com", "--first-name", "Jane", "--enabled=false", "-f", "edipi=1234567895",
		"--password", "Letme77in@")
	mustContain(t, out, "Updated account 10022 (jdoe@example.com)")
	req := m.last(t, "PUT", "/api/v1/account")
	if len(req.Query) > 0 {
		t.Errorf("update sent URL parameters (password must stay out of the URL): %v", req.Query)
	}
	valuesEq(t, req.Form, url.Values{
		"id": {"10022"}, "uid": {"jdoe@example.com"}, "emailAddress": {"jdoe@example.com"}, "firstName": {"Jane"},
		"lastName": {"Doe"}, "displayName": {"John Doe"}, "accountSource": {"LOCAL"}, "enabled": {"false"},
		"managedAppleId": {"jdoe@appleid.example"}, "passwordNeverExpire": {"true"}, "androidWorkDeviceAccountEnabled": {"false"},
		"edipi": {"1234567895"}, "password": {"Letme77in@"}, "confirmPassword": {"Letme77in@"},
	})
}

// The PUT clears fields it does not receive, so fields the lookup did not
// return must come from -f; null ones (account 10022's edipi) need nothing.
// The API omits null fields, so fields the lookup lacks are empty and the
// update must go through without them (observed API behaviour).
func TestUserUpdateWithOmittedNullFields(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/account", userPage)
	m.on("PUT /api/v1/account", userOK)
	m.run(t, "user", "update", "jdoe@example.com.au", "--last-name", "Smith", "-f", "managedAppleId=jdoe@appleid.example")
	form := m.last(t, "PUT", "/api/v1/account").Form
	if form.Get("lastName") != "Smith" || form.Get("managedAppleId") != "jdoe@appleid.example" || form.Has("edipi") {
		t.Errorf("form = %v", form)
	}
}

func TestUserGroupMembership(t *testing.T) {
	m := newMock(t)
	m.on("PUT /api/v1/account", `{"errors":null,"result":true}`)
	m.on("DELETE /api/v1/account", userOK)

	// an empty $GROUP in a script must not mean "no groups"
	for _, cmd := range []string{"add-to-group", "remove-from-group"} {
		if _, err := m.runErr(t, "user", cmd, "10200", "--group-id", "", "--yes"); err == nil || !strings.Contains(err.Error(), "no ids") {
			t.Errorf("%s --group-id \"\": err = %v", cmd, err)
		}
	}
	if n := m.count("PUT", "") + m.count("DELETE", ""); n > 0 {
		t.Fatalf("%d request(s) sent without group ids", n)
	}

	mustContain(t, m.run(t, "user", "add-to-group", "10200", "10201", "--group-id", "11124", "--group-id", "11125"), "Assigned 2 account(s)")
	valuesEq(t, m.last(t, "PUT", "/api/v1/account").Query, url.Values{
		"accountIds": {"10200", "10201"}, "groupIds": {"11124", "11125"}, "append": {"true"},
	})
	if _, err := m.runErr(t, "user", "add-to-group", "10200", "--group-id", "11124", "--replace"); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("--replace without --yes: err = %v", err)
	}
	m.run(t, "user", "add-to-group", "10200", "--group-id", "11124", "--replace", "--yes")
	if got := m.last(t, "PUT", "/api/v1/account").Query.Get("append"); got != "false" {
		t.Errorf("append = %q", got)
	}

	if _, err := m.runErr(t, "user", "remove-from-group", "2", "--group-id", "1"); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("remove without --yes: err = %v", err)
	}
	if m.count("DELETE", "") > 0 {
		t.Fatal("DELETE sent without confirmation")
	}
	m.run(t, "user", "remove-from-group", "2", "--group-id", "1", "--yes")
	valuesEq(t, m.last(t, "DELETE", "/api/v1/account").Query, url.Values{"accountIds": {"2"}, "groupIds": {"1"}})
}

func TestUserDelete(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/account", `{"errors":null,"result":{"searchResults":[],"totalCount":0,"offset":0,"limit":0}}`)
	m.on("DELETE /api/v1/account", userOK)
	if _, err := m.runErr(t, "user", "delete", "47253017", "11193157"); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("delete without --yes: err = %v", err)
	}
	if m.count("DELETE", "") > 0 {
		t.Fatal("DELETE sent without confirmation")
	}
	valuesEq(t, m.last(t, "GET", "/api/v1/account").Query, url.Values{
		"checkDeleteViability": {"true"}, "ids": {"47253017", "11193157"},
	})
	mustContain(t, m.run(t, "user", "delete", "47253017,11193157", "--yes"), "Deleted account(s) 47253017, 11193157")
	valuesEq(t, m.last(t, "DELETE", "/api/v1/account").Query, url.Values{"id": {"47253017,11193157"}})
}

func TestUserDeleteBlockedByViabilityCheck(t *testing.T) {
	for result, want := range map[string]string{
		`{"searchResults":[{"id":47253017,"uid":"jdoe@example.com"}],"totalCount":1}`: "viability check flagged 1",
		`[{"id":47253017}]`: "unexpected API result", // an unreadable check fails closed
	} {
		m := newMock(t)
		m.on("GET /api/v1/account", `{"errors":null,"result":`+result+`}`)
		m.on("DELETE /api/v1/account", userOK)
		if _, err := m.runErr(t, "user", "delete", "47253017", "--yes"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
		if m.count("DELETE", "") > 0 {
			t.Error("DELETE sent despite the viability check")
		}
	}
}

func TestUserInvite(t *testing.T) {
	m := newMock(t)
	m.on("POST /api/v1/invite/enduser", `{"errors":null,"result":true}`)
	mustContain(t, m.run(t, "user", "invite", "10552", "--ownership", "EMPLOYEE"), "Invited 1 user(s)")
	valuesEq(t, m.last(t, "POST", "/api/v1/invite/enduser").Query, url.Values{
		"accountIds": {"10552"}, "deviceOwnershipType": {"EMPLOYEE"},
	})
}

func TestUserEnrollmentURL(t *testing.T) {
	enrollURL := "https://na1.example.com/c/i/reg/enroll.mobileconfig?token=" + strings.Repeat("7F7F00009C44AFBE", 30)
	m := newMock(t)
	m.on("POST /api/v1/tenant/enrollment/url", `{"errors":null,"result":{"10022":"`+enrollURL+`"}}`)
	out := m.run(t, "user", "enrollment-url", "10022")
	mustContain(t, out, "ACCOUNTID", "10022", enrollURL)
	jsonEq(t, m.last(t, "POST", "/api/v1/tenant/enrollment/url").Body, `{"accountIds":[10022],"tokenLifetime":604800000}`)
}

func TestUserLDAPImport(t *testing.T) {
	m := newMock(t)
	m.on("POST /api/v1/ldap/import/user", userOK)
	m.on("GET /api/v1/ldap/import/user/status", `{"requestId":1,"status":"FAILED","errorCode":"LDAP_USER_IMPORT-106","errorMessage":"The LDAP user jdoe@example.com not found to import."}`)
	out := m.run(t, "user", "ldap-import", "jdoe@example.com", "--ldap-server", "ldap-2k", "--send-invite")
	mustContain(t, out, "Import request 1 submitted", "user ldap-import-status 1")
	valuesEq(t, m.last(t, "POST", "/api/v1/ldap/import/user").Query, url.Values{
		"uidOrEmail": {"jdoe@example.com"}, "ldapServerName": {"ldap-2k"}, "sendInvite": {"true"},
	})
	mustContain(t, m.run(t, "user", "ldap-import-status", "1"), "status:", "FAILED", "LDAP_USER_IMPORT-106")
	if got := m.last(t, "GET", "/api/v1/ldap/import/user/status").Query.Get("requestId"); got != "1" {
		t.Errorf("requestId = %q", got)
	}
}

func TestUserAttrs(t *testing.T) {
	m := newMock(t)
	m.on("PATCH /api/v1/account/{id}/customattributes", userOK)
	m.on("PATCH /api/v1/account/customattributes", userOK)
	m.on("DELETE /api/v1/account/customattributes", userOK)
	m.on("DELETE /api/v1/account/{id}/customattributes", userOK)

	m.run(t, "user", "attrs", "set", "739375", "testUserAttribute=forDocs")
	jsonEq(t, m.last(t, "PATCH", "/api/v1/account/739375/customattributes").Body, `{"attrs":{"testUserAttribute":["forDocs"]}}`)

	m.run(t, "user", "attrs", "set", "19850", "testUserAttribute=forCheck", "--overwrite")
	jsonEq(t, m.last(t, "PATCH", "/api/v1/account/19850/customattributes").Body,
		`{"attrs":{"testUserAttribute":["forCheck"]},"forceOverwrite":true}`)

	mustContain(t, m.run(t, "user", "attrs", "set", "10022,10023", "costCenter=4711", "costCenter=4712"), "on 2 account(s)")
	jsonEq(t, m.last(t, "PATCH", "/api/v1/account/customattributes").Body,
		`{"ids":[10022,10023],"attrs":{"attrs":{"costCenter":["4711","4712"]}}}`)

	if _, err := m.runErr(t, "user", "attrs", "delete", "42299037", "test_abc", "test"); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("delete without --yes: err = %v", err)
	}
	if m.count("DELETE", "") > 0 {
		t.Fatal("DELETE sent without confirmation")
	}
	m.run(t, "user", "attrs", "delete", "42299037", "test_abc", "test", "--yes")
	jsonEq(t, m.last(t, "DELETE", "/api/v1/account/customattributes").Body, `{"ids":["42299037"],"attrKeys":["test_abc","test"]}`)

	mustContain(t, m.run(t, "user", "attrs", "clear", "42299037", "--yes"), "Removed all custom attributes from account 42299037")
	if body := m.last(t, "DELETE", "/api/v1/account/42299037/customattributes").Body; body != "" {
		t.Errorf("clear sent a body: %q", body)
	}
}

func TestUserPin(t *testing.T) {
	m := newMock(t)
	m.on("POST /api/v1/account/regpin", `{"errors":null,"result":[
 {"id":796902,"createdAt":1681160105155,"createdBy":38942135,"accountId":38942135,"pin":"8731","expiresAt":1681764905157,"used":false,"ppkgPinStatus":"NOT_GENERATED"},
 {"id":796903,"createdAt":1681160105181,"createdBy":38942136,"accountId":38942136,"pin":"4825","expiresAt":1681764905183,"used":false,"ppkgPinStatus":"NOT_GENERATED"}]}`)
	m.on("GET /api/v1/account/{id}/registrationPin", `{"errors":null,"result":[{"id":42950,"createdAt":1751868324090,"createdBy":2057350,"accountId":2057350,"pin":"698133","expiresAt":1752473124089,"used":false,"ppkgPinStatus":"NOT_GENERATED"}]}`)

	mustContain(t, m.run(t, "user", "pin", "generate", "38942135", "38942136", "--ownership", "COMPANY"), "PIN", "8731", "4825", "2023-04-17")
	req := m.last(t, "POST", "/api/v1/account/regpin")
	valuesEq(t, req.Query, url.Values{"accountIds": {"38942135", "38942136"}, "deviceOwnershipType": {"COMPANY"}})
	jsonEq(t, req.Body, `{"ids":[38942135,38942136]}`)

	mustContain(t, m.run(t, "user", "pin", "view", "2057350"), "698133")
	m.last(t, "GET", "/api/v1/account/2057350/registrationPin")

	if _, err := m.runErr(t, "user", "pin", "view", "../device"); err == nil || !strings.Contains(err.Error(), "invalid id") {
		t.Errorf("non-numeric id: err = %v", err)
	}
}

func TestUserRegistrationSettings(t *testing.T) {
	m := newMock(t)
	m.on("PUT /api/v1/userpolicy", userOK)
	mustContain(t, m.run(t, "user", "registration-settings", "--mode", "pin", "--pin-length", "6", "-f", "description=PIN Only Setting"),
		"Device registration mode set to PIN")
	req := m.last(t, "PUT", "/api/v1/userpolicy")
	valuesEq(t, req.Query, url.Values{"policyType": {"REGISTRATION_AUTH"}})
	jsonEq(t, req.Body, `{"systemName":"registrationAuthUserPolicy","name":"Device Registration Authentication Type",
		"description":"PIN Only Setting","policyType":"REGISTRATION_AUTH","priority":2147483647,"enabled":true,
		"policyDataMutable":true,"distributionMutable":false,"priorityMutable":false,"deletable":false,
		"registrationAuthMode":"PIN","pinType":"NUMERIC","pinLength":6,"pinLifetimeMillis":604800000,"pinResetByUser":true}`)

	m.run(t, "user", "registration-settings", "--mode", "PASSWORD")
	if body := m.last(t, "PUT", "/api/v1/userpolicy").Body; strings.Contains(body, "pinLength") {
		t.Errorf("PASSWORD mode sent PIN settings: %s", body)
	}
}

func TestUserReports(t *testing.T) {
	const csv = "id,uid\n10022,jdoe@example.com\n"
	m := newMock(t)
	m.on("GET /api/v1/report/flex/job/summary", `{"errors":null,"result":{"searchResults":[{"name":"users","description":"","manualRunInProgress":false,"status":"ENABLED","startDate":1691756796125,"nextRunDate":1691843196125,"outputFormats":["CSV"],"reportJobRunStatus":"SCHEDULED"}],"totalCount":1,"offset":0,"limit":50}}`)
	m.on("GET /api/v1/report/flex/job/output/latest/download", csv)

	mustContain(t, m.run(t, "user", "report", "list"), "users", "ENABLED", "SCHEDULED", "CSV", "2023-08-11 12:26")
	valuesEq(t, m.last(t, "GET", "/api/v1/report/flex/job/summary").Query, url.Values{"rows": {"50"}, "start": {"0"}})

	if out := m.run(t, "user", "report", "download", "Users-1"); out != csv {
		t.Errorf("stdout = %q", out)
	}
	file := filepath.Join(t.TempDir(), "users.csv")
	mustContain(t, m.run(t, "user", "report", "download", "Users-1", "--out", file), "Saved", "(30 bytes)")
	if data, err := os.ReadFile(file); err != nil || string(data) != csv {
		t.Errorf("file = %q, %v", data, err)
	}
	valuesEq(t, m.last(t, "GET", "/api/v1/report/flex/job/output/latest/download").Query, url.Values{
		"reportName": {"Users-1"}, "fileType": {"CSV"},
	})
}

func TestUserLoginNonce(t *testing.T) {
	nonce := strings.Repeat("7F7F00009C4BAFBE", 20)
	m := newMock(t)
	m.on("POST /api/v1/account/login-nonce", `{"errors":null,"result":{"loginNonce":"`+nonce+`"}}`)
	if out := m.run(t, "user", "login-nonce"); out != nonce+"\n" {
		t.Errorf("out = %q", out)
	}
}
