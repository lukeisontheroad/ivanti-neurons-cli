package cmd

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
)

const (
	actionCheckOK       = `{"errors":null,"result":{"totalCount":0,"queryTime":0,"searchResults":[],"offset":0,"limit":0}}`
	actionCheckRejected = `{"errors":null,"result":{"searchResults":[{"id":22003,"guid":"badd9735-4572-4ee9-a13c-062367c62792","deviceName":"Kiosk iPad","platformType":"IOS","mdmChannelId":"fb84afb7-c458-43e6-9c6e-45cf011542b1","actionExceptionCodes":["PLATFORM_NOT_SUPPORTED"]}],"totalCount":1,"offset":0,"limit":1}}`
	actionGUID          = "57d9903c-aadf-4b40-aef3-e1f24302f180"
)

// TestActionCommands runs every action once and asserts the exact query the
// action PUT sends and the report line.
func TestActionCommands(t *testing.T) {
	tests := []struct {
		args   []string
		path   string
		result string
		want   url.Values
		out    string
	}{
		{[]string{"os-update", "22002", "--version", "15.5"}, "scheduleOSUpdate", "1",
			url.Values{"ids": {"22002"}, "version": {"15.5"}}, "Scheduled OS update on 1 device(s)"},
		{[]string{"restart", "10542407,10542400"}, "restart", "1",
			url.Values{"ids": {"10542407", "10542400"}}, "Restarted 2 device(s)"},
		{[]string{"checkin", "22062", actionGUID}, "forceCheckin", "2",
			url.Values{"ids": {"22062"}, "guids": {actionGUID}}, "Forced check-in on 2 device(s)"},
		{[]string{"lock", "22000", "--message", "Return to IT", "--phone", "555-555-5555", "--pin", "123456"}, "lock", "1",
			url.Values{"ids": {"22000"}, "message": {"Return to IT"}, "phoneNumber": {"555-555-5555"}, "osxLockPin": {"123456"}}, "Locked 1 device(s)"},
		{[]string{"unlock", "22000", "--pin", "123ab400"}, "unlock", "1",
			url.Values{"ids": {"22000"}, "deviceUnlockPin": {"123ab400"}}, "Unlocked 1 device(s)"},
		{[]string{"wipe", "32362", "--yes", "--message", "Decommissioning", "--pin", "123456", "--preserve-data-plan",
			"--disallow-proximity-setup", "--return-to-service", "--wifi-policy", "144616", "-f", "preserveApps=true"}, "wipe", "1",
			url.Values{"ids": {"32362"}, "message": {"Decommissioning"}, "osxWipePin": {"123456"}, "preserveDataPlan": {"true"},
				"iosDisallowProximitySetup": {"true"}, "returnToServiceEnabled": {"true"}, "wifiPolicyId": {"144616"}, "preserveApps": {"true"}},
			"Wiped 1 device(s)"},
		{[]string{"wipe-cancel", "22000"}, "wipeCancel", "true", url.Values{"ids": {"22000"}}, "Cancelled wipe on 1 device(s)"},
		{[]string{"retire", "1007966", "--yes"}, "retire", "1", url.Values{"ids": {"1007966"}}, "Retired 1 device(s)"},
		{[]string{"retire-cancel", "22000"}, "retireCancel", "true", url.Values{"ids": {"22000"}}, "Cancelled retire on 1 device(s)"},
		{[]string{"logout", "22002", "--yes"}, "logoutUser", "1", url.Values{"ids": {"22002"}}, "Logged out the user on 1 device(s)"},
		{[]string{"message", "370872,383279", "--push", "Docs test of bulk messages"}, "message", "true",
			url.Values{"deviceIds": {"370872", "383279"}, "dmPartitionId": {"23000"}, "sendPushNotification": {"true"},
				"pushNotificationMessage": {"Docs test of bulk messages"}, "sendEmail": {"false"}, "emailSubject": {""}, "emailBody": {""}},
			"Sent message to 2 device(s)"},
		{[]string{"message", "370872", "--email", "Please update", "--subject", "Compliance", "--badge", "3"}, "message", "true",
			url.Values{"deviceIds": {"370872"}, "dmPartitionId": {"23000"}, "sendPushNotification": {"false"},
				"sendEmail": {"true"}, "emailSubject": {"Compliance"}, "emailBody": {"Please update"}, "badge": {"3"}},
			"Sent message to 1 device(s)"},
		{[]string{"enter-kiosk", "22000"}, "enterKioskMode", "1", url.Values{"ids": {"22000"}}, "Entered kiosk mode on 1 device(s)"},
		{[]string{"exit-kiosk", "22000"}, "exitKioskMode", "1", url.Values{"ids": {"22000"}}, "Exited kiosk mode on 1 device(s)"},
		{[]string{"enable-lost-mode", "121073", "--message", "This device has been lost.", "--phone", "555-555-5555", "--footnote", "Lost Phone"}, "enableLostMode", "1",
			url.Values{"ids": {"121073"}, "message": {"This device has been lost."}, "phoneNumber": {"555-555-5555"}, "footnote": {"Lost Phone"}, "changeFootnote": {"true"}},
			"Enabled lost mode on 1 device(s)"},
		{[]string{"disable-lost-mode", "136732"}, "disableLostMode", "1", url.Values{"ids": {"136732"}}, "Disabled lost mode on 1 device(s)"},
		{[]string{"relinquish", "909", "--yes"}, "relinquishOwnership", "1", url.Values{"ids": {"909"}}, "Relinquished 1 device(s)"},
		{[]string{"relinquish-cancel", "909"}, "relinquishOwnershipCancel", "1", url.Values{"ids": {"909"}}, "Cancelled relinquish on 1 device(s)"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			m := newMock(t)
			path := "/api/v1/device/" + tt.path
			m.on("GET "+path, actionCheckOK)
			m.on("PUT "+path, `{"errors":null,"result":`+tt.result+`}`)
			out := m.run(t, append([]string{"device"}, tt.args...)...)
			mustContain(t, out, tt.out)
			req := m.last(t, "PUT", path)
			if !reflect.DeepEqual(req.Form, tt.want) || len(req.Query) > 0 {
				t.Errorf("form = %v (query %v), want form %v", req.Form, req.Query, tt.want)
			}
		})
	}
}

func TestActionWipeChecksFirst(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/device/wipe", actionCheckRejected)
	m.on("PUT /api/v1/device/wipe", `{"errors":null,"result":2}`)
	out, err := m.runErr(t, "device", "wipe", "22002", "22003", "--yes")
	if err == nil || !strings.Contains(err.Error(), "refusing to wipe: 1 of 2 device(s) not eligible") {
		t.Fatalf("expected refusal, got %v", err)
	}
	mustContain(t, out, "22003", "PLATFORM_NOT_SUPPORTED")
	if got, want := m.last(t, "GET", "/api/v1/device/wipe").Query, (url.Values{"checkViability": {"true"}, "ids": {"22002", "22003"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("check query = %v, want %v", got, want)
	}
	if n := m.count("PUT", "/api/v1/device/wipe"); n != 0 {
		t.Fatalf("wipe sent despite ineligible device (%d PUTs)", n)
	}
}

func TestActionWipeNeedsConfirmation(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/device/wipe", actionCheckOK)
	_, err := m.runErr(t, "device", "wipe", "22002", "22003")
	if err == nil || !strings.Contains(err.Error(), "refusing to wipe 2 device(s) without confirmation") {
		t.Fatalf("expected confirmation refusal, got %v", err)
	}
	if n := m.count("PUT", "/api/v1/device/wipe"); n != 0 {
		t.Fatalf("wipe sent without confirmation (%d PUTs)", n)
	}
}

func TestActionCheckOnly(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/device/lock", actionCheckRejected)
	out, err := m.runErr(t, "device", "lock", "22002", "22003", "--check")
	if err == nil || !strings.Contains(err.Error(), "1 of 2 device(s) not eligible for lock") {
		t.Fatalf("expected ineligible error, got %v", err)
	}
	mustContain(t, out, "ACTIONEXCEPTIONCODES", "22003", "Kiosk iPad", "PLATFORM_NOT_SUPPORTED")
	if n := m.count("PUT", "/api/v1/device/lock"); n != 0 {
		t.Fatalf("--check sent the action (%d PUTs)", n)
	}

	m = newMock(t)
	m.on("GET /api/v1/device/scheduleOSUpdate", actionCheckOK)
	mustContain(t, m.run(t, "device", "os-update", "22000", "--check"), "All 1 device(s) eligible for os-update")
	mustContain(t, m.run(t, "device", "os-update", "22000", "--check", "-o", "json"), `"totalCount": 0`)
}

func TestActionRelinquishCheckCount(t *testing.T) {
	m := newMock(t)
	m.on("PUT /api/v1/device/relinquishOwnership", `{"errors":null,"result":0}`)
	out, err := m.runErr(t, "device", "relinquish", "909", "--yes")
	if err == nil || !strings.Contains(err.Error(), "1 of 1 device(s) not eligible") {
		t.Fatalf("expected refusal, got %v", err)
	}
	if strings.Contains(out, "DEVICENAME") {
		t.Errorf("count-shaped check printed an empty table:\n%s", out)
	}
	if got := m.last(t, "PUT", "/api/v1/device/relinquishOwnership").Query; got.Get("checkViability") != "true" {
		t.Fatalf("check not sent: %v", got)
	}
	if n := m.count("PUT", "/api/v1/device/relinquishOwnership"); n != 1 {
		t.Fatalf("want only the check PUT, got %d", n)
	}
}

func TestActionInputErrors(t *testing.T) {
	m := newMock(t)
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"wipe", actionGUID, "--yes"}, "wipe takes numeric device ids, not GUIDs"},
		{[]string{"lock", "22000;rm"}, `invalid id "22000;rm"`},
		{[]string{"lock", ","}, "no device ids given"},
		{[]string{"lock", actionGUID, "--check"}, "viability check takes numeric device ids"},
		{[]string{"os-update", "22000"}, "[version check]"},
		{[]string{"message", "22000"}, "[push email]"},
		{[]string{"enable-lost-mode", "22000"}, "[message]"},
		{[]string{"kiosk-users", "abc"}, `invalid id "abc"`},
	} {
		if _, err := m.runErr(t, append([]string{"device"}, tt.args...)...); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%v: want error containing %q, got %v", tt.args, tt.want, err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.reqs) != 0 {
		t.Fatalf("invalid input reached the API: %v", m.reqs)
	}
}

func TestActionResultShapes(t *testing.T) {
	m := newMock(t)
	m.on("PUT /api/v1/device/forceCheckin", `{"errors":null,"result":1}`)
	mustContain(t, m.run(t, "device", "checkin", "22062", "22196"), "Warning: the API acted on 1 of 2 device(s)", "Forced check-in on 1 device(s)")
	if out := m.run(t, "device", "checkin", "22062", "-o", "json"); strings.TrimSpace(out) != "1" {
		t.Errorf("-o json should print the raw result, got %q", out)
	}

	m.on("PUT /api/v1/device/wipeCancel", `{"errors":null,"result":false}`)
	if _, err := m.runErr(t, "device", "wipe-cancel", "22000"); err == nil || !strings.Contains(err.Error(), "wipe-cancel failed") {
		t.Errorf("want failure on result false, got %v", err)
	}
	m.on("PUT /api/v1/device/scheduleOSUpdate", `{"errors":null,"result":0}`)
	if _, err := m.runErr(t, "device", "os-update", "22000", "--version", "15.5"); err == nil || !strings.Contains(err.Error(), "os-update failed") {
		t.Errorf("want failure on result 0, got %v", err)
	}
}

func TestActionKioskUsers(t *testing.T) {
	m := newMock(t)
	m.on("GET /api/v1/device/22001/kioskUsers", `{"errors":null,"result":{"searchResults":[
		{"displayName":"worker2","uid":"worker2@example.com","loggedInAt":1760344499977,"loggedOutAt":1760344521919,"lastSessionTime":21},
		{"displayName":"worker3","uid":"worker3@example.com","loggedInAt":1760344557854,"loggedOutAt":1760344653617,"lastSessionTime":95}],
		"totalCount":2,"offset":0,"limit":50}}`)
	out := m.run(t, "device", "kiosk-users", "22001", "--sort", "loggedInAt:desc", "-f", "loginRangeStartTime=1760344499977")
	mustContain(t, out, "DISPLAYNAME", "worker2", "worker3@example.com", "2025-10-13 08:34", "95")
	q := m.last(t, "GET", "/api/v1/device/22001/kioskUsers").Query
	for k, v := range map[string]string{"rows": "50", "start": "0", "sortFields[0].name": "loggedInAt", "sortFields[0].order": "DESC", "loginRangeStartTime": "1760344499977"} {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q (query %v)", k, q.Get(k), v, q)
		}
	}
}
