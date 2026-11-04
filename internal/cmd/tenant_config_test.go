package cmd

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

// testCertDER returns a fresh self-signed CA certificate (DER).
func testCertDER(t *testing.T) []byte {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Example Test CA"},
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func testCertPEM(t *testing.T) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: testCertDER(t)}))
}

func TestConfigCreateCertificate(t *testing.T) {
	m := newMock(t)
	m.on("POST /api/v1/policy", `{"errors":null,"result":{"policyId":3610087,"name":"Example CA","policyType":"CERTIFICATE","dmPartitionDistributionType":"NONE","views":[{"subjectDN":"CN=Example Test CA"}]}}`)
	out := m.run(t, "config", "create", "certificate", "--name", "Example CA", "--file", tempFile(t, "ca.cer", testCertPEM(t)))
	mustContain(t, out, "policyId:", "3610087", "NONE")
	r := m.last(t, "POST", "/api/v1/policy")
	wantQuery(t, r.Query, map[string]string{"policyType": "CERTIFICATE"})
	mustContain(t, r.Body, `name="certs[0].certData"; filename="ca.cer"`, "-----BEGIN CERTIFICATE-----",
		"name=\"dmPartitionId\"\r\n\r\n23000", "name=\"name\"\r\n\r\nExample CA", "name=\"enabled\"\r\n\r\ntrue",
		"name=\"dmPartitionDistributionType\"\r\n\r\nNONE", "name=\"delegatedDistMutableInSpace\"\r\n\r\nfalse")

	der := testCertDER(t)
	m.run(t, "config", "create", "certificate", "--name", "Example CA", "--description", "Root", "--file", tempFile(t, "ca.der", string(der)))
	mustContain(t, m.last(t, "POST", "/api/v1/policy").Body, string(der), "name=\"description\"\r\n\r\nRoot")

	// Anything but exactly one certificate is refused before the upload.
	key := "-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEIA==\n-----END PRIVATE KEY-----\n"
	for name, content := range map[string]string{"key.pem": key, "cert-and-key.pem": testCertPEM(t) + key, "junk.cer": "not a certificate"} {
		if _, err := m.runErr(t, "config", "create", "certificate", "--name", "x", "--file", tempFile(t, name, content)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if n := m.count("POST", "/api/v1/policy"); n != 2 {
		t.Fatalf("refused files were uploaded: %d POSTs, want 2", n)
	}
}

func TestConfigDistribute(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/policy/{id}/distribution", `{"errors":null,"result":{"accounts":[{"id":10200}],"groups":[],"devices":[{"id":22184,"deviceName":"iPad"}],"ruleGroups":[],"policyId":35140,"dmPartitionId":23000}}`)
	m.on("PUT /api/v1/policy/distribution", `{"errors":null,"result":true}`)

	out := m.run(t, "config", "distribute", "35140", "--device", "22185,22184", "--device-group", "38002")
	mustContain(t, out, "Configuration 35140 now goes to 2 device(s), 1 device group(s), 1 user(s), 0 user group(s)")
	wantQuery(t, m.last(t, "GET", "/api/v1/policy/35140/distribution").Query, map[string]string{"dmPartitionId": "23000"})
	jsonEq(t, m.last(t, "PUT", "/api/v1/policy/distribution").Body,
		`{"policyId":35140,"dmPartitionId":23000,"deviceIds":[22185,22184],"ruleGroupIds":[38002],"accountIds":[10200],"groupIds":[]}`)

	if _, err := m.runErr(t, "config", "distribute", "35140", "--device", "22185", "--replace"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("--replace without --yes: want refusal, got %v", err)
	}
	if n := m.count("PUT", "/api/v1/policy/distribution"); n != 1 {
		t.Fatalf("refused --replace still sent a PUT (%d total)", n)
	}
	m.run(t, "config", "distribute", "35140", "--user", "10201", "--replace", "--yes")
	jsonEq(t, m.last(t, "PUT", "/api/v1/policy/distribution").Body,
		`{"policyId":35140,"dmPartitionId":23000,"deviceIds":[],"ruleGroupIds":[],"accountIds":[10201],"groupIds":[]}`)

	for _, args := range [][]string{{"35140"}, {"35140", "--device", ""}, {"35140", "--device", "abc"}, {"x", "--device", "1"}} {
		if _, err := m.runErr(t, append([]string{"config", "distribute"}, args...)...); err == nil {
			t.Errorf("config distribute %v accepted", args)
		}
	}

	// An unreadable current distribution stops the write instead of dropping recipients.
	m2 := newMock(t)
	m2.on("GET /api/v1/policy/{id}/distribution", `{"errors":null,"result":null}`)
	if _, err := m2.runErr(t, "config", "distribute", "35140", "--device", "1"); err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("unreadable distribution: want refusal, got %v", err)
	}
	if n := m2.count("PUT", ""); n != 0 {
		t.Fatalf("unreadable distribution still sent %d PUT(s)", n)
	}
}

func TestConfigViewDistributionDelete(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/policy/{id}", `{"errors":null,"result":{"policyId":35140,"name":"Example CA","policyType":"CERTIFICATE","dmPartitionDistributionType":"NONE","deletable":true,"modifiedAt":1616701276254}}`)
	mustContain(t, m.run(t, "config", "view", "35140"), "Example CA", "CERTIFICATE", "deletable:", "2021-03-25")
	wantQuery(t, m.last(t, "GET", "/api/v1/policy/35140").Query, map[string]string{"dmPartitionId": "23000"})

	m.on("GET /api/v1/policy/{id}/distribution", `{"errors":null,"result":{"accounts":[],"groups":[],"devices":[{"id":22184,"deviceName":"iPad"}],"ruleGroups":[{"id":38002,"name":"Kiosks"}],"policyId":35140,"dmPartitionId":23000}}`)
	mustContain(t, m.run(t, "config", "distribution", "35140"), "deviceName=iPad", "Kiosks")

	m.on("DELETE /api/v1/policy/{id}", `{"errors":null,"result":1}`)
	if _, err := m.runErr(t, "config", "delete", "35140"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("delete without --yes: want refusal, got %v", err)
	}
	if n := m.count("DELETE", "/api/v1/policy/35140"); n != 0 {
		t.Fatalf("refused delete still sent %d request(s)", n)
	}
	mustContain(t, m.run(t, "config", "delete", "35140", "--yes"), "Deleted configuration 35140")
	wantQuery(t, m.last(t, "DELETE", "/api/v1/policy/35140").Query, map[string]string{"dmPartitionId": "23000"})
}
