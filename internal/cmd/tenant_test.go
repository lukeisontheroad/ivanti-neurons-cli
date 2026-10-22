package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTenantMetadataStatusLicense(t *testing.T) {
	m := newMock(t)
	mustContain(t, m.run(t, "tenant", "metadata"), "tenantGuid:", "t-guid", "webAppVersion.version:", "99.0")

	m.on("GET /api/v1/tenant/mixpanel-status", `{"errors":null,"result":false}`)
	mustContain(t, m.run(t, "tenant", "status"), "false")

	m.on("GET /api/v1/tenant/license/usage", `{"errors":null,"result":{"billingType":"SUBSCRIPTION","licenseSkus":["PLATINUM"],"displayName":"Platinum","licenseType":"DEVICE","licenseStatus":"ACTIVATED","startDate":1714975231998,"durationInDays":365,"licenseTerm":{"softLimit":1000,"hardLimit":0,"currentUsage":1},"accountTerm":{"softLimit":0,"hardLimit":0,"currentUsage":248},"contentTerm":null,"activeDeviceCount":1,"trial":true}}`)
	out := m.run(t, "tenant", "license", "--fields", "licenseTerm,accountTerm")
	mustContain(t, out, "Platinum", "2024-05-06", `"currentUsage":248`)
	wantQuery(t, m.last(t, "GET", "/api/v1/tenant/license/usage").Query, map[string]string{"requestedFields": "licenseTerm,accountTerm"})
}

func TestTenantSpaceAndConnectorList(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/tenant/partition/device", `{"errors":null,"result":{"totalCount":1,"queryTime":0,"searchResults":[{"id":23000,"name":"Default Partition","defaultPartition":true,"priority":1,"deviceCount":0,"configurationCount":13,"policyCount":4,"deviceGroupCount":4}],"offset":0,"limit":50}}`)
	mustContain(t, m.run(t, "space", "list"), "Default Partition", "23000")

	m.on("GET /api/v1/ec", `{"errors":null,"result":{"searchResults":[{"id":28723,"name":"ppp091.auto.mobileiron.com","registrationState":"REGISTERED","kocabVersion":"69.0.0.24","enabled":true,"available":false,"lastConnectedAt":1591741596537,"loggingLevel":"INFO"}],"totalCount":1,"offset":0,"limit":50}}`)
	mustContain(t, m.run(t, "connector", "status"), "ppp091.auto.mobileiron.com", "REGISTERED", "2020-06-09")
}

func TestConfigListUsesSpaceAndPaging(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/policy", `{"errors":null,"result":{"totalCount":10,"queryTime":0,"searchResults":[{"id":35000,"name":"System Root Certificate","policyType":"CERTIFICATE","enabled":true,"priority":1,"totalDeviceCount":0,"installedDeviceCount":0,"modifiedAt":1381909930374}],"offset":0,"limit":50}}`)
	out := m.run(t, "config", "list", "-q", "root", "--limit", "1")
	mustContain(t, out, "System Root Certificate", "CERTIFICATE", "Showing 1 of 10")
	wantQuery(t, m.last(t, "GET", "/api/v1/policy").Query, map[string]string{
		"dmPartitionId": "23000", "tag": "DEVICE_CONFIGURATION", "q": "root", "rows": "1", "start": "0"})

	m.on("GET /api/v1/sentry/config", `{"errors":null,"result":{"searchResults":[{"id":50000,"name":"SentryProfile","configurationType":"PROFILE","deletable":true,"isDefaultAlcorProfile":true}],"totalCount":1,"offset":0,"limit":50}}`)
	mustContain(t, m.run(t, "config", "sentry"), "SentryProfile")
	wantQuery(t, m.last(t, "GET", "/api/v1/sentry/config").Query, map[string]string{"configurationType": "PROFILE"})
}

func TestConfigAssign(t *testing.T) {
	m := newMock(t)
	m.on("PUT /api/v1/policy/{id}/rule_groups", `{"errors":null,"result":true}`)
	out := m.run(t, "config", "assign", "35140", "38002,38003")
	mustContain(t, out, "Assigned configuration 35140 to 2 device group(s)")
	wantQuery(t, m.last(t, "PUT", "/api/v1/policy/35140/rule_groups").Query, map[string]string{"ruleGroupIds": "38002,38003", "append": "true"})

	if _, err := m.runErr(t, "config", "assign", "35141", "38002", "--replace"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("--replace without --yes: want refusal, got %v", err)
	}
	if n := m.count("PUT", "/api/v1/policy/35141/rule_groups"); n != 0 {
		t.Fatalf("refused --replace still sent %d request(s)", n)
	}
	m.run(t, "config", "assign", "35141", "38002", "--replace", "--yes")
	wantQuery(t, m.last(t, "PUT", "/api/v1/policy/35141/rule_groups").Query, map[string]string{"append": "false"})

	if _, err := m.runErr(t, "config", "assign", "abc", "1"); err == nil {
		t.Fatal("non-numeric config id accepted")
	}
	// An empty shell variable must not become "replace with no groups".
	if _, err := m.runErr(t, "config", "assign", "35142", "", "--replace", "--yes"); err == nil {
		t.Fatal("empty group list accepted")
	}
}

func TestAttributeCRUD(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/customattributes", `{"errors":null,"result":{"searchResults":[{"id":197,"createdAt":1488590903744,"type":"ACCOUNT","name":"firstName"}],"totalCount":1,"offset":0,"limit":50}}`)
	mustContain(t, m.run(t, "attribute", "list", "--type", "account"), "firstName", "ACCOUNT")
	wantQuery(t, m.last(t, "GET", "/api/v1/customattributes").Query, map[string]string{"type": "account"})

	m.on("POST /api/v1/customattributes", `{"errors":null,"result":[{"id":196079,"type":"DEVICE","dataType":"TEXT","isPreserved":false,"name":"attribute1"}]}`)
	mustContain(t, m.run(t, "attribute", "create", "attribute1", "--type", "DEVICE"), "196079", "attribute1")
	jsonEq(t, m.last(t, "POST", "/api/v1/customattributes").Body,
		`{"attrs":[{"dataType":"TEXT","isPreserved":false,"name":"attribute1","type":"DEVICE"}]}`)

	m.on("PUT /api/v1/customattributes", `{"errors":null,"result":1}`)
	mustContain(t, m.run(t, "attribute", "update", "86972", "--name", "attribute1", "--type", "DEVICE"), "Updated custom attribute 86972")
	jsonEq(t, m.last(t, "PUT", "/api/v1/customattributes").Body, `{"attrs":[{"id":"86972","name":"attribute1","type":"DEVICE"}]}`)
	m.run(t, "attribute", "update", "86972", "--name", "a", "--type", "DEVICE", "--preserved")
	jsonEq(t, m.last(t, "PUT", "/api/v1/customattributes").Body, `{"attrs":[{"id":"86972","isPreserved":true,"name":"a","type":"DEVICE"}]}`)

	m.on("DELETE /api/v1/customattributes", `{"errors":null,"result":1}`)
	if _, err := m.runErr(t, "attribute", "delete", "182626"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("delete without --yes: want refusal, got %v", err)
	}
	if n := m.count("DELETE", "/api/v1/customattributes"); n != 0 {
		t.Fatalf("refused delete still sent %d request(s)", n)
	}
	mustContain(t, m.run(t, "attribute", "delete", "182626,182627", "-y"), "Deleted 2")
	jsonEq(t, m.last(t, "DELETE", "/api/v1/customattributes").Body, `{"ids":[182626,182627]}`)
}

func TestOIDCClient(t *testing.T) {
	m := newMock(t)
	const guid = "1181cdec-f141-4de3-b71a-36fc26ab9a4a"
	resp := `{"errors":null,"result":{"clientId":"CID","clientSecret":"S3CRET","accountGuid":"` + guid + `"}}`
	m.on("POST /api/v1/oidc/client", resp)
	m.on("POST /api/v1/oidc/secret-reset", resp)

	out := m.run(t, "oidc", "create", guid)
	mustContain(t, out, "clientId:", "CID", "clientSecret:", "S3CRET", "shown only once", "auth login --url "+m.srv.URL+" --client-id CID")
	jsonEq(t, m.last(t, "POST", "/api/v1/oidc/client").Body, `{"accountGuid":"`+guid+`"}`)

	if _, err := m.runErr(t, "oidc", "create", "not-a-guid"); err == nil {
		t.Fatal("invalid GUID accepted")
	}
	if _, err := m.runErr(t, "oidc", "reset-secret", guid); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("reset-secret without --yes: want refusal, got %v", err)
	}
	mustContain(t, m.run(t, "oidc", "reset-secret", guid, "--yes"), "S3CRET")
	jsonEq(t, m.last(t, "POST", "/api/v1/oidc/secret-reset").Body, `{"accountGuid":"`+guid+`"}`)
}

func TestCertDownloads(t *testing.T) {
	m := newMock(t)
	m.on("GET /c/ca/{tenant}/{file}", "CRL-DER")
	m.on("GET /c/ca/{tenant}/{dmpart}/{file}", "CRL-SPACE")

	out := filepath.Join(t.TempDir(), "aws.crl")
	m.run(t, "cert", "crl", "42591", "AWS CA", "--pem", "--out", out)
	if data, _ := os.ReadFile(out); string(data) != "CRL-DER" {
		t.Fatalf("saved %q", data)
	}
	wantQuery(t, m.last(t, "GET", "/c/ca/42591/AWS CA.crl").Query, map[string]string{"pem": "true"})
	if _, err := m.runErr(t, "cert", "crl", "42591", "AWS CA", "--out", out); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("overwrite: want refusal, got %v", err)
	}

	mustContain(t, m.run(t, "cert", "crl", "42591", "AWS CA", "--space", "24000"), "CRL-SPACE")
	m.last(t, "GET", "/c/ca/42591/24000/AWS CA.crl")
	if _, err := m.runErr(t, "cert", "crl", "42591", "../x"); err == nil {
		t.Fatal("CA name with a slash accepted")
	}

	csv := "caID,caName,certHash,mdmChannelId,notAfter,notBefore,serialNumber,storeState,subject,usageType\n18000,ExternalCA1,,,Tue Jun 08 14:20:15 IST 2049,Mon Jun 17 14:20:19 IST 2019,1007,Manual_Revoke,\"CN=x\",\n"
	m.on("GET /api/v1/ca/getCertStatus/{ca}", csv)
	mustContain(t, m.run(t, "cert", "revocation-pending", "attca"), "ExternalCA1,,,Tue Jun 08")
	wantQuery(t, m.last(t, "GET", "/api/v1/ca/getCertStatus/attca").Query, map[string]string{"format": "csv"})

	m.on("GET /api/v1/ca/getCertStatusOnDate", csv)
	mustContain(t, m.run(t, "cert", "revoked-on", "09-12-2021"), "Manual_Revoke")
	wantQuery(t, m.last(t, "GET", "/api/v1/ca/getCertStatusOnDate").Query, map[string]string{"format": "csv", "manuallyRevokeDate": "09-12-2021"})
	if _, err := m.runErr(t, "cert", "revoked-on", "2021-12-09"); err == nil {
		t.Fatal("yyyy-mm-dd date accepted")
	}
}

func TestCertUploadAndClearKeys(t *testing.T) {
	m := newMock(t)
	p12, ids := tempFile(t, "smes.p12", "P12DATA"), tempFile(t, "ids.csv", "certificateIds\n3\n7\n")

	m.on("POST /api/v1/user_provided", `{"errors":null,"result":true}`)
	m.run(t, "cert", "upload", p12, "--user", "jdoe@example.com", "--policy", "35037", "--password", "123456", "-f", "accountId=10200")
	r := m.last(t, "POST", "/api/v1/user_provided")
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		t.Fatalf("content type %q", r.Header.Get("Content-Type"))
	}
	mustContain(t, r.Body, `name="certData"; filename="smes.p12"`, "P12DATA",
		"name=\"policyId\"\r\n\r\n35037", "name=\"accountUid\"\r\n\r\njdoe@example.com",
		"name=\"password\"\r\n\r\n123456", "name=\"accountId\"\r\n\r\n10200")

	m.on("DELETE /api/v1/user_provided/clearPrivateKey/csv", `{"errors":null,"result":{"numberOfPrivateKeysCleared":1,"invalidCertificateIds":[3]}}`)
	if _, err := m.runErr(t, "cert", "clear-keys", "--csv", ids); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("clear-keys without --yes: want refusal, got %v", err)
	}
	out := m.run(t, "cert", "clear-keys", "--csv", ids, "--yes")
	mustContain(t, out, "numberOfPrivateKeysCleared:", "invalidCertificateIds:")
	mustContain(t, m.last(t, "DELETE", "/api/v1/user_provided/clearPrivateKey/csv").Body, `name="csv-data"; filename="ids.csv"`, "certificateIds\n3\n7")

	m.on("DELETE /api/v1/user_provided/clearPrivateKey", `{"errors":null,"result":{"numberOfPrivateKeysCleared":5,"invalidAccountIds":[],"invalidIdentityCertificateUPPolicyIds":[]}}`)
	if _, err := m.runErr(t, "cert", "clear-keys", "--account", "10200", "--policy", "35497"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("clear-keys --account without --yes: want refusal, got %v", err)
	}
	if n := m.count("DELETE", "/api/v1/user_provided/clearPrivateKey"); n != 0 {
		t.Fatalf("refused clear-keys still sent %d request(s)", n)
	}
	mustContain(t, m.run(t, "cert", "clear-keys", "--account", "10200", "--policy", "35497,35498", "--yes"), "5")
	jsonEq(t, m.last(t, "DELETE", "/api/v1/user_provided/clearPrivateKey").Body,
		`{"accountAndUPPolicyWrappers":[{"accountId":10200,"userProvidedCertificatePolicyIds":[35497,35498]}]}`)

	if _, err := m.runErr(t, "cert", "clear-keys", "--csv", ids, "--account", "1", "--yes"); err == nil {
		t.Fatal("--csv with --account accepted")
	}
}

func TestDepProfilesAndMDMProfile(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/depServer/{id}/profiles", `{"errors":null,"result":[{"id":51799,"uuid":"27EF7B79448A485D9CF9F68038BB18DB","profileName":"QEJoe","department":"QE-2","mandatory":false,"mdmRemovable":true,"supervised":false,"virtualServerId":198,"status":"ACTIVE","numOfDevices":0,"default":true}]}`)
	m.on("GET /api/v2/depServer/{id}/profiles", `{"errors":null,"result":{"searchResults":[{"id":51800,"profileName":"V2Profile"}],"totalCount":1}}`)
	mustContain(t, m.run(t, "dep", "profiles", "198"), "QEJoe", "ACTIVE")
	mustContain(t, m.run(t, "dep", "profiles", "198", "--v2"), "V2Profile")
	if _, err := m.runErr(t, "dep", "profiles", "0"); err == nil || !strings.Contains(err.Error(), "invalid id") {
		t.Fatalf("dep profiles 0: want invalid id, got %v", err)
	}

	m.on("POST /c/i/dep/custom.mobileconfig", "<plist>profile</plist>")
	out := filepath.Join(t.TempDir(), "enroll.mobileconfig")
	m.run(t, "dep", "mdm-profile", "--user", "jdoe@example.com", "--device-info", "SIGNEDINFO", "--out", out)
	if data, _ := os.ReadFile(out); string(data) != "<plist>profile</plist>" {
		t.Fatalf("saved %q", data)
	}
	r := m.last(t, "POST", "/c/i/dep/custom.mobileconfig")
	wantQuery(t, r.Query, map[string]string{"user": "jdoe@example.com"})
	if got := r.Header.Get("x-apple-aspen-deviceinfo"); got != "SIGNEDINFO" {
		t.Fatalf("device info header = %q", got)
	}
}
