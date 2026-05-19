package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
)

const configPath = "config.json"
const configExamplePath = "config.example.json"

type AppConfig struct {
	Harness        string   `json:"harness"`
	DefaultSession string   `json:"default_session"`
	ExcludeDirs    []string `json:"exclude_dirs"`
}

// defaultExcludeDirs are directories never listed as projects by /api/dirs:
// tclaw's own frontend, upload storage, the PoC and common infra dirs.
var defaultExcludeDirs = []string{
	"web", "uploads", "pty-experiment", "scripts", "skills", "claude", "tls", "data",
}

var (
	configMu  sync.RWMutex
	appConfig = AppConfig{
		Harness:        "claude",
		DefaultSession: "agent",
		ExcludeDirs:    defaultExcludeDirs,
	}
)

func loadConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg, cfgErr := loadConfigTemplate(configExamplePath)
			if cfgErr != nil {
				cfg = appConfig
			}
			return saveConfig(path, cfg)
		}
		return err
	}

	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}

	cfg.normalize()

	configMu.Lock()
	appConfig = cfg
	configMu.Unlock()
	return nil
}

func loadConfigTemplate(path string) (AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AppConfig{}, err
	}

	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return AppConfig{}, err
	}

	cfg.normalize()
	return cfg, nil
}

func saveConfig(path string, cfg AppConfig) error {
	cfg.normalize()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}

	configMu.Lock()
	appConfig = cfg
	configMu.Unlock()
	return nil
}

func getConfig() AppConfig {
	configMu.RLock()
	defer configMu.RUnlock()
	return appConfig
}

func (c *AppConfig) normalize() {
	c.Harness = strings.TrimSpace(c.Harness)
	if c.Harness == "" {
		c.Harness = "claude"
	}
	c.DefaultSession = strings.TrimSpace(c.DefaultSession)
	if c.DefaultSession == "" {
		c.DefaultSession = "agent"
	}
	if c.ExcludeDirs == nil {
		c.ExcludeDirs = defaultExcludeDirs
	}
}
