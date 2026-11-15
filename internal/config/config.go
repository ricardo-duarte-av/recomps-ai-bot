// Package config loads the bot's YAML configuration.
package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Matrix   Matrix   `yaml:"matrix"`
	OpenAI   OpenAI   `yaml:"openai"`
	GitHub   GitHub   `yaml:"github"`
	Database Database `yaml:"database"`
}

type Matrix struct {
	Homeserver     string   `yaml:"homeserver"`
	UserID         string   `yaml:"user_id"`
	Password       string   `yaml:"password"`
	DeviceName     string   `yaml:"device_name"`
	PickleKey      string   `yaml:"pickle_key"`
	AllowedRooms   []string `yaml:"allowed_rooms"`
	ModeratorLevel int      `yaml:"moderator_level"`
}

type OpenAI struct {
	APIKey  string `yaml:"api_key"`
	Model   string `yaml:"model"`
	BaseURL string `yaml:"base_url"`
	// ReasoningEffort is sent as reasoning_effort when set (e.g. "minimal", "low").
	ReasoningEffort string `yaml:"reasoning_effort"`
}

type GitHub struct {
	Token string `yaml:"token"`
}

type Database struct {
	Path string `yaml:"path"`
}

// Load reads, defaults and validates the config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if c.Matrix.DeviceName == "" {
		c.Matrix.DeviceName = "recompbot"
	}
	if c.Matrix.ModeratorLevel == 0 {
		c.Matrix.ModeratorLevel = 50
	}
	if c.OpenAI.Model == "" {
		c.OpenAI.Model = "gpt-5-mini"
	}
	if c.Database.Path == "" {
		c.Database.Path = "/data/sqlite.db"
	}

	var errs []error
	req := func(v, name string) {
		if v == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
	}
	req(c.Matrix.Homeserver, "matrix.homeserver")
	req(c.Matrix.UserID, "matrix.user_id")
	req(c.Matrix.Password, "matrix.password")
	req(c.Matrix.PickleKey, "matrix.pickle_key")
	req(c.OpenAI.APIKey, "openai.api_key")
	if len(c.Matrix.AllowedRooms) == 0 {
		errs = append(errs, errors.New("matrix.allowed_rooms must list at least one room"))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &c, nil
}
