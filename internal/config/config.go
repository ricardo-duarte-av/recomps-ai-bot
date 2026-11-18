// Package config loads the bot's YAML configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Matrix   Matrix   `yaml:"matrix"`
	OpenAI   OpenAI   `yaml:"openai"`
	GitHub   GitHub   `yaml:"github"`
	Forges   Forges   `yaml:"forges"`
	Database Database `yaml:"database"`
	Web      Web      `yaml:"web"`
	Detect   Detect   `yaml:"detect"`
}

// Detect configures automatic detection of repository links posted in rooms.
type Detect struct {
	// Enabled defaults to true when unset.
	Enabled *bool `yaml:"enabled"`
	// MentionRoom adds an @room ping to suggestions (defaults to true). The bot needs
	// the room's "notify everyone" power level for the ping to notify anyone.
	MentionRoom *bool `yaml:"mention_room"`
	// MaxLinksPerMessage caps how many repos are checked from one message (default 3).
	MaxLinksPerMessage int `yaml:"max_links_per_message"`
}

func (d Detect) IsEnabled() bool     { return d.Enabled == nil || *d.Enabled }
func (d Detect) ShouldMention() bool { return d.MentionRoom == nil || *d.MentionRoom }

// Web configures the public read-only list page.
type Web struct {
	// Enabled defaults to true when unset.
	Enabled *bool  `yaml:"enabled"`
	Listen  string `yaml:"listen"`
	Title   string `yaml:"title"`
}

// IsEnabled reports whether the web page should be served.
func (w Web) IsEnabled() bool { return w.Enabled == nil || *w.Enabled }

type Matrix struct {
	Homeserver     string   `yaml:"homeserver"`
	UserID         string   `yaml:"user_id"`
	Password       string   `yaml:"password"`
	DeviceName     string   `yaml:"device_name"`
	PickleKey      string   `yaml:"pickle_key"`
	AllowedRooms   []string `yaml:"allowed_rooms"`
	ModeratorLevel int      `yaml:"moderator_level"`
	// IgnoredUsers lists Matrix user IDs (e.g. other bots) whose messages and
	// reactions are ignored entirely: no commands, no link detection.
	IgnoredUsers []string `yaml:"ignored_users"`
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

// Forges configures git hosts other than the well-known ones.
type Forges struct {
	// Hosts lists self-hosted forges and/or API tokens. Hosts not listed here are auto-detected.
	Hosts []ForgeHost `yaml:"hosts"`
	// AllowPrivateNetworks permits fetching from private/loopback addresses (e.g. a LAN Gitea).
	AllowPrivateNetworks bool `yaml:"allow_private_networks"`
}

type ForgeHost struct {
	Host  string `yaml:"host"`
	Type  string `yaml:"type"` // github | gitlab | gitea | forgejo | generic
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
	if c.Detect.MaxLinksPerMessage <= 0 {
		c.Detect.MaxLinksPerMessage = 3
	}
	if c.Web.Listen == "" {
		c.Web.Listen = "0.0.0.0:8080"
	}
	if c.Web.Title == "" {
		c.Web.Title = "Recomps & Decomps"
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
	for i, u := range c.Matrix.IgnoredUsers {
		if !strings.HasPrefix(u, "@") || !strings.Contains(u, ":") {
			errs = append(errs, fmt.Errorf("matrix.ignored_users[%d] %q must be a full user ID like @bot:example.org", i, u))
		}
	}
	for i, h := range c.Forges.Hosts {
		if h.Host == "" {
			errs = append(errs, fmt.Errorf("forges.hosts[%d].host is required", i))
		}
		switch h.Type {
		case "", "github", "gitlab", "gitea", "forgejo", "generic":
		default:
			errs = append(errs, fmt.Errorf("forges.hosts[%d].type %q must be github, gitlab, gitea, forgejo or generic", i, h.Type))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &c, nil
}
