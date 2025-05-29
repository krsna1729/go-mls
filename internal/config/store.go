package config

import (
	"encoding/json"
	"go-mls/internal/stream"
	"os"
	"sync"
)

type ConfigStore struct {
	FilePath string
	mu       sync.Mutex
}

func NewConfigStore(path string) *ConfigStore {
	return &ConfigStore{FilePath: path}
}

// Save a named config (append or update in a map)
func (c *ConfigStore) SaveNamed(name string, cfg stream.RTMPRelayConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	configs := map[string]stream.RTMPRelayConfig{}
	_ = c.loadAll(&configs) // ignore error, start with empty if not found
	configs[name] = cfg
	file, err := os.Create(c.FilePath)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "    ") // pretty print with 4 spaces
	return encoder.Encode(configs)
}

// Load all configs as a map
func (c *ConfigStore) LoadAll() (map[string]stream.RTMPRelayConfig, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	configs := map[string]stream.RTMPRelayConfig{}
	if err := c.loadAll(&configs); err != nil {
		return nil, err
	}
	return configs, nil
}

// Load a single named config
func (c *ConfigStore) LoadNamed(name string) (stream.RTMPRelayConfig, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	configs := map[string]stream.RTMPRelayConfig{}
	if err := c.loadAll(&configs); err != nil {
		return stream.RTMPRelayConfig{}, err
	}
	cfg, ok := configs[name]
	if !ok {
		return stream.RTMPRelayConfig{}, os.ErrNotExist
	}
	return cfg, nil
}

// Delete a named config
func (c *ConfigStore) DeleteNamed(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	configs := map[string]stream.RTMPRelayConfig{}
	if err := c.loadAll(&configs); err != nil {
		return err
	}
	if _, ok := configs[name]; !ok {
		return os.ErrNotExist
	}
	delete(configs, name)
	file, err := os.Create(c.FilePath)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "    ")
	return encoder.Encode(configs)
}

// Remove configs with empty names or where both input_url and output_url are empty
func (c *ConfigStore) Clean() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	configs := map[string]stream.RTMPRelayConfig{}
	if err := c.loadAll(&configs); err != nil {
		return err
	}
	for name, cfg := range configs {
		if name == "" || (cfg.InputURL == "" && cfg.OutputURL == "") {
			delete(configs, name)
		}
	}
	file, err := os.Create(c.FilePath)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "    ")
	return encoder.Encode(configs)
}

func (c *ConfigStore) loadAll(target *map[string]stream.RTMPRelayConfig) error {
	file, err := os.Open(c.FilePath)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewDecoder(file).Decode(target)
}
