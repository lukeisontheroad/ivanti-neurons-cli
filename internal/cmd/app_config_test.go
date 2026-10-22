package cmd

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// appManagedMock serves version 121646 of app 16005 with an INSTALL and an
// ANDROID_FOR_WORK configuration (276183); the app id answers 403 on the
// version endpoints, as the API does.
func appManagedMock(t *testing.T) *mock {
	m := newMock(t)
	m.mux.HandleFunc("GET /api/v1/app/version/{id}/config/types", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "121646" {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"errors":{"globalErrors":[{"code":"HTTP-403","message":"You are not authorized to access this resource."}]},"result":null}`)
			return
		}
		fmt.Fprint(w, `{"errors":null,"result":{"INSTALL":{},"ANDROID_FOR_WORK":{}}}`)
	})
	m.mux.HandleFunc("GET /api/v1/app/version/121646/config", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") == "INSTALL" {
			fmt.Fprint(w, `{"errors":null,"result":{"totalCount":1,"searchResults":[{"id":276182,"type":"INSTALL","name":"Install","settings":{"type":"INSTALL","required":false}}]}}`)
			return
		}
		fmt.Fprint(w, `{"errors":null,"result":{"totalCount":1,"searchResults":[{"id":276183,"type":"ANDROID_FOR_WORK","name":"default","userCounts":{"total":1},
"settings":{"type":"ANDROID_FOR_WORK","configuration":{"uuid":"c0ffee","restrictions":{
 "ProxySettings":{"type":"STRING","enabled":true,"title":"Proxy settings","value":"http://proxy/test.pac"},
 "HomepageLocation":{"type":"STRING","enabled":false,"title":"Configure the home page URL","value":null},
 "DisableAuthNegotiateCnameLookup":{"type":"BOOL","enabled":false,"title":"Disable CNAME lookup","value":null},
 "NetworkPredictionOptions":{"type":"CHOICE","enabled":false,"value":null,"entry":["Always","Wi-Fi only","Never"],"entryValue":["0","1","2"]},
 "ExplicitlyAllowedNetworkPorts":{"type":"MULTISELECT","enabled":false,"value":null,"entry":[],"entryValue":[]}}}}}]}}`)
	})
	// the single GET returns the configuration with its assignments
	m.mux.HandleFunc("GET /api/v1/app/version/121646/config/276183", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"errors":null,"result":{"id":276183,"applicationVersionId":121646,"type":"ANDROID_FOR_WORK","name":"default","priority":1,
"groupIds":[50918],"accountIds":[],"deviceIds":[],"ruleGroupIds":[],"groups":[{"id":50918,"name":"All Users"}],
"settings":{"type":"ANDROID_FOR_WORK","configuration":{"uuid":"c0ffee","restrictions":{
 "ProxySettings":{"type":"STRING","enabled":true,"title":"Proxy settings","value":"http://proxy/test.pac"},
 "HomepageLocation":{"type":"STRING","enabled":false,"title":"Configure the home page URL","value":null},
 "DisableAuthNegotiateCnameLookup":{"type":"BOOL","enabled":false,"title":"Disable CNAME lookup","value":null},
 "NetworkPredictionOptions":{"type":"CHOICE","enabled":false,"value":null,"entry":["Always","Wi-Fi only","Never"],"entryValue":["0","1","2"]},
 "ExplicitlyAllowedNetworkPorts":{"type":"MULTISELECT","enabled":false,"value":null,"entry":[],"entryValue":[]}}}}}}`)
	})
	m.on("GET /api/v1/app/16005", `{"errors":null,"result":{"id":16005,"name":"Google Chrome","latestAppVersionString":"155.0","versions":[{"applicationVersionId":121646,"displayVersion":"155.0"}]}}`)
	m.on("PUT /api/v1/app/version/121646/config/276183", `{"errors":null,"result":276183}`)
	return m
}

func TestAppConfigListAllTypes(t *testing.T) {
	m := appManagedMock(t)
	out := m.run(t, "app", "config", "list", "16005") // app id: resolves to 121646
	mustContain(t, out, "Using version 121646 (155.0) of app 16005", "276182", "INSTALL", "276183", "ANDROID_FOR_WORK")
	out = m.run(t, "app", "config", "list", "121646", "--type", "android_for_work")
	if strings.Contains(out, "276182") || !strings.Contains(out, "276183") {
		t.Errorf("--type did not narrow:\n%s", out)
	}
}

func TestAppConfigGetShowsPushedKeys(t *testing.T) {
	m := appManagedMock(t)
	out := m.run(t, "app", "config", "get", "121646", "276183")
	mustContain(t, out, "ProxySettings", "http://proxy/test.pac", "Proxy settings")
	if strings.Contains(out, "HomepageLocation") {
		t.Errorf("disabled keys must not show without --all:\n%s", out)
	}
	mustContain(t, m.run(t, "app", "config", "get", "121646", "276183", "--all"), "ENABLED", "HomepageLocation", "false")
}

func TestAppConfigSet(t *testing.T) {
	m := appManagedMock(t)
	out := m.run(t, "app", "config", "set", "121646", "276183",
		"DisableAuthNegotiateCnameLookup=true", "NetworkPredictionOptions=Never", "ProxySettings=")
	mustContain(t, out, "+ DisableAuthNegotiateCnameLookup = true", "+ NetworkPredictionOptions = 2 (Never)", "- ProxySettings")
	body := m.last(t, "PUT", "/api/v1/app/version/121646/config/276183").Body
	mustContain(t, body, `"DisableAuthNegotiateCnameLookup":{"enabled":true`, `"value":true`,
		`"NetworkPredictionOptions":{"enabled":true`, `"value":"2"`,
		`"ProxySettings":{"enabled":false,"title":"Proxy settings","type":"STRING","value":"http://proxy/test.pac"}`)
	// the PUT replaces the assignments: they must go back, the expansions not
	mustContain(t, body, `"groupIds":[50918]`, `"accountIds":[]`, `"ruleGroupIds":[]`, `"uuid":null`)
	if strings.Contains(body, `"groups"`) || strings.Contains(body, "userCounts") {
		t.Errorf("read-only fields sent back: %s", body)
	}

	// unchanged values send nothing; bad keys, values, and types are refused
	before := m.count("PUT", "")
	mustContain(t, m.run(t, "app", "config", "set", "121646", "276183", "ProxySettings=http://proxy/test.pac"), "No changes; nothing sent.")
	for args, want := range map[string]string{
		"Proxy=x":                               `did you mean ProxySettings?`,
		"DisableAuthNegotiateCnameLookup=maybe": "BOOL: want true or false",
		"NetworkPredictionOptions=5":            "want one of 0, 1, 2",
		"ExplicitlyAllowedNetworkPorts=1":       "MULTISELECT: not supported",
	} {
		if _, err := m.runErr(t, "app", "config", "set", "121646", "276183", args); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", args, err, want)
		}
	}
	if _, err := m.runErr(t, "app", "config", "set", "121646", "276182", "x=1"); err == nil || !strings.Contains(err.Error(), "app config update") {
		t.Errorf("INSTALL config: err = %v", err)
	}
	if n := m.count("PUT", ""); n != before {
		t.Errorf("%d PUT(s) sent for refused or empty changes", n-before)
	}
}

func TestAppConfigWrongID(t *testing.T) {
	m := appManagedMock(t)
	_, err := m.runErr(t, "app", "config", "list", "999")
	if err == nil || !strings.Contains(err.Error(), "neither an app version id nor an app id") || !strings.Contains(err.Error(), "app view") {
		t.Fatalf("err = %v", err)
	}
}

func TestAppConfigUpdateKeepsAssignments(t *testing.T) {
	m := appManagedMock(t)
	m.run(t, "app", "config", "update", "121646", "276183", "-f", "name=renamed", "-f", "priority=2")
	body := m.last(t, "PUT", "/api/v1/app/version/121646/config/276183").Body
	mustContain(t, body, `"groupIds":[50918]`, `"type":"ANDROID_FOR_WORK"`, `"name":"renamed"`)
	m.run(t, "app", "config", "update", "121646", "276183", "--data", `{"name":"x","groupIds":[]}`)
	mustContain(t, m.last(t, "PUT", "/api/v1/app/version/121646/config/276183").Body, `"groupIds":[]`)
}
