package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server      ServerConfig                `yaml:"server"`
	Admin       AdminConfig                 `yaml:"admin"`
	Storage     StorageConfig               `yaml:"storage"`
	Debug       DebugConfig                 `yaml:"debug"`
	Plugins     map[string]PluginBinary     `yaml:"plugins"`
	Providers   map[string]ProviderInstance `yaml:"providers"`
	Credentials map[string]Credential       `yaml:"credentials"`
	Models      map[string]ModelAlias       `yaml:"models"`

	ConfigDir string `yaml:"-"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type AdminConfig struct {
	Host  string `yaml:"host"`
	Port  int    `yaml:"port"`
	Token string `yaml:"token"`
}

type StorageConfig struct {
	SQLite string `yaml:"sqlite"`
	Traces string `yaml:"traces"`
}

type DebugConfig struct {
	Level         string `yaml:"level"`
	Retention     string `yaml:"retention"`
	FullRetention string `yaml:"full_retention"`

	RetentionD     time.Duration `yaml:"-"`
	FullRetentionD time.Duration `yaml:"-"`
}

type PluginBinary struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
}

type ProviderInstance struct {
	Plugin     string         `yaml:"plugin"`
	Credential string         `yaml:"credential"`
	Config     map[string]any `yaml:"config"`
}

type Credential struct {
	Provider string `yaml:"provider"`
	Env      string `yaml:"env"`
}

type ModelAlias struct {
	Provider      string `yaml:"provider"`
	UpstreamModel string `yaml:"upstream_model"`
	Reasoning     string `yaml:"reasoning"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	cfg.ConfigDir = filepath.Dir(abs)
	cfg.applyDefaults()
	if err := cfg.expandPaths(); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Server.Host == "" {
		c.Server.Host = "0.0.0.0"
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8317
	}
	if c.Admin.Host == "" {
		c.Admin.Host = "127.0.0.1"
	}
	if c.Admin.Port == 0 {
		c.Admin.Port = 8318
	}
	if c.Storage.SQLite == "" {
		c.Storage.SQLite = "~/.providerapi/providerapi.db"
	}
	if c.Storage.Traces == "" {
		c.Storage.Traces = "~/.providerapi/traces"
	}
	if c.Debug.Level == "" {
		c.Debug.Level = "metadata"
	}
	c.Debug.RetentionD = parseDuration(c.Debug.Retention, 168*time.Hour)
	c.Debug.FullRetentionD = parseDuration(c.Debug.FullRetention, 24*time.Hour)
	if c.Plugins == nil {
		c.Plugins = map[string]PluginBinary{}
	}
	if c.Providers == nil {
		c.Providers = map[string]ProviderInstance{}
	}
	if c.Credentials == nil {
		c.Credentials = map[string]Credential{}
	}
	if c.Models == nil {
		c.Models = map[string]ModelAlias{}
	}
}

func (c *Config) expandPaths() error {
	var err error
	c.Storage.SQLite, err = ExpandPath(c.Storage.SQLite)
	if err != nil {
		return err
	}
	c.Storage.Traces, err = ExpandPath(c.Storage.Traces)
	if err != nil {
		return err
	}
	for name, p := range c.Plugins {
		p.Command, err = c.resolveCommand(p.Command)
		if err != nil {
			return err
		}
		c.Plugins[name] = p
	}
	return nil
}

func (c *Config) resolveCommand(cmd string) (string, error) {
	if cmd == "" {
		return "", nil
	}
	expanded, err := ExpandPath(cmd)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(expanded) {
		return expanded, nil
	}
	return filepath.Join(c.ConfigDir, expanded), nil
}

func (c *Config) validate() error {
	switch strings.ToLower(c.Debug.Level) {
	case "off", "metadata", "full":
	default:
		return fmt.Errorf("debug.level must be off, metadata, or full")
	}
	for name, alias := range c.Models {
		if alias.Provider == "" {
			return fmt.Errorf("models.%s.provider is required", name)
		}
		if _, ok := c.Providers[alias.Provider]; !ok {
			return fmt.Errorf("models.%s references unknown provider %q", name, alias.Provider)
		}
	}
	for name, inst := range c.Providers {
		if inst.Plugin == "" {
			return fmt.Errorf("providers.%s.plugin is required", name)
		}
		if _, ok := c.Plugins[inst.Plugin]; !ok {
			return fmt.Errorf("providers.%s references unknown plugin %q", name, inst.Plugin)
		}
		if inst.Credential != "" {
			if _, ok := c.Credentials[inst.Credential]; !ok {
				return fmt.Errorf("providers.%s references unknown credential %q", name, inst.Credential)
			}
		}
	}
	return nil
}

func (c *Config) DataDir() string {
	return filepath.Dir(c.Storage.SQLite)
}

func parseDuration(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}

func ExpandPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}
