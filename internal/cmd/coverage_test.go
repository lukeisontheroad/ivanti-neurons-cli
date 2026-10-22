package cmd

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// coverageGUID stands in for device / account / task GUIDs.
const coverageGUID = "57d9903c-aadf-4b40-aef3-e1f24302f180"

// coverage maps every call documented in the Ivanti Neurons for MDM API
// guide (and its OpenAPI export) to the command that issues it. Each entry
// runs against a permissive mock and must send exactly that method + path
// (plus the listed query values). "FILE" in args becomes a temp file.
// Not listed: POST /oauth2/token (core client, see internal/api tests);
// PUT .../customattributes on devices and app versions (documented as the
// same call as POST, which the CLI sends); GET /api/v1/app/devicefilter
// (named in prose only, no documented call).
var coverage = []struct {
	method, path string
	query        map[string]string
	args         []string
	mocks        map[string]string // extra fixed responses (ServeMux pattern -> body)
}{
	// tenant, spaces, configurations, Sentry, connector
	{"GET", "/api/v1/metadata/tenant", nil, []string{"tenant", "metadata"}, nil},
	{"GET", "/api/v1/tenant/mixpanel-status", nil, []string{"tenant", "status"}, nil},
	{"GET", "/api/v1/tenant/license/usage", nil, []string{"tenant", "license"}, nil},
	{"GET", "/api/v1/tenant/partition/device", nil, []string{"space", "list"}, nil},
	{"GET", "/api/v1/policy", map[string]string{"tag": "DEVICE_CONFIGURATION"}, []string{"config", "list"}, nil},
	{"PUT", "/api/v1/policy/1/rule_groups", map[string]string{"ruleGroupIds": "2"}, []string{"config", "assign", "1", "2"}, nil},
	{"GET", "/api/v1/sentry/config", nil, []string{"config", "sentry"}, nil},
	{"GET", "/api/v1/ec", nil, []string{"connector", "list"}, nil},

	// certificates
	{"GET", "/c/ca/7/AWS CA.crl", nil, []string{"cert", "crl", "7", "AWS CA"}, nil},
	{"GET", "/c/ca/7/23000/AWS CA.crl", nil, []string{"cert", "crl", "7", "AWS CA", "--space", "23000"}, nil},
	{"GET", "/api/v1/ca/getCertStatus/AWS CA", map[string]string{"format": "csv"}, []string{"cert", "revocation-pending", "AWS CA"}, nil},
	{"GET", "/api/v1/ca/getCertStatusOnDate", map[string]string{"manuallyRevokeDate": "01-02-2026"}, []string{"cert", "revoked-on", "01-02-2026"}, nil},
	{"POST", "/api/v1/user_provided", nil, []string{"cert", "upload", "FILE", "--policy", "5"}, nil},
	{"DELETE", "/api/v1/user_provided/clearPrivateKey/csv", nil, []string{"cert", "clear-keys", "--csv", "FILE"}, nil},
	{"DELETE", "/api/v1/user_provided/clearPrivateKey", nil, []string{"cert", "clear-keys", "--account", "1", "--policy", "2"}, nil},

	// custom attribute definitions, OIDC clients, DEP
	{"GET", "/api/v1/customattributes", nil, []string{"attribute", "list"}, nil},
	{"POST", "/api/v1/customattributes", nil, []string{"attribute", "create", "cityId", "--type", "DEVICE"}, nil},
	{"PUT", "/api/v1/customattributes", nil, []string{"attribute", "update", "1", "--name", "cityId", "--type", "DEVICE"}, nil},
	{"DELETE", "/api/v1/customattributes", nil, []string{"attribute", "delete", "1"}, nil},
	{"POST", "/api/v1/oidc/client", nil, []string{"oidc", "create", coverageGUID}, nil},
	{"POST", "/api/v1/oidc/secret-reset", nil, []string{"oidc", "reset-secret", coverageGUID}, nil},
	{"GET", "/api/v1/depServer/3/profiles", nil, []string{"dep", "profiles", "3"}, nil},
	{"GET", "/api/v2/depServer/3/profiles", nil, []string{"dep", "profiles", "3", "--v2"}, nil},
	{"POST", "/c/i/dep/custom.mobileconfig", nil, []string{"dep", "mdm-profile", "--device-info", "abc"}, nil},

	// devices
	{"GET", "/api/v1/device", map[string]string{"dmPartitionId": "23000"}, []string{"device", "list"}, nil},
	{"GET", "/api/v1/device", map[string]string{"type": "APP_INVENTORY"}, []string{"device", "with-app", "com.example.app", "--platform", "IOS"}, nil},
	{"GET", "/api/v1/device/1", nil, []string{"device", "view", "1"}, nil},
	{"GET", "/api/v1/device/" + coverageGUID, nil, []string{"device", "view", coverageGUID}, nil},
	{"DELETE", "/api/v1/device", map[string]string{"deviceIds": "1"}, []string{"device", "delete", "1"}, nil},
	{"PUT", "/api/v1/device/updateAccount", map[string]string{"accountId": "2"}, []string{"device", "assign", "1", "--to", "2"},
		map[string]string{"PUT /api/v1/device/updateAccount": `{"errors":null,"result":{"totalCount":0,"searchResults":[]}}`}},
	{"GET", "/api/v1/device/1/customattributes", nil, []string{"device", "attrs", "get", "1"}, nil},
	{"POST", "/api/v1/device/1/customattributes", nil, []string{"device", "attrs", "replace", "1", "a=b"}, nil},
	{"PATCH", "/api/v1/device/1/customattributes", nil, []string{"device", "attrs", "set", "1", "a=b"}, nil},
	{"DELETE", "/api/v1/device/1/customattributes", nil, []string{"device", "attrs", "remove", "1", "a=b"}, nil},
	{"DELETE", "/api/v1/device/customattributes", nil, []string{"device", "attrs", "remove-keys", "1", "--key", "a"}, nil},
	{"GET", "/api/v1/device/1/configs", nil, []string{"device", "config", "list", "1"}, nil},
	{"PUT", "/api/v1/device/1/uninstallconfigs", map[string]string{"policyIds": "2"}, []string{"device", "config", "exclude", "1", "2"}, nil},
	{"DELETE", "/api/v1/device/1/uninstallconfigs", map[string]string{"policyIds": "2"}, []string{"device", "config", "include", "1", "2"}, nil},
	{"PUT", "/api/v1/device/1/reinstallconfigs", map[string]string{"policyIds": "2"}, []string{"device", "config", "reinstall", "1", "2"}, nil},
	{"PUT", "/api/v1/device/clearConfigError", nil, []string{"device", "config", "clear-error", "1", coverageGUID}, nil},
	{"GET", "/api/v1/device/1/policies", nil, []string{"device", "compliance", "status", "1"}, nil},
	{"GET", "/api/v2/policy/filter", nil, []string{"device", "compliance", "policies"}, nil},
	{"POST", "/api/v1/device/compliance/sync", map[string]string{"vendor": "Intune"}, []string{"device", "compliance", "sync", "1", "--vendor", "Intune"}, nil},
	{"GET", "/api/v1/device/1/certs/search", nil, []string{"device", "certs", "1"},
		map[string]string{"GET /api/v1/device/1": `{"errors":null,"result":{"id":1,"ownerId":5}}`}},
	{"POST", "/api/v1/predicate", nil, []string{"device", "predicate", "create", "--name", "n", "--query", "q"}, nil},
	{"PUT", "/api/v1/predicate", nil, []string{"device", "predicate", "update", "1", "--name", "n", "--query", "q"}, nil},
	{"PUT", "/api/v1/ldapserver/1/sync", nil, []string{"device", "ldap-sync", "1"}, nil},
	{"POST", "/api/v1/teamviewer/1", nil, []string{"device", "teamviewer", "1"}, nil},

	// device actions (viability checks via --check)
	{"GET", "/api/v1/device/scheduleOSUpdate", map[string]string{"checkViability": "true"}, []string{"device", "os-update", "1", "--version", "17.1", "--check"}, nil},
	{"PUT", "/api/v1/device/scheduleOSUpdate", nil, []string{"device", "os-update", "1", "--version", "17.1"}, nil},
	{"PUT", "/api/v1/device/restart", nil, []string{"device", "restart", "1"}, nil},
	{"GET", "/api/v1/device/forceCheckin", map[string]string{"checkViability": "true"}, []string{"device", "checkin", "1", "--check"}, nil},
	{"PUT", "/api/v1/device/forceCheckin", map[string]string{"guids": coverageGUID}, []string{"device", "checkin", coverageGUID}, nil},
	{"GET", "/api/v1/device/wipe", map[string]string{"checkViability": "true"}, []string{"device", "wipe", "1", "--check"}, nil},
	{"PUT", "/api/v1/device/wipe", map[string]string{"ids": "1"}, []string{"device", "wipe", "1"}, nil},
	{"PUT", "/api/v1/device/wipeCancel", nil, []string{"device", "wipe-cancel", "1"}, nil},
	{"GET", "/api/v1/device/lock", map[string]string{"checkViability": "true"}, []string{"device", "lock", "1", "--check"}, nil},
	{"PUT", "/api/v1/device/lock", map[string]string{"ids": "1"}, []string{"device", "lock", "1"}, nil},
	{"PUT", "/api/v1/device/unlock", nil, []string{"device", "unlock", "1"}, nil},
	{"GET", "/api/v1/device/retire", map[string]string{"checkViability": "true"}, []string{"device", "retire", "1", "--check"}, nil},
	{"PUT", "/api/v1/device/retire", map[string]string{"ids": "1"}, []string{"device", "retire", "1"}, nil},
	{"PUT", "/api/v1/device/retireCancel", nil, []string{"device", "retire-cancel", "1"}, nil},
	{"GET", "/api/v1/device/logoutUser", map[string]string{"checkViability": "true"}, []string{"device", "logout", "1", "--check"}, nil},
	{"PUT", "/api/v1/device/logoutUser", map[string]string{"ids": "1"}, []string{"device", "logout", "1"}, nil},
	{"PUT", "/api/v1/device/message", nil, []string{"device", "message", "1", "--push", "hi"}, nil},
	{"GET", "/api/v1/device/enterKioskMode", map[string]string{"checkViability": "true"}, []string{"device", "enter-kiosk", "1", "--check"}, nil},
	{"PUT", "/api/v1/device/enterKioskMode", nil, []string{"device", "enter-kiosk", "1"}, nil},
	{"PUT", "/api/v1/device/exitKioskMode", nil, []string{"device", "exit-kiosk", "1"}, nil},
	{"GET", "/api/v1/device/1/kioskUsers", nil, []string{"device", "kiosk-users", "1"}, nil},
	{"PUT", "/api/v1/device/enableLostMode", nil, []string{"device", "enable-lost-mode", "1", "--message", "m"}, nil},
	{"GET", "/api/v1/device/disableLostMode", map[string]string{"checkViability": "true"}, []string{"device", "disable-lost-mode", "1", "--check"}, nil},
	{"PUT", "/api/v1/device/disableLostMode", nil, []string{"device", "disable-lost-mode", "1"}, nil},
	{"PUT", "/api/v1/device/relinquishOwnership", map[string]string{"checkViability": "true"}, []string{"device", "relinquish", "1", "--check"}, nil},
	{"PUT", "/api/v1/device/relinquishOwnershipCancel", nil, []string{"device", "relinquish-cancel", "1"}, nil},

	// users
	{"GET", "/api/v1/account", nil, []string{"user", "list"}, nil},
	{"GET", "/api/v1/account", map[string]string{"fq": "UID=jdoe@example.com"}, []string{"user", "view", "jdoe@example.com"},
		map[string]string{"GET /api/v1/account": `{"errors":null,"result":{"totalCount":1,"searchResults":[{"id":7,"uid":"jdoe@example.com"}]}}`}},
	{"GET", "/api/v1/account", map[string]string{"uid": "jdoe@example.com"}, []string{"user", "exists", "jdoe@example.com"}, nil},
	{"GET", "/api/v1/user/account", nil, []string{"user", "me"}, nil},
	{"GET", "/api/v1/account/metadata", nil, []string{"user", "metadata"}, nil},
	{"POST", "/api/v1/account/search", nil, []string{"user", "search"},
		map[string]string{"POST /api/v1/account/search": `{"errors":null,"result":{"totalCount":0,"searchResults":[]}}`}},
	{"POST", "/api/v1/accounts", nil, []string{"user", "create", "--email", "jdoe@example.com"},
		map[string]string{"GET /api/v1/account": `{"errors":null,"result":{"totalCount":1,"searchResults":[{"id":7,"uid":"jdoe@example.com"}]}}`}},
	{"POST", "/api/v1/account", nil, []string{"user", "create", "--email", "jdoe@example.com", "--password", "Secret!23"},
		map[string]string{"GET /api/v1/account": `{"errors":null,"result":{"totalCount":1,"searchResults":[{"id":7,"uid":"jdoe@example.com"}]}}`}},
	{"PUT", "/api/v1/account", nil, []string{"user", "update", "jdoe@example.com", "--first-name", "John"},
		map[string]string{"GET /api/v1/account": `{"errors":null,"result":{"totalCount":1,"searchResults":[{"id":7,"uid":"jdoe@example.com","emailAddress":"jdoe@example.com","firstName":null,"lastName":null,"displayName":null,"accountSource":"LOCAL","enabled":true,"passwordNeverExpire":false,"androidWorkDeviceAccountEnabled":false,"managedAppleId":null,"edipi":null,"gdprProfileId":null}]}}`}},
	{"PUT", "/api/v1/account", map[string]string{"groupIds": "2"}, []string{"user", "add-to-group", "1", "--group-id", "2"}, nil},
	{"DELETE", "/api/v1/account", map[string]string{"groupIds": "2"}, []string{"user", "remove-from-group", "1", "--group-id", "2"}, nil},
	{"GET", "/api/v1/account", map[string]string{"checkDeleteViability": "true"}, []string{"user", "delete", "1"}, nil},
	{"DELETE", "/api/v1/account", map[string]string{"id": "1"}, []string{"user", "delete", "1"}, nil},
	{"POST", "/api/v1/invite/enduser", nil, []string{"user", "invite", "1"}, nil},
	{"POST", "/api/v1/tenant/enrollment/url", nil, []string{"user", "enrollment-url", "1"}, nil},
	{"POST", "/api/v1/ldap/import/user", nil, []string{"user", "ldap-import", "jdoe"}, nil},
	{"GET", "/api/v1/ldap/import/user/status", nil, []string{"user", "ldap-import-status", "1"}, nil},
	{"PUT", "/api/v1/userpolicy", nil, []string{"user", "registration-settings", "--mode", "PIN"}, nil},
	{"POST", "/api/v1/account/login-nonce", nil, []string{"user", "login-nonce"}, nil},
	{"PATCH", "/api/v1/account/1/customattributes", nil, []string{"user", "attrs", "set", "1", "a=b"}, nil},
	{"PATCH", "/api/v1/account/customattributes", nil, []string{"user", "attrs", "set", "1,2", "a=b"}, nil},
	{"DELETE", "/api/v1/account/customattributes", nil, []string{"user", "attrs", "delete", "1", "a"}, nil},
	{"DELETE", "/api/v1/account/1/customattributes", nil, []string{"user", "attrs", "clear", "1"}, nil},
	{"POST", "/api/v1/account/regpin", nil, []string{"user", "pin", "generate", "1"}, nil},
	{"GET", "/api/v1/account/1/registrationPin", nil, []string{"user", "pin", "view", "1"}, nil},
	{"GET", "/api/v1/report/flex/job/summary", nil, []string{"user", "report", "list"}, nil},
	{"GET", "/api/v1/report/flex/job/output/latest/download", nil, []string{"user", "report", "download", "users"}, nil},

	// user groups and device groups
	{"POST", "/api/v1/group", nil, []string{"group", "create", "Sales"}, nil},
	{"GET", "/api/v1/group", nil, []string{"group", "list"}, nil},
	{"PUT", "/api/v1/group/1/accounts", map[string]string{"append": "true"}, []string{"group", "members", "add", "1", "2"}, nil},
	{"GET", "/api/v1/group", map[string]string{"checkDeleteViability": "true"}, []string{"group", "check-delete", "1"}, nil},
	{"DELETE", "/api/v1/group", map[string]string{"id": "1"}, []string{"group", "delete", "1"}, nil},
	{"POST", "/api/v1/rule_group", map[string]string{"type": "DEVICE"}, []string{"device-group", "create", "Kiosks"}, nil},
	{"GET", "/api/v1/rule_group", map[string]string{"type": "DEVICE"}, []string{"device-group", "list"}, nil},
	{"GET", "/api/v1/rule_group/1", nil, []string{"device-group", "view", "1"}, nil},
	{"GET", "/api/v1/rule_group/partitions", nil, []string{"device-group", "find", "All Devices"}, nil},
	{"GET", "/api/v1/device", map[string]string{"fq": "platformType='IOS'"}, []string{"device-group", "devices", "1"},
		map[string]string{"GET /api/v1/rule_group/1": `{"errors":null,"result":{"id":1,"definition":"platformType='IOS'"}}`}},
	{"POST", "/api/v1/rule_group/1/devices", nil, []string{"device-group", "members", "add", "1", "2"}, nil},
	{"DELETE", "/api/v1/rule_group/1/devices", nil, []string{"device-group", "members", "remove", "1", "2"}, nil},

	// apps
	{"GET", "/api/v1/app", nil, []string{"app", "list"}, nil},
	{"GET", "/api/v1/app/1", nil, []string{"app", "view", "1"}, nil},
	{"GET", "/api/v1/app/category", nil, []string{"app", "categories"}, nil},
	{"GET", "/api/v1/app/version/1/distribution", nil, []string{"app", "distribution", "1"},
		map[string]string{"GET /api/v1/app/version/1/config/types": `{"errors":null,"result":{"INSTALL":{}}}`}},
	{"GET", "/api/v1/app/distributionfilters/23000", nil, []string{"app", "filters"}, nil},
	{"GET", "/api/v1/app/user/1", nil, []string{"app", "available", "user", "1"}, nil},
	{"GET", "/api/v1/app/device/1", nil, []string{"app", "available", "device", "1"}, nil},
	{"GET", "/api/v1/app/rulegroup/1", nil, []string{"app", "available", "device-group", "1"}, nil},
	{"GET", "/api/v1/app/eligibledevices", nil, []string{"app", "eligible", "com.example.app"}, nil},
	{"POST", "/api/v1/app", nil, []string{"app", "create", "FILE", "--platform", "ANDROID"}, nil},
	{"DELETE", "/api/v1/app/1/version", map[string]string{"id": "2"}, []string{"app", "delete", "1", "2"}, nil},
	{"GET", "/api/v1/applist", nil, []string{"app", "inventory", "list"}, nil},
	{"GET", "/api/v1/app/inventory", map[string]string{"deviceId": "1"}, []string{"app", "inventory", "device", "1"}, nil},
	{"GET", "/api/v2/app/inventory", map[string]string{"deviceId": "1"}, []string{"app", "inventory", "device", "1", "--v2"},
		map[string]string{"GET /api/v2/app/inventory": `{"errors":null,"result":[]}`}},
	{"GET", "/api/v2/app/inventory", map[string]string{"deviceIds": "1,2"}, []string{"app", "inventory", "device", "1,2"},
		map[string]string{"GET /api/v2/app/inventory": `{"errors":null,"result":[]}`}},
	{"GET", "/api/v1/app/googleplay", nil, []string{"app", "store", "search", "google", "mail"}, nil},
	{"GET", "/api/v2/app/itunes", nil, []string{"app", "store", "search", "itunes", "mail"}, nil},
	{"GET", "/api/v1/app/googleplay/com.example.app", nil, []string{"app", "store", "view", "google", "com.example.app"}, nil},
	{"GET", "/api/v1/app/itunes/123", nil, []string{"app", "store", "view", "itunes", "123"}, nil},
	{"GET", "/api/v1/app/appTracks", nil, []string{"app", "store", "tracks", "com.example.app"}, nil},
	{"POST", "/api/v2/app/public/google/com.example.app", nil, []string{"app", "store", "add", "com.example.app"}, nil},
	{"PUT", "/api/v1/app", map[string]string{"targetApplicationVersionId": "1"}, []string{"app", "inhouse", "copy", "1"}, nil},
	{"POST", "/api/v1/app/version/1/screenshots", nil, []string{"app", "inhouse", "screenshots", "1", "--phone", "FILE"}, nil},
	{"POST", "/api/v2/app/inhouse/1/icon", nil, []string{"app", "inhouse", "icon", "1", "FILE"}, nil},
	{"PUT", "/api/v2/app/inhouse/1", nil, []string{"app", "inhouse", "update", "1", "-f", "name=x"}, nil},
	{"GET", "/api/v1/app/inhouse/profiles", nil, []string{"app", "inhouse", "profiles"}, nil},
	{"PUT", "/api/v2/app/install", nil, []string{"app", "install", "devices", "com.example.app", coverageGUID}, nil},
	{"PUT", "/api/v2/app/install/async", nil, []string{"app", "install", "devices", "com.example.app", coverageGUID, "--async"}, nil},
	{"PUT", "/api/v2/app/1/install", nil, []string{"app", "install", "dist", "1", "--new-install", "--everyone-with-app"}, nil},
	{"PUT", "/api/v2/app/1/install/async", nil, []string{"app", "install", "dist", "1", "--new-install", "--everyone-with-app", "--async"}, nil},
	{"GET", "/api/v1/asynctask/all", nil, []string{"app", "task", "list"}, nil},
	{"GET", "/api/v1/asynctask/status/" + coverageGUID, nil, []string{"app", "task", "status", coverageGUID}, nil},
	{"POST", "/api/v1/asynctask", nil, []string{"app", "task", "retry"}, nil},
	{"GET", "/api/v1/app/review", nil, []string{"app", "review", "list"}, nil},
	{"POST", "/c/api/v1/appstore/1/review", nil, []string{"app", "review", "add", "1", "--rating", "5", "--title", "t", "--summary", "s"}, nil},
	{"DELETE", "/api/v1/app/review/delete", map[string]string{"id": "1"}, []string{"app", "review", "delete", "1"}, nil},
	{"GET", "/api/v1/app/version/1/config", nil, []string{"app", "config", "list", "1"},
		map[string]string{"GET /api/v1/app/version/1/config/types": `{"errors":null,"result":{"INSTALL":{}}}`, "GET /api/v1/app/version/1/config": `{"errors":null,"result":{"totalCount":1,"searchResults":[{"id":2,"type":"INSTALL"}]}}`}},
	{"GET", "/api/v1/app/version/1/config/2", nil, []string{"app", "config", "get", "1", "2"},
		map[string]string{"GET /api/v1/app/version/1/config/types": `{"errors":null,"result":{"INSTALL":{}}}`, "GET /api/v1/app/version/1/config/2": `{"errors":null,"result":{"id":2,"type":"INSTALL","settings":{"type":"INSTALL"}}}`}},
	{"GET", "/api/v1/app/version/1/config/types", nil, []string{"app", "config", "types", "1"},
		map[string]string{"GET /api/v1/app/version/1/config/types": `{"errors":null,"result":{"INSTALL":{}}}`, "GET /api/v1/app/version/1/config": `{"errors":null,"result":{"totalCount":1,"searchResults":[{"id":2,"type":"INSTALL"}]}}`}},
	{"POST", "/api/v1/app/version/1/config", nil, []string{"app", "config", "create", "1"},
		map[string]string{"GET /api/v1/app/version/1/config/types": `{"errors":null,"result":{"INSTALL":{}}}`, "GET /api/v1/app/version/1/config": `{"errors":null,"result":{"totalCount":1,"searchResults":[{"id":2,"type":"INSTALL"}]}}`}},
	{"PUT", "/api/v1/app/version/1/config/2", nil, []string{"app", "config", "update", "1", "2", "-f", "name=x"},
		map[string]string{"GET /api/v1/app/version/1/config/2": `{"errors":null,"result":{"id":2,"type":"INSTALL","groupIds":[],"accountIds":[],"deviceIds":[],"ruleGroupIds":[]}}`, "GET /api/v1/app/version/1/config/types": `{"errors":null,"result":{"INSTALL":{}}}`, "GET /api/v1/app/version/1/config": `{"errors":null,"result":{"totalCount":1,"searchResults":[{"id":2,"type":"INSTALL"}]}}`}},
	{"GET", "/api/v1/app/version/1/customattributes", nil, []string{"app", "attrs", "get", "1"},
		map[string]string{"GET /api/v1/app/version/1/config/types": `{"errors":null,"result":{"INSTALL":{}}}`, "GET /api/v1/app/version/1/config": `{"errors":null,"result":{"totalCount":1,"searchResults":[{"id":2,"type":"INSTALL"}]}}`}},
	{"POST", "/api/v1/app/version/1/customattributes", nil, []string{"app", "attrs", "set", "1", "a=b"}, nil},
	{"PATCH", "/api/v1/app/version/1/customattributes", nil, []string{"app", "attrs", "update", "1", "a=b"}, nil},
	{"DELETE", "/api/v1/app/version/1/customattributes", nil, []string{"app", "attrs", "delete", "1", "a=b"}, nil},
	{"PATCH", "/api/v1/app/1/customattributes", nil, []string{"app", "attrs", "update", "1", "a=b", "--app"}, nil},
	{"DELETE", "/api/v1/app/1/customattributes", nil, []string{"app", "attrs", "delete", "1", "a=b", "--app"}, nil},
	{"PATCH", "/api/v1/app/customattributes", nil, []string{"app", "attrs", "update", "1,2", "a=b", "--app"}, nil},
	{"DELETE", "/api/v1/app/customattributes", nil, []string{"app", "attrs", "delete", "1,2", "a=b", "--app"}, nil},

	// bulk enrollment
	{"GET", "/api/v1/bulkenrollment/profile", nil, []string{"bulk", "list"}, nil},
	{"POST", "/api/v1/bulkenrollment/profile/upload", nil, []string{"bulk", "create", "FILE"}, nil},
	{"GET", "/api/v1/bulkenrollment/profile/1", nil, []string{"bulk", "get", "1"}, nil},
	{"GET", "/api/v1/bulkenrollment/profile/1/info", nil, []string{"bulk", "info", "1"}, nil},
	{"PUT", "/api/v1/bulkenrollment/profile", nil, []string{"bulk", "update", "1", "--name", "x"},
		map[string]string{"GET /api/v1/bulkenrollment/profile": `{"errors":null,"result":{"totalCount":1,"searchResults":[{"csvId":1,"csvName":"a","description":"d"}]}}`}},
	{"DELETE", "/api/v1/bulkenrollment/profile/1", nil, []string{"bulk", "delete", "1"}, nil},
	{"GET", "/api/v1/bulkenrollment/profile/1/download", nil, []string{"bulk", "download", "1"}, nil},
	{"GET", "/api/v1/bulkenrollment/profile/1/qrcode/generate", nil, []string{"bulk", "qrcode", "1"}, nil},
	{"PUT", "/api/v1/bulkenrollment/profile/1/qrcode", nil, []string{"bulk", "configure", "1", "-f", "a=b"}, nil},
	{"PUT", "/api/v1/bulkenrollment/profile/1/refreshtoken", nil, []string{"bulk", "refresh-token", "1"}, nil},
	{"PUT", "/api/v1/bulkenrollment/profile/1", map[string]string{"action": "refreshToken"}, []string{"bulk", "set-expiry", "1", "2099-12-31"}, nil},
	{"PUT", "/api/v1/bulkenrollment/profile/1", map[string]string{"action": "addUser"}, []string{"bulk", "set-user", "1", "2", "--expiry", "2099-12-31"}, nil},
	{"GET", "/api/v1/bulkenrollment/profile/1/devices", nil, []string{"bulk", "device", "list", "1"}, nil},
	{"POST", "/api/v1/bulkenrollment/profile/1", nil, []string{"bulk", "device", "add", "1", "--imei", "123"}, nil},
	{"POST", "/api/v1/bulkenrollment/profile/1/upload", nil, []string{"bulk", "device", "upload", "1", "FILE"}, nil},
	{"PUT", "/api/v1/bulkenrollment/profile/1/devices", nil, []string{"bulk", "device", "edit", "1", "2", "--imei", "123"},
		map[string]string{"GET /api/v1/bulkenrollment/profile/1/devices": `{"errors":null,"result":{"totalCount":1,"searchResults":[{"id":2,"imei":"9","serialNumber":"S","manufacturer":"M","customAttributes":{"a":"b"}}]}}`}},
	{"DELETE", "/api/v1/bulkenrollment/profile/1/devices", map[string]string{"ids": "2"}, []string{"bulk", "device", "delete", "1", "2"}, nil},
}

// coverageFallback answers unregistered routes: an empty search page for
// GETs (so viability checks pass), a count of 1 for writes.
func coverageFallback(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		fmt.Fprint(w, `{"errors":null,"result":{"totalCount":0,"searchResults":[]}}`)
		return
	}
	fmt.Fprint(w, `{"errors":null,"result":1}`)
}

func TestEndpointCoverage(t *testing.T) {
	file := filepath.Join(t.TempDir(), "upload.bin")
	if err := os.WriteFile(file, []byte("id,imei\n1,123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range coverage {
		args := make([]string, len(c.args))
		for i, a := range c.args {
			args[i] = strings.ReplaceAll(a, "FILE", file)
		}
		t.Run(c.method+" "+c.path+" "+strings.Join(c.args, " "), func(t *testing.T) {
			m := newMockFallback(t, coverageFallback)
			for pattern, body := range c.mocks {
				m.on(pattern, body)
			}
			if out, err := m.runErr(t, append(args, "--yes")...); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			req := m.last(t, c.method, c.path)
			for k, v := range c.query {
				if got := strings.Join(req.params()[k], ","); got != v {
					t.Errorf("param %s = %q, want %q (%v)", k, got, v, req.params())
				}
			}

			// Every command rejects a bad --output before sending anything.
			m.reqs = nil
			if _, err := m.runErr(t, append(args, "--yes", "-o", "yaml")...); err == nil || !strings.Contains(err.Error(), "invalid --output") {
				t.Errorf("-o yaml: got %v, want invalid --output", err)
			}
			if len(m.reqs) > 0 {
				t.Errorf("-o yaml sent %d request(s) before failing", len(m.reqs))
			}
		})
	}
}
