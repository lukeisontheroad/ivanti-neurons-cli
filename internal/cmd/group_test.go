package cmd

import (
	"net/url"
	"strings"
	"testing"
)

const (
	groupListJSON = `{"errors":null,"result":{"searchResults":[{"id":11776,"name":"All Users","description":"All Users","source":"LOCAL","mutable":true,
"definition":"ACCOUNTGROUP EQ 'All Users'","membershipStatic":true,"applicationCount":24}],"totalCount":1,"offset":0,"limit":20}}`
	deviceGroupJSON = `{"errors":null,"result":{"id":38010,"dmPartitionId":23000,"name":"iPads","description":"All iPads","type":"DEVICE",
"definition":"PRETTYMODEL EQ 'iPad' ","mutable":true,"memberCount":12,"systemName":null,"membershipStatic":false,"dmPartitionName":"Default Space",
"treeView":{"operator":"LEAF","rule":{"field":"PRETTYMODEL","operator":"EQ","values":["iPad"]},"children":null},"staticMembers":[]}}`
	deviceGroupStaticJSON = `{"errors":null,"result":{"id":47808,"name":"Kiosks","type":"DEVICE","definition":null,"memberCount":1,
"membershipStatic":true,"staticMembers":[200]}}`
	deviceGroupDevicesJSON = `{"errors":null,"result":{"totalCount":1,"searchResults":[{"id":197483,"deviceName":"iPad","platformType":"IOS",
"platformVersion":"17.1","uid":"jdoe@example.com","registrationState":"ACTIVE","lastCheckin":1422649201021}],"offset":0,"limit":50}}`
)

func TestGroupList(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/group", groupListJSON)
	out := m.run(t, "group", "list", "-q", "all")
	mustContain(t, out, "MEMBERSHIPSTATIC", "11776", "All Users", "LOCAL", "24")
	valuesEq(t, m.last(t, "GET", "/api/v1/group").Query, url.Values{"q": {"all"}, "rows": {"50"}, "start": {"0"}})
}

func TestGroupCreate(t *testing.T) {
	m := newMock(t)
	m.on("POST /api/v1/group", `{"errors":null,"result":{"id":2168413,"name":"ForDocs2","source":"LOCAL","mutable":true,"membershipStatic":true}}`)
	out := m.run(t, "group", "create", "ForDocs2", "--description", "Docs")
	mustContain(t, out, `Created user group "ForDocs2" (id 2168413)`)
	req := m.last(t, "POST", "/api/v1/group")
	valuesEq(t, req.Query, url.Values{"name": {"ForDocs2"}, "source": {"LOCAL"}, "membershipStatic": {"true"}, "description": {"Docs"}})
	if req.Body != "" {
		t.Errorf("unexpected body %q", req.Body)
	}

	m.run(t, "group", "create", "Sales", "--rule", "ACCOUNTGROUP EQ 'Managers'", "-f", "source=LDAP_GROUP")
	valuesEq(t, m.last(t, "POST", "/api/v1/group").Query, url.Values{"name": {"Sales"}, "definition": {"ACCOUNTGROUP EQ 'Managers'"},
		"membershipStatic": {"false"}, "source": {"LDAP_GROUP"}})
}

func TestGroupMembers(t *testing.T) {
	m := newMock(t)
	m.on("PUT /api/v1/group/{id}/accounts", `{"errors":null,"result":true}`)
	out := m.run(t, "group", "members", "add", "11003", "10004,10005")
	mustContain(t, out, "Added 2 user(s) to user group 11003")
	valuesEq(t, m.last(t, "PUT", "/api/v1/group/11003/accounts").Query, url.Values{"accountIds": {"10004", "10005"}, "append": {"true"}})

	if _, err := m.runErr(t, "group", "members", "set", "11004", "10004"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("members set ran without confirmation: %v", err)
	}
	m.run(t, "group", "members", "set", "11004", "10004", "--yes")
	valuesEq(t, m.last(t, "PUT", "/api/v1/group/11004/accounts").Query, url.Values{"accountIds": {"10004"}, "append": {"false"}})

	if _, err := m.runErr(t, "group", "members", "add", "11003", "abc"); err == nil || !strings.Contains(err.Error(), "invalid id") {
		t.Fatalf("expected invalid id error, got %v", err)
	}
	if _, err := m.runErr(t, "group", "members", "set", "11004", "", "--yes"); err == nil || !strings.Contains(err.Error(), "no ids") {
		t.Fatalf("blank account list must not clear the group: %v", err)
	}
}

func TestGroupCheckDelete(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/group", `{"errors":null,"result":{"searchResults":[],"totalCount":0,"offset":0,"limit":0}}`)
	out := m.run(t, "group", "check-delete", "4677669")
	mustContain(t, out, "No results.")
	valuesEq(t, m.last(t, "GET", "/api/v1/group").Query, url.Values{"checkDeleteViability": {"true"}, "ids": {"4677669"}})
}

func TestGroupDelete(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/group", `{"errors":null,"result":{"searchResults":[],"totalCount":0}}`)
	m.on("DELETE /api/v1/group", `{"errors":null,"result":2}`)
	if _, err := m.runErr(t, "group", "delete", "1"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("delete ran without confirmation: %v", err)
	}
	mustContain(t, m.run(t, "group", "delete", "1,2", "--yes"), "Deleted 2 user group(s)")
	valuesEq(t, m.last(t, "GET", "/api/v1/group").Query, url.Values{"checkDeleteViability": {"true"}, "ids": {"1", "2"}})
	valuesEq(t, m.last(t, "DELETE", "/api/v1/group").Query, url.Values{"id": {"1", "2"}})

	// blockers stop the delete, even with --yes
	m = newMock(t)
	m.on("GET /api/v1/group", `{"errors":null,"result":{"searchResults":[{"id":2,"name":"Sales","reason":"ASSIGNED_TO_APP"}],"totalCount":1}}`)
	m.on("DELETE /api/v1/group", `{"errors":null,"result":1}`)
	if _, err := m.runErr(t, "group", "delete", "2", "--yes"); err == nil || !strings.Contains(err.Error(), "ASSIGNED_TO_APP") {
		t.Fatalf("delete with blockers: err = %v", err)
	}
	if m.count("DELETE", "") > 0 {
		t.Fatal("DELETE sent despite blockers")
	}
}

func TestDeviceGroupList(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/rule_group", `{"errors":null,"result":{"totalCount":1,"searchResults":[{"id":38009,"dmPartitionId":23000,"name":"All Devices",
"description":"Targets any type of device.","type":"DEVICE","memberCount":510,"systemName":"ALL_DEVICES","membershipStatic":false}],"offset":0,"limit":50}}`)
	out := m.run(t, "device-group", "list", "--limit", "0")
	mustContain(t, out, "MEMBERCOUNT", "38009", "All Devices", "510")
	valuesEq(t, m.last(t, "GET", "/api/v1/rule_group").Query, url.Values{"dmPartitionId": {"23000"}, "type": {"DEVICE"},
		"assignedPoliciesAndConfigs": {"false"}, "rows": {"500"}, "start": {"0"}})
}

func TestDeviceGroupView(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/rule_group/38010", deviceGroupJSON)
	out := m.run(t, "device-group", "view", "38010")
	mustContain(t, out, "name:", "iPads", "PRETTYMODEL EQ 'iPad'", "memberCount:", "12", "Default Space")
	valuesEq(t, m.last(t, "GET", "/api/v1/rule_group/38010").Query, url.Values{"dmPartitionId": {"23000"}, "type": {"DEVICE"}})
}

func TestDeviceGroupFind(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/tenant/partition/device", `{"errors":null,"result":{"totalCount":2,"searchResults":[{"id":23000,"name":"Default Space"},{"id":24000,"name":"Kiosks"}]}}`)
	m.on("GET /api/v1/rule_group/partitions", `{"errors":null,"result":{"searchResults":[{"id":40165,"dmPartitionId":24000,"name":"All Devices",
"memberCount":153,"membershipStatic":false,"dmPartitionName":"Kiosks"}],"totalCount":1,"offset":0,"limit":50}}`)
	out := m.run(t, "device-group", "find", "All Devices")
	mustContain(t, out, "40165", "Kiosks", "153")
	valuesEq(t, m.last(t, "GET", "/api/v1/rule_group/partitions").Query, url.Values{"q": {"All Devices"}, "type": {"DEVICE"},
		"dmPartitionIds": {"23000,24000"}, "assignedPoliciesAndConfigs": {"false"}, "rows": {"500"}, "start": {"0"}})
}

func TestDeviceGroupCreate(t *testing.T) {
	m := newMock(t)
	m.on("POST /api/v1/rule_group", `{"errors":null,"result":{"id":39340,"name":"TestForDocs","type":"DEVICE","definition":"PRETTYMODEL EQ 'iPad' ","memberCount":0,"dmPartitionId":23000}}`)
	out := m.run(t, "device-group", "create", "TestForDocs", "--description", "Test for docs", "--rule", "PRETTYMODEL EQ 'iPad'")
	mustContain(t, out, `Created device group "TestForDocs" (id 39340)`)
	valuesEq(t, m.last(t, "POST", "/api/v1/rule_group").Query, url.Values{"name": {"TestForDocs"}, "type": {"DEVICE"},
		"dmPartitionId": {"23000"}, "description": {"Test for docs"}, "definition": {"PRETTYMODEL EQ 'iPad'"}, "membershipStatic": {"false"}})

	m.run(t, "device-group", "create", "Kiosks")
	valuesEq(t, m.last(t, "POST", "/api/v1/rule_group").Query, url.Values{"name": {"Kiosks"}, "type": {"DEVICE"},
		"dmPartitionId": {"23000"}, "membershipStatic": {"true"}})
}

func TestDeviceGroupDevices(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/rule_group/38010", deviceGroupJSON)
	m.on("GET /api/v1/rule_group/47808", deviceGroupStaticJSON)
	m.on("GET /api/v1/device", deviceGroupDevicesJSON)
	out := m.run(t, "device-group", "devices", "38010", "--fq", "platformType='IOS'")
	mustContain(t, out, "197483", "jdoe@example.com", "2015-01-30")
	valuesEq(t, m.last(t, "GET", "/api/v1/device").Query, url.Values{"dmPartitionId": {"23000"},
		"fq": {"(PRETTYMODEL EQ 'iPad') AND (platformType='IOS')"}, "rows": {"50"}, "start": {"0"}})

	if _, err := m.runErr(t, "device-group", "devices", "47808"); err == nil || !strings.Contains(err.Error(), "staticMembers") {
		t.Fatalf("expected static group error, got %v", err)
	}
}

func TestDeviceGroupMembers(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/rule_group/47808", deviceGroupStaticJSON)
	m.on("GET /api/v1/rule_group/47809", `{"errors":null,"result":{"id":47809,"memberCount":1,"membershipStatic":true,"staticMembers":[{"id":201}]}}`)
	m.on("GET /api/v1/rule_group/38010", deviceGroupJSON)
	m.on("POST /api/v1/rule_group/{id}/devices", `{"errors":null,"result":0}`)
	m.on("DELETE /api/v1/rule_group/{id}/devices", `{"errors":null,"result":0}`)

	out := m.run(t, "device-group", "members", "add", "47808", "300", "200")
	mustContain(t, out, "Added 2 device(s) to device group 47808")
	if got := m.last(t, "POST", "/api/v1/rule_group/47808/devices").Body; got != `{"deviceIds":[200,300]}` {
		t.Errorf("add body = %s", got)
	}
	m.run(t, "device-group", "members", "add", "47809", "300") // object-shaped static members
	if got := m.last(t, "POST", "/api/v1/rule_group/47809/devices").Body; got != `{"deviceIds":[201,300]}` {
		t.Errorf("add body = %s", got)
	}

	if _, err := m.runErr(t, "device-group", "members", "remove", "47808", "200"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("remove ran without confirmation: %v", err)
	}
	m.run(t, "device-group", "members", "remove", "47808", "200", "--yes")
	if got := m.last(t, "DELETE", "/api/v1/rule_group/47808/devices").Body; got != `{"deviceIds":[200]}` {
		t.Errorf("remove body = %s", got)
	}

	posts := m.count("POST", "")
	if _, err := m.runErr(t, "device-group", "members", "set", "38010"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("set ran without confirmation: %v", err)
	}
	if m.count("POST", "") != posts {
		t.Fatal("set sent a POST without confirmation")
	}
	m.run(t, "device-group", "members", "set", "38010", "--yes")
	if got := m.last(t, "POST", "/api/v1/rule_group/38010/devices").Body; got != `{"deviceIds":[]}` {
		t.Errorf("set body = %s", got)
	}
}

// add must refuse (even with --yes) whenever the current static members
// cannot be read, since its POST replaces the whole list.
func TestDeviceGroupMembersAddRefusesUnreadableMembers(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/rule_group/1", `{"errors":null,"result":{"id":1,"memberCount":3,"membershipStatic":true,"staticMembers":null}}`)
	m.on("GET /api/v1/rule_group/2", `{"errors":null,"result":{"id":2,"memberCount":12,"membershipStatic":false,"staticMembers":null}}`)
	m.on("GET /api/v1/rule_group/3", `{"errors":null,"result":{"id":3,"memberCount":1,"membershipStatic":true,"staticMembers":[{"name":"x"}]}}`)
	m.on("POST /api/v1/rule_group/{id}/devices", `{"errors":null,"result":0}`)
	for _, id := range []string{"1", "2", "3"} {
		if _, err := m.runErr(t, "device-group", "members", "add", id, "300", "--yes"); err == nil || !strings.Contains(err.Error(), "members set") {
			t.Errorf("group %s: expected refusal, got %v", id, err)
		}
	}
	if m.count("POST", "") > 0 {
		t.Fatal("add sent a POST despite unreadable members")
	}
}
