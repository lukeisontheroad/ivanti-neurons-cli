// Package config handles the ivanti-neurons CLI configuration file and
// instance (profile) resolution.
//
// Resolution order for the active instance:
//  1. IVANTI_URL plus IVANTI_USERNAME + IVANTI_PASSWORD (Basic auth) or
//     IVANTI_CLIENT_ID + IVANTI_CLIENT_SECRET (OAuth2 client credentials)
//     environment variables (full override, no file needed)
//  2. --instance flag
//  3. IVANTI_INSTANCE environment variable
//  4. "default" key in the config file
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	EnvURL          = "IVANTI_URL"
	EnvUsername     = "IVANTI_USERNAME"
	EnvPassword     = "IVANTI_PASSWORD"
	EnvClientID     = "IVANTI_CLIENT_ID"
	EnvClientSecret = "IVANTI_CLIENT_SECRET"
	EnvInstance     = "IVANTI_INSTANCE"
	EnvSpace        = "IVANTI_SPACE"  // device space (dmPartitionId) override
	EnvConfig       = "IVANTI_CONFIG" // override config file path
)

// EnvName is the instance name reported when credentials come from the
// environment; such instances are never written back to the config file.
const EnvName = "(env)"

// Instance holds the credentials for one Ivanti Neurons for MDM tenant.
// Exactly one credential shape is set: Username+Password (Basic auth) or
// ClientID+ClientSecret (OAuth2 client credentials, required when the API
// user has MFA enabled).
type Instance struct {
	URL          string `yaml:"url"`
	Username     string `yaml:"username,omitempty"`
	Password     string `yaml:"password,omitempty"`
	ClientID     string `yaml:"client_id,omitempty"`
	ClientSecret string `yaml:"client_secret,omitempty"`

	// Default device space (dmPartitionId) and content space (cmPartitionId),
	// cached from the tenant metadata at login.
	Space        int64 `yaml:"space,omitempty"`
	ContentSpace int64 `yaml:"content_space,omitempty"`

	// Cached OAuth2 access token (client-credentials instances only).
	AccessToken string    `yaml:"access_token,omitempty"`
	TokenExpiry time.Time `yaml:"token_expiry,omitempty"`
}

type Config struct {
	Default   string              `yaml:"default,omitempty"`
	Instances map[string]Instance `yaml:"instances,omitempty"`
}

// Path returns the config file location: $IVANTI_CONFIG, or
// $XDG_CONFIG_HOME/ivanti-neurons/config.yml, defaulting to
// ~/.config/ivanti-neurons/config.yml on every platform (like gh/glab;
// deliberately not os.UserConfigDir, whose macOS result
// "Library/Application Support" is hostile to terminal use).
func Path() (string, error) {
	if p := os.Getenv(EnvConfig); p != "" {
		return p, nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "ivanti-neurons", "config.yml"), nil
}

// Load reads the config file. A missing file yields an empty config, not an error.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return &Config{Instances: map[string]Instance{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	if cfg.Instances == nil {
		cfg.Instances = map[string]Instance{}
	}
	return &cfg, nil
}

// Save writes the config file with 0600 permissions, creating the directory
// if needed.
func Save(cfg *Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	// Temp file + rename: a concurrent reader or a Ctrl-C mid-write never
	// sees a truncated file. CreateTemp files are 0600.
	f, err := os.CreateTemp(filepath.Dir(p), ".config-*.yml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }() // no-op after a successful rename
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}

// Resolve returns the active instance. name is EnvName when credentials come
// from environment variables. flagInstance is the value of --instance.
func Resolve(flagInstance string) (name string, inst Instance, err error) {
	if u := os.Getenv(EnvURL); u != "" {
		inst = Instance{
			URL:          u,
			Username:     os.Getenv(EnvUsername),
			Password:     os.Getenv(EnvPassword),
			ClientID:     os.Getenv(EnvClientID),
			ClientSecret: os.Getenv(EnvClientSecret),
		}
		if !inst.HasCredentials() {
			// Never fall back to a configured tenant when the environment
			// names another one.
			return "", Instance{}, fmt.Errorf("%s is set but credentials are incomplete: set %s+%s or %s+%s",
				EnvURL, EnvUsername, EnvPassword, EnvClientID, EnvClientSecret)
		}
		return EnvName, inst, nil
	}
	cfg, err := Load()
	if err != nil {
		return "", Instance{}, err
	}
	name = ActiveName(cfg, flagInstance)
	if name == "" {
		return "", Instance{}, fmt.Errorf("not logged in: run `ivanti-neurons auth login` or set %s with %s+%s (or %s+%s)",
			EnvURL, EnvUsername, EnvPassword, EnvClientID, EnvClientSecret)
	}
	inst, ok := cfg.Instances[name]
	if !ok {
		return "", Instance{}, fmt.Errorf("unknown instance %q in config (available: %s)", name, keys(cfg.Instances))
	}
	if inst.URL == "" || !inst.HasCredentials() {
		return "", Instance{}, fmt.Errorf("instance %q is missing url or credentials: run `ivanti-neurons auth login`", name)
	}
	return name, inst, nil
}

// ActiveName picks the instance name: --instance flag, then IVANTI_INSTANCE,
// then the config default.
func ActiveName(cfg *Config, flagInstance string) string {
	if flagInstance != "" {
		return flagInstance
	}
	if n := os.Getenv(EnvInstance); n != "" {
		return n
	}
	return cfg.Default
}

// HasCredentials reports whether a complete credential pair is set.
func (i Instance) HasCredentials() bool {
	return (i.Username != "" && i.Password != "") || (i.ClientID != "" && i.ClientSecret != "")
}

// Update applies fn to the named instance and saves the config. It is a
// no-op for environment instances and unknown names (best-effort caches).
func Update(name string, fn func(*Instance)) error {
	if name == "" || name == EnvName {
		return nil
	}
	cfg, err := Load()
	if err != nil {
		return err
	}
	inst, ok := cfg.Instances[name]
	if !ok {
		return nil
	}
	fn(&inst)
	cfg.Instances[name] = inst
	return Save(cfg)
}

func keys(m map[string]Instance) string {
	if len(m) == 0 {
		return "none"
	}
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ", ")
}
