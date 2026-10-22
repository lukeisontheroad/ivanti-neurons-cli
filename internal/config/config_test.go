package config

import (
	"os"
	"path/filepath"
	"testing"
)

func withTempConfig(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yml")
	t.Setenv(EnvConfig, p)
	for _, e := range []string{EnvURL, EnvUsername, EnvPassword, EnvClientID, EnvClientSecret, EnvInstance} {
		t.Setenv(e, "")
	}
	return p
}

func TestSaveLoadRoundtripAndPerms(t *testing.T) {
	p := withTempConfig(t)
	if err := Save(&Config{Default: "work", Instances: map[string]Instance{
		"work": {URL: "https://na1.mobileiron.com", Username: "api", Password: "secret", Space: 23000},
	}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected 0600 perms, got %o", perm)
	}
	cfg, err := Load()
	if err != nil || cfg.Instances["work"].Password != "secret" || cfg.Instances["work"].Space != 23000 {
		t.Fatalf("roundtrip mismatch: %+v %v", cfg, err)
	}
}

func TestResolveOrder(t *testing.T) {
	withTempConfig(t)
	if err := Save(&Config{Default: "a", Instances: map[string]Instance{
		"a": {URL: "https://a", Username: "u", Password: "p"},
		"b": {URL: "https://b", ClientID: "c", ClientSecret: "s"},
	}}); err != nil {
		t.Fatal(err)
	}
	if name, _, _ := Resolve(""); name != "a" {
		t.Fatalf("default: got %q", name)
	}
	t.Setenv(EnvInstance, "b")
	if name, _, _ := Resolve(""); name != "b" {
		t.Fatalf("env instance: got %q", name)
	}
	if name, _, _ := Resolve("a"); name != "a" {
		t.Fatalf("flag: got %q", name)
	}
	t.Setenv(EnvURL, "https://env")
	t.Setenv(EnvClientID, "id")
	t.Setenv(EnvClientSecret, "sec")
	if name, inst, _ := Resolve("a"); name != EnvName || inst.ClientID != "id" {
		t.Fatalf("env override: got %q %+v", name, inst)
	}
	t.Setenv(EnvClientSecret, "")
	if _, _, err := Resolve("a"); err == nil {
		t.Fatal("incomplete env credentials must not fall back to the config file")
	}
	t.Setenv(EnvURL, "")
	if _, _, err := Resolve("missing"); err == nil {
		t.Fatal("expected unknown instance error")
	}
}

func TestUpdateSkipsEnvAndUnknown(t *testing.T) {
	withTempConfig(t)
	_ = Save(&Config{Instances: map[string]Instance{"a": {URL: "https://a", Username: "u", Password: "p"}}})
	if err := Update(EnvName, func(i *Instance) { i.Space = 1 }); err != nil {
		t.Fatal(err)
	}
	if err := Update("a", func(i *Instance) { i.Space = 42 }); err != nil {
		t.Fatal(err)
	}
	cfg, _ := Load()
	if cfg.Instances["a"].Space != 42 || len(cfg.Instances) != 1 {
		t.Fatalf("unexpected: %+v", cfg.Instances)
	}
}
