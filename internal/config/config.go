// Package config loads the bot's YAML configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Matrix    Matrix    `yaml:"matrix"`
	OpenAI    OpenAI    `yaml:"openai"`
	GitHub    GitHub    `yaml:"github"`
	Forges    Forges    `yaml:"forges"`
	Database  Database  `yaml:"database"`
	Web       Web       `yaml:"web"`
	Detect    Detect    `yaml:"detect"`
	IGDB      IGDB      `yaml:"igdb"`
	Releases  Releases  `yaml:"releases"`
	Archivist Archivist `yaml:"archivist"`
}

// Archivist configures the matrix-archivist sync hook called after the list
// changes, so the archivist clones new repos right away.
type Archivist struct {
	// HookURL is the source's hook, e.g. http://archivist:8080/hooks/sources/recomps.
	// Empty turns it off.
	HookURL string `yaml:"hook_url"`
	// HookToken is the source's hook_token in the archivist's config.
	HookToken string `yaml:"hook_token"`
}

// IsEnabled reports whether the archivist hook is configured.
func (a Archivist) IsEnabled() bool { return a.HookURL != "" }

// Releases configures the daily report of new releases of the listed repos.
type Releases struct {
	// Time is the local time of day ("HH:MM") the report is posted. The report
	// covers the 24 hours before it. Empty turns the report off.
	Time string `yaml:"time"`
	// Timezone is an IANA zone name such as "Europe/Lisbon" (default UTC).
	Timezone string `yaml:"timezone"`
	// Room (ID or alias) gets the report; defaults to matrix.announce_room.
	Room string `yaml:"room"`

	// Hour, Minute and Location are parsed from Time and Timezone by Load.
	Hour, Minute int            `yaml:"-"`
	Location     *time.Location `yaml:"-"`
}

// IsEnabled reports whether the daily release report is configured.
func (r Releases) IsEnabled() bool { return r.Time != "" }

// IGDB holds the Twitch application credentials used to call the IGDB API.
// Linking to IGDB is off when they're empty.
type IGDB struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
}

// IsEnabled reports whether IGDB credentials are configured.
func (i IGDB) IsEnabled() bool { return i.ClientID != "" }

// Detect configures automatic detection of repository links posted in rooms.
type Detect struct {
	// Enabled defaults to true when unset.
	Enabled *bool `yaml:"enabled"`
	// Mention picks who a suggestion pings: MentionModerators (the default) pings
	// each member of the suggestion's room with at least the moderator power level,
	// MentionRoom pings everyone with @room, MentionNone pings nobody.
	Mention string `yaml:"mention"`
	// MentionRoomLegacy is the old mention_room option: false still turns pings
	// off when mention is unset; true is ignored in favor of pinging moderators.
	MentionRoomLegacy *bool `yaml:"mention_room"`
	// MaxLinksPerMessage caps how many repos are checked from one message (default 3).
	MaxLinksPerMessage int `yaml:"max_links_per_message"`
	// Auto adds fully classified repos without asking, when posted by someone with
	// at least AutoLevel power in the room (default false). A room's
	// org.recompbot.auto_add state event overrides it either way.
	Auto bool `yaml:"auto"`
	// AutoLevel is the power level a link's poster needs for it to be auto-added
	// (default 10). Links from others are suggested with a prompt as usual.
	AutoLevel *int `yaml:"auto_level"`
}

func (d Detect) IsEnabled() bool { return d.Enabled == nil || *d.Enabled }

// AutoMinLevel is AutoLevel, defaulting to 10.
func (d Detect) AutoMinLevel() int {
	if d.AutoLevel == nil {
		return 10
	}
	return *d.AutoLevel
}

// Values for Detect.Mention.
const (
	MentionModerators = "moderators"
	MentionRoom       = "room"
	MentionNone       = "none"
)

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
	// AnswerWithReply sends the bot's answers as Matrix replies to the message they
	// answer (defaults to true). When false they're sent as regular messages.
	AnswerWithReply *bool `yaml:"answer_with_reply"`
	// AnnounceRoom (room ID or alias, optional) gets a message for every new entry
	// added to the list. It doesn't have to be one of the allowed rooms.
	AnnounceRoom string `yaml:"announce_room"`
	// Keywords make the bot treat messages containing any of them (whole words,
	// any case) as requests, as if it had been mentioned. Such messages are only
	// answered when they turn out to be a real request (a search, an add...), so
	// chatter that happens to contain a keyword gets no reply.
	Keywords []string `yaml:"keywords"`
}

func (m Matrix) ShouldReply() bool { return m.AnswerWithReply == nil || *m.AnswerWithReply }

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
		c.OpenAI.Model = "gpt-6-luna"
	}
	if c.Detect.Mention == "" {
		c.Detect.Mention = MentionModerators
		if c.Detect.MentionRoomLegacy != nil && !*c.Detect.MentionRoomLegacy {
			c.Detect.Mention = MentionNone
		}
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
	if (c.IGDB.ClientID == "") != (c.IGDB.ClientSecret == "") {
		errs = append(errs, errors.New("igdb.client_id and igdb.client_secret must be set together"))
	}
	if c.Archivist.IsEnabled() && c.Archivist.HookToken == "" {
		errs = append(errs, errors.New("archivist.hook_token is required when archivist.hook_url is set"))
	}
	if c.Releases.IsEnabled() {
		t, err := time.Parse("15:04", strings.TrimSpace(c.Releases.Time))
		if err != nil {
			errs = append(errs, fmt.Errorf("releases.time %q must be HH:MM, e.g. 20:00", c.Releases.Time))
		}
		c.Releases.Hour, c.Releases.Minute = t.Hour(), t.Minute()
		c.Releases.Location = time.UTC
		if c.Releases.Timezone != "" {
			if c.Releases.Location, err = time.LoadLocation(c.Releases.Timezone); err != nil {
				errs = append(errs, fmt.Errorf("releases.timezone %q: %w", c.Releases.Timezone, err))
			}
		}
		if c.Releases.Room == "" {
			c.Releases.Room = c.Matrix.AnnounceRoom
		}
		if c.Releases.Room == "" {
			errs = append(errs, errors.New("releases.room (or matrix.announce_room) is required when releases.time is set"))
		}
	}
	switch c.Detect.Mention {
	case MentionModerators, MentionRoom, MentionNone:
	default:
		errs = append(errs, fmt.Errorf("detect.mention %q must be moderators, room or none", c.Detect.Mention))
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
