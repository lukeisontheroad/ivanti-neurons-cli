package cmd

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wantQuery fails unless q holds each key with the given value (repeated
// keys joined by ","; "" asserts absence).
func wantQuery(t *testing.T, q url.Values, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got := strings.Join(q[k], ","); got != v {
			t.Errorf("query %s = %q, want %q (query: %v)", k, got, v, q)
		}
	}
}

// tempFile writes content to a new temp file called name.
func tempFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const appListFixture = `{"errors":null,"result":{"totalCount":142,"queryTime":11,"searchResults":[
 {"name":"2048","id":18122,"platformAppId":"com.ketchapp.2048","platformType":"IOS","appType":"IN_HOUSE","cmPartitionId":20003,"latestDisplayVersion":"1.8","createdAt":1401908341993}],
 "offset":0,"limit":20}}`

func TestAppListInHouse(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/app", appListFixture)
	out := m.run(t, "app", "list", "--type", "in_house", "--limit", "1", "-q", "2048")
	mustContain(t, out, "2048", "com.ketchapp.2048", "1.8", "Showing 1 of 142")
	wantQuery(t, m.last(t, "GET", "/api/v1/app").Query, map[string]string{
		"cmPartitionId": "20000", "excludeContainerApps": "false", "fq": "appType='IN_HOUSE'",
		"q": "2048", "rows": "1", "start": "0",
	})
}

func TestAppView(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/app/{id}", `{"errors":null,"result":{"id":181235,"platformAppId":"com.argo.perf.gJD","platformType":"ANDROID","appType":"PUBLIC","dmPartitionId":24996,"developerName":"Argo","name":"Argo noKL","latestAppVersionString":"1.1",
	 "versions":[{"name":"Argo noKL","secondaryVersion":"1.1","displayVersion":"1.1","applicationVersionId":147803,"size":0}]}}`)
	out := m.run(t, "app", "view", "181235")
	mustContain(t, out, "name:", "Argo noKL", "com.argo.perf.gJD", "APPLICATIONVERSIONID", "147803")
	if _, err := m.runErr(t, "app", "view", "../tenant"); err == nil {
		t.Fatal("expected invalid id error")
	}
}

func TestAppInventoryDeviceV1(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/app/inventory", `{"errors":null,"result":{"searchResults":[{"deviceId":87178,"name":"_-_","identifier":"com.sm.keyslide","version":"2","displayVersion":"1.2","appType":"PUBLIC","platformType":"IOS","managed":false}],"totalCount":1,"offset":0,"limit":20}}`)
	facet := "INSTALLTYPE != 'SYSTEM_APP'"
	out := m.run(t, "app", "inventory", "device", "87178", "--facet-fq", facet)
	mustContain(t, out, "com.sm.keyslide", "1.2", "PUBLIC")
	wantQuery(t, m.last(t, "GET", "/api/v1/app/inventory").Query, map[string]string{
		"deviceId": "87178", "dmPartitionId": "23000", "facetFq": facet, "rows": "50", "start": "0",
	})
}

func TestAppInventoryDevicesV2(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v2/app/inventory", `{"errors":null,"results":[
	 {"deviceId":1,"platformType":"ANDROID","appInventory":[{"identifier":"com.example.app1","name":"ExampleApp1","displayVersion":"1.0","sourceType":"store","installType":"user","managed":false}]},
	 {"deviceId":2,"platformType":"ANDROID","appInventory":[{"identifier":"com.example.app2","name":"ExampleApp2","displayVersion":"2.0","sourceType":"store","installType":"user","managed":true}]}]}`)
	out := m.run(t, "app", "inventory", "device", "1,2")
	mustContain(t, out, "DEVICEID", "com.example.app1", "com.example.app2", "store")
	wantQuery(t, m.last(t, "GET", "/api/v2/app/inventory").Query, map[string]string{"deviceIds": "1,2"})
	if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) != 3 {
		t.Errorf("want header + one row per device app, got:\n%s", out)
	}
	out = m.run(t, "app", "inventory", "device", "1", "--v2", "-o", "json")
	mustContain(t, out, `"appInventory"`)
	wantQuery(t, m.last(t, "GET", "/api/v2/app/inventory").Query, map[string]string{"deviceId": "1", "deviceIds": ""})
}

func TestAppInventoryList(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/applist", `{"errors":null,"result":[{"searchResults":[{"name":"Outlook","bundleId":"com.microsoft.Office.Outlook"}],"totalCount":1,"offset":0,"limit":10}]}`)
	out := m.run(t, "app", "inventory", "list", "--limit", "10")
	mustContain(t, out, `"bundleId":"com.microsoft.Office.Outlook"`)
	wantQuery(t, m.last(t, "GET", "/api/v1/applist").Query, map[string]string{"rows": "10", "start": "0", "nav": "true"})
}

func TestAppEligiblePrintsGUIDs(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/app/eligibledevices", `{"errors":null,"result":["3fc9c1df-4f87-4db5-9569-362064a1c2ce","17045366-798e-4a96-9a1c-bbe72db6e020"]}`)
	out := m.run(t, "app", "eligible", "com.mobileiron.anyware.ios")
	if out != "3fc9c1df-4f87-4db5-9569-362064a1c2ce\n17045366-798e-4a96-9a1c-bbe72db6e020\n" {
		t.Errorf("want one GUID per line, got %q", out)
	}
	wantQuery(t, m.last(t, "GET", "/api/v1/app/eligibledevices").Query, map[string]string{
		"dmPartitionId": "23000", "appId": "com.mobileiron.anyware.ios", "platformType": "IOS", "appVersion": "",
	})
}

func TestAppGetCommands(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/asynctask/status/{id}", `{"errors":null,"result":{"taskUuid":"8cf7b560-1172-418e-a9cc-0db7197ffe9f","taskType":"CLEAR_CONFIG_ERROR","taskStatus":"COMPLETE","errorConfigsCleared":56}}`)
	m.on("GET /api/v1/app/version/{id}/distribution", `{"errors":null,"result":{"applicationVersionId":147803,"appDistributionMutable":true}}`)
	out := m.run(t, "app", "task", "status", "8cf7b560-1172-418e-a9cc-0db7197ffe9f")
	mustContain(t, out, "errorConfigsCleared:", "56")
	out = m.run(t, "app", "distribution", "147803")
	mustContain(t, out, "appDistributionMutable:", "true")
	m.last(t, "GET", "/api/v1/app/version/147803/distribution")
	before := len(m.reqs)
	if _, err := m.runErr(t, "app", "distribution", "../x"); err == nil || len(m.reqs) != before {
		t.Fatalf("expected invalid id rejected before any request, got %v", err)
	}
}

func TestAppStoreSearchItunes(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v2/app/itunes", `{"errors":null,"result":[{"name":"Outlook","developerName":"Magzter Inc.","version":"6.10","appStoreId":486362804,"bundleId":"com.dci.outlook","deviceFamily":"ALL"}]}`)
	out := m.run(t, "app", "store", "search", "itunes", "outlook", "--country", "ch")
	mustContain(t, out, "com.dci.outlook", "486362804")
	wantQuery(t, m.last(t, "GET", "/api/v2/app/itunes").Query, map[string]string{
		"q": "outlook", "rows": "50", "countryCode": "CH", "cmPartitionId": "20000",
	})
	if _, err := m.runErr(t, "app", "store", "search", "amazon", "x"); err == nil {
		t.Fatal("expected unknown store error")
	}
}

func TestAppAvailableDeviceGroup(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/app/rulegroup/{id}", `{"errors":null,"result":{"searchResults":[{"applicationVersionId":211531,"platformType":"ANDROID","bundleId":"notizen.bloc.notes","name":"ClearNote","displayVersion":"1.4.3"}],"totalCount":1,"offset":0,"limit":20}}`)
	out := m.run(t, "app", "available", "device-group", "228087")
	mustContain(t, out, "ClearNote", "211531")
	wantQuery(t, m.last(t, "GET", "/api/v1/app/rulegroup/228087").Query, map[string]string{"nav": "true", "rows": "50"})
}

// The user and device lists are search pages too: paged, with a hint when cut.
func TestAppAvailableUserPages(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/app/user/{id}", `{"errors":null,"result":{"totalCount":30,"searchResults":[{"applicationVersionId":18437,"name":"Adobe Reader"}],"offset":0,"limit":20}}`)
	mustContain(t, m.run(t, "app", "available", "user", "10009", "--limit", "1"), "Adobe Reader", "Showing 1 of 30")
	wantQuery(t, m.last(t, "GET", "/api/v1/app/user/10009").Query, map[string]string{"rows": "1", "start": "0", "nav": ""})
}

func TestAppTaskList(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/asynctask/all", `{"errors":null,"result":{"noOfPollRequestsRemaining":13,"nextIntervalInMillis":1000,"taskList":[
	 {"taskId":5,"taskType":"APP_INSTALL_DIST","taskStatus":"COMPLETE","createdAt":1649264141176,"description":"App Pocket Planes 1.2.4 install/update"}]}}`)
	out := m.run(t, "app", "task", "list")
	mustContain(t, out, "TASKID", "APP_INSTALL_DIST", "COMPLETE", "2022-04-06")
}

// TestAppWrites checks method, path, query, and body of every write shape.
func TestAppWrites(t *testing.T) {
	ipa, png1, png2 := tempFile(t, "PayDay.ipa", "bin:PayDay.ipa"), tempFile(t, "home.png", "png"), tempFile(t, "ipad.png", "png")
	guid1, guid2 := "b8b55026-502b-4f64-a422-f1f094aec6ba", "6ff689c0-21c7-47a8-a979-3f35c989667d"
	tasks := `{"errors":null,"result":{"noOfPollRequestsRemaining":13,"nextIntervalInMillis":1000,"taskList":[{"taskId":40,"taskType":"APP_INSTALL_GUID","taskStatus":"QUEUED","createdAt":1650473330150,"description":"App install/update for 2 device(s)"}]}}`
	tests := []struct {
		name         string
		args         []string
		route, resp  string
		method, path string
		query        map[string]string
		body         string   // exact JSON body
		bodyHas      []string // multipart fragments
		out          string
	}{
		{
			name:  "create upload",
			args:  []string{"app", "create", ipa, "--platform", "ios", "--new-version", "-f", "overrideUrl=https://apps.example.com"},
			route: "POST /api/v1/app", resp: `{"errors":null,"result":{"addedToExisitingApp":true,"applicationId":16001,"applicationVersionId":17002}}`,
			method: "POST", path: "/api/v1/app",
			bodyHas: []string{`name="app-data"; filename="PayDay.ipa"`, "bin:PayDay.ipa", "name=\"platformType\"\r\n\r\nIOS", "name=\"newVersion\"\r\n\r\ntrue", "name=\"cmPartitionId\"\r\n\r\n20000", "name=\"overrideUrl\"\r\n\r\nhttps://apps.example.com"},
			out:     "17002",
		},
		{
			name:  "create public by itunes id",
			args:  []string{"app", "create", "--itunes-id", "422020153"},
			route: "POST /api/v1/app", resp: `{"errors":null,"result":true}`,
			method: "POST", path: "/api/v1/app", query: map[string]string{"itunes-id": "422020153"},
			out: "App created",
		},
		{
			name:  "delete version",
			args:  []string{"app", "delete", "16003", "17004", "--yes"},
			route: "DELETE /api/v1/app/{id}/version", resp: `{"errors":null,"result":true}`,
			method: "DELETE", path: "/api/v1/app/16003/version", query: map[string]string{"cmPartitionId": "20000", "id": "17004"},
			out: "Deleted version 17004 of app 16003",
		},
		{
			name:  "store add enterprise app",
			args:  []string{"app", "store", "add", "com.photography.goallearn", "-f", "categoryIds=[71482]", "-f", "enableReviews=true"},
			route: "POST /api/v2/app/public/google/{id}", resp: `{"errors":null,"result":{"applicationId":191446,"applicationVersionId":155769,"addedToExisitingApp":false}}`,
			method: "POST", path: "/api/v2/app/public/google/com.photography.goallearn",
			body: `{"dmPartitionId":23000,"platformType":"ANDROID","appType":"PUBLIC","categoryIds":[71482],"enableReviews":true}`,
			out:  "155769",
		},
		{
			name:  "inhouse update",
			args:  []string{"app", "inhouse", "update", "54710", "--data", `{"id":43488,"name":"Words against the World","dmPartitionId":23505}`, "-f", "whatsNew=Better memory management.", "--repush"},
			route: "PUT /api/v2/app/inhouse/{id}", resp: `{"errors":null,"result":true}`,
			method: "PUT", path: "/api/v2/app/inhouse/54710", query: map[string]string{"repushApp": "true"},
			body: `{"id":43488,"name":"Words against the World","dmPartitionId":23505,"whatsNew":"Better memory management.","appType":"IN_HOUSE"}`,
			out:  "Updated app version 54710",
		},
		{
			name:  "inhouse screenshots",
			args:  []string{"app", "inhouse", "screenshots", "52161", "--phone", png1, "--tablet", png2},
			route: "POST /api/v1/app/version/{id}/screenshots", resp: `{"errors":null,"result":true}`,
			method: "POST", path: "/api/v1/app/version/52161/screenshots",
			bodyHas: []string{`name="phoneScreenshots[0].file"; filename="home.png"`, `name="tabletScreenshots[0].file"; filename="ipad.png"`},
			out:     "Uploaded 2 screenshot(s)",
		},
		{
			name:  "inhouse icon",
			args:  []string{"app", "inhouse", "icon", "191477", png1},
			route: "POST /api/v2/app/inhouse/{id}/icon", resp: `data:image/png;base64,iVBORw0KGgo=`,
			method: "POST", path: "/api/v2/app/inhouse/191477/icon", query: map[string]string{"preview": "true", "dataUri": "true"},
			bodyHas: []string{`name="image-data"; filename="home.png"`},
			out:     "Updated icon of app version 191477",
		},
		{
			name:  "inhouse copy metadata",
			args:  []string{"app", "inhouse", "copy", "17002"},
			route: "PUT /api/v1/app", resp: `{"errors":null,"result":true}`,
			method: "PUT", path: "/api/v1/app", query: map[string]string{"targetApplicationVersionId": "17002"},
			out: "Copied previous version metadata to app version 17002",
		},
		{
			name:  "install devices async",
			args:  []string{"app", "install", "devices", "com.eeenmachine.pocketplanes", guid1 + "," + guid2, "--app-version", "1.2.4", "--async"},
			route: "PUT /api/v2/app/install/async", resp: tasks,
			method: "PUT", path: "/api/v2/app/install/async",
			body: `{"guids":["` + guid1 + `","` + guid2 + `"],"dmPartitionId":23000,"bundleId":"com.eeenmachine.pocketplanes","appVersion":"1.2.4","platformType":"IOS"}`,
			out:  "APP_INSTALL_GUID",
		},
		{
			name:  "install devices sync skips ineligible",
			args:  []string{"app", "install", "devices", "com.playgendary.bowmasters", guid1},
			route: "PUT /api/v2/app/install", resp: `{"errors":null,"result":{"numberOfTriggeredAppInstalls":0,"deviceGuidsNotEligibleForApp":["` + guid1 + `"]}}`,
			method: "PUT", path: "/api/v2/app/install",
			body: `{"guids":["` + guid1 + `"],"dmPartitionId":23000,"bundleId":"com.playgendary.bowmasters","platformType":"IOS"}`,
			out:  "deviceGuidsNotEligibleForApp:",
		},
		{
			name:  "install by distribution",
			args:  []string{"app", "install", "dist", "17000", "--filter", "4", "--users", "10050,10051", "--user-groups", "11000", "--update"},
			route: "PUT /api/v2/app/{id}/install", resp: `{"errors":null,"result":3}`,
			method: "PUT", path: "/api/v2/app/17000/install",
			body: `{"appVersionId":17000,"dmPartitionId":23000,"appDeviceFilterId":4,"accountIds":[10050,10051],"accountGroupIds":[11000],"requestNewInstall":false,"requestUpdate":true,"everyoneWithApp":false}`,
			out:  "3",
		},
		{
			name:  "install by distribution async",
			args:  []string{"app", "install", "dist", "17003", "--everyone-with-app", "--new-install", "--async"},
			route: "PUT /api/v2/app/{id}/install/async", resp: tasks,
			method: "PUT", path: "/api/v2/app/17003/install/async",
			body: `{"appVersionId":17003,"dmPartitionId":23000,"requestNewInstall":true,"requestUpdate":false,"everyoneWithApp":true}`,
			out:  "QUEUED",
		},
		{
			name:  "task retry",
			args:  []string{"app", "task", "retry"},
			route: "POST /api/v1/asynctask", resp: `{"errors":null,"result":{"taskUuid":"8cf7b560-1172-418e-a9cc-0db7197ffe9f","taskType":"CLEAR_CONFIG_ERROR","taskStatus":"QUEUED","createdAt":1701099803091}}`,
			method: "POST", path: "/api/v1/asynctask", query: map[string]string{"taskGuid": "CLEAR_CONFIG_ERROR"},
			out: "8cf7b560-1172-418e-a9cc-0db7197ffe9f",
		},
		{
			name:  "review add",
			args:  []string{"app", "review", "add", "17026", "--rating", "5", "--title", "Must have!", "--summary", "Great little app!"},
			route: "POST /c/api/v1/appstore/{id}/review", resp: `{"errors":null,"result":true}`,
			method: "POST", path: "/c/api/v1/appstore/17026/review", query: map[string]string{"rating": "5", "title": "Must have!", "summary": "Great little app!"},
			out: "Reviewed app version 17026",
		},
		{
			name:  "review delete",
			args:  []string{"app", "review", "delete", "49023,49024", "--yes"},
			route: "DELETE /api/v1/app/review/delete", resp: `{"errors":null,"result":2}`,
			method: "DELETE", path: "/api/v1/app/review/delete", query: map[string]string{"cmPartitionId": "20000", "id": "49023,49024"},
			out: "Deleted 2 review(s)",
		},
		{
			name:  "config create",
			args:  []string{"app", "config", "create", "121646", "-f", "name=ForDocs", "-f", "groupIds=[50918]", "-f", `settings={"type":"INSTALL","required":true,"priority":"LOW"}`},
			route: "POST /api/v1/app/version/{id}/config", resp: `{"errors":null,"result":276182}`,
			method: "POST", path: "/api/v1/app/version/121646/config",
			body: `{"type":"INSTALL","name":"ForDocs","groupIds":[50918],"settings":{"type":"INSTALL","required":true,"priority":"LOW"}}`,
			out:  "Created app config 276182",
		},
		{
			name:  "config update",
			args:  []string{"app", "config", "update", "121646", "276183", "--data", `{"id":999,"applicationVersionId":998}`, "-f", "name=ForDocs", "-f", "priority=1"},
			route: "PUT /api/v1/app/version/{id}/config/{cid}", resp: `{"errors":null,"result":276183}`,
			method: "PUT", path: "/api/v1/app/version/121646/config/276183",
			body: `{"id":276183,"applicationVersionId":121646,"type":"INSTALL","name":"ForDocs","priority":1}`,
			out:  "Updated app config 276183",
		},
		{
			name:  "attrs set version",
			args:  []string{"app", "attrs", "set", "165764", "key=test", "key=two", "--yes"},
			route: "POST /api/v1/app/version/{id}/customattributes", resp: `{"attrs":{"key":["test","two"]}}`,
			method: "POST", path: "/api/v1/app/version/165764/customattributes",
			body: `{"attrs":{"key":["test","two"]}}`,
			out:  "Replaced custom attributes of app version 165764",
		},
		{
			name:  "attrs update version",
			args:  []string{"app", "attrs", "update", "165764", "key=value2"},
			route: "PATCH /api/v1/app/version/{id}/customattributes", resp: `1`,
			method: "PATCH", path: "/api/v1/app/version/165764/customattributes",
			body: `{"attrs":{"key":["value2"]},"forceOverwrite":false}`,
		},
		{
			name:  "attrs update app",
			args:  []string{"app", "attrs", "update", "16005", "key12=value101", "key12=value102", "--app", "--overwrite"},
			route: "PATCH /api/v1/app/{id}/customattributes", resp: `1`,
			method: "PATCH", path: "/api/v1/app/16005/customattributes",
			body: `{"attrs":{"key12":["value101","value102"]},"forceOverwrite":true}`,
			out:  "Updated custom attributes of app 16005",
		},
		{
			name:  "attrs update apps bulk",
			args:  []string{"app", "attrs", "update", "16005,16006", "key12=value101", "--app", "--overwrite"},
			route: "PATCH /api/v1/app/customattributes", resp: `1`,
			method: "PATCH", path: "/api/v1/app/customattributes",
			body: `{"ids":[16005,16006],"attrs":{"attrs":{"key12":["value101"]}},"forceOverwrite":true}`,
			out:  "Updated custom attributes of 2 apps",
		},
		{
			name:  "attrs delete version",
			args:  []string{"app", "attrs", "delete", "165764", "key=test", "--yes"},
			route: "DELETE /api/v1/app/version/{id}/customattributes", resp: `1`,
			method: "DELETE", path: "/api/v1/app/version/165764/customattributes",
			body: `{"attrs":{"key":["test"]}}`,
			out:  "Removed custom attributes of app version 165764",
		},
		{
			name:  "attrs delete apps bulk",
			args:  []string{"app", "attrs", "delete", "16005,16006", "key12=", "a=1", "--app", "--yes"},
			route: "DELETE /api/v1/app/customattributes", resp: `1`,
			method: "DELETE", path: "/api/v1/app/customattributes",
			body: `{"ids":[16005,16006],"attrKeys":["a","key12"]}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMock(t)
			m.on(tc.route, tc.resp)
			out := m.run(t, tc.args...)
			mustContain(t, out, tc.out)
			req := m.last(t, tc.method, tc.path)
			wantQuery(t, req.Query, tc.query)
			if tc.body != "" {
				jsonEq(t, req.Body, tc.body)
			}
			mustContain(t, req.Body, tc.bodyHas...)
		})
	}
}

func TestAppDestructiveNeedConfirmation(t *testing.T) {
	for _, args := range [][]string{
		{"app", "delete", "16003", "17004"},
		{"app", "review", "delete", "49023"},
		{"app", "attrs", "delete", "165764", "key=test"},
	} {
		m := newMock(t)
		_, err := m.runErr(t, args...)
		if err == nil || !strings.Contains(err.Error(), "--yes") {
			t.Errorf("%v: expected refusal without --yes, got %v", args, err)
		}
		// a bad --output fails before the confirmation
		if _, err := m.runErr(t, append(args, "-o", "yaml")...); err == nil || !strings.Contains(err.Error(), "invalid --output") {
			t.Errorf("%v -o yaml: want invalid --output, got %v", args, err)
		}
		for _, r := range m.reqs {
			if r.Method == "DELETE" {
				t.Errorf("%v: DELETE sent without confirmation", args)
			}
		}
	}
}

func TestAppArgValidation(t *testing.T) {
	for _, args := range [][]string{
		{"app", "install", "devices", "com.example.app", "not-a-guid"},
		{"app", "install", "dist", "17000"},
		{"app", "create", "--platform", "IOS"},
		{"app", "create", "app.ipa"},
		{"app", "review", "add", "17026", "--rating", "6"},
		{"app", "store", "view", "google", "../../x"},
		{"app", "attrs", "update", "1,2", "k=v"},
		{"app", "inhouse", "screenshots", "52161"},
		{"app", "attrs", "set", ",", "k=v"},
		{"app", "store", "add", "com.example.app", "--data", "null"},
		{"app", "install", "dist", "17000", "--update", "--users", "10050,abc"},
		{"app", "install", "dist", "17000", "--update"}, // no target
		{"app", "install", "dist", "17000", "--update", "--everyone-with-app", "--users", ""},
		{"app", "inhouse", "update", "54710", "--repush"}, // empty replace body
		{"app", "config", "update", "121646", "276183"},
	} {
		m := newMock(t)
		if _, err := m.runErr(t, args...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
		if len(m.reqs) > 0 {
			t.Errorf("%v: sent %d request(s) despite invalid arguments", args, len(m.reqs))
		}
	}
}
