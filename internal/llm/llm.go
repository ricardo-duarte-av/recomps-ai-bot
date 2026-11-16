// Package llm wraps the OpenAI API calls used to interpret chat messages and READMEs.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/oses"
)

type Client struct {
	api             openai.Client
	model           string
	reasoningEffort string
}

func New(apiKey, model, baseURL, reasoningEffort string) *Client {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &Client{api: openai.NewClient(opts...), model: model, reasoningEffort: reasoningEffort}
}

// Intent is what the user wants, as understood from their message.
type Intent struct {
	Intent         string   `json:"intent"` // add | remove | query | unknown
	RepoURL        string   `json:"repo_url"`
	Game           string   `json:"game"`
	OriginalSystem string   `json:"original_system"`
	Kind           string   `json:"kind"` // recomp | decomp | "" (unspecified)
	OSes           []string `json:"oses"`

	// Search filters (query intent only), resolved against what's in the list.
	Titles  []string `json:"titles"`  // exact game titles in the list
	Systems []string `json:"systems"` // exact original systems in the list
	ListAll bool     `json:"list_all"`
}

// HasFilter reports whether a query narrows the list down in any way.
func (i *Intent) HasFilter() bool {
	return i.RepoURL != "" || i.Game != "" || i.OriginalSystem != "" || i.Kind != "" ||
		len(i.OSes) > 0 || len(i.Titles) > 0 || len(i.Systems) > 0
}

// RepoInfo is what was extracted from a README.
type RepoInfo struct {
	// IsGamePort is false for repos that aren't a recomp/decomp/port of a specific game
	// (emulators, tools, libraries, mods, unrelated software).
	IsGamePort     bool     `json:"is_game_port"`
	Game           string   `json:"game"`
	OriginalSystem string   `json:"original_system"`
	Kind           string   `json:"kind"`
	OSes           []string `json:"oses"`
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func enumProp(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

func osesProp(desc string) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": "string", "enum": oses.All}}
}

func objectSchema(props map[string]any) map[string]any {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return map[string]any{"type": "object", "properties": props, "required": keys, "additionalProperties": false}
}

var osRules = `Target platforms can be operating systems or consoles/handhelds (a port from one console to another, e.g. a SNES game recompiled to run on the Nintendo 3DS, targets "3ds"). Canonical platform values: ` + strings.Join(oses.All, ", ") + `.
Map distributions and devices onto them: Steam Deck, SteamOS, Fedora, Ubuntu, Debian, Arch, Mint and any other Linux distro => linux; OSX, OS X, Mac, macOS => macos; Win/PC => windows; Nintendo Switch => switch; New 3DS/2DS => 3ds; DSi => nds; PlayStation Vita => vita; Xbox Series X|S => xboxone; browser/WebAssembly => web.`

const intentPrompt = `You interpret chat messages sent to a Matrix bot that maintains a list of game recompilations ("recomps") and decompilations ("decomps").
Classify the message into exactly one intent:
- "add": the user asks to add a repository to the list.
- "remove": the user asks to remove a repository from the list.
- "query": anything that asks about the list: whether a game, series, system, platform or repository is in it, what's available, what runs where. When in doubt and the message names a game, system, platform or repository, it's a query.
- "unknown": only messages with nothing to look up (greetings, thanks, "what can you do?", unrelated chat).
Extract only what the user explicitly states; use "", [] or false for anything not stated. Do not guess.
- repo_url: the repository URL if present.
- game: the original game's title, spelled out in full when the user uses a well-known abbreviation (e.g. "OoT" => "The Legend of Zelda: Ocarina of Time", "SM64" => "Super Mario 64").
- original_system: the console/platform the game originally ran on, if stated (e.g. "Nintendo 64", "Game Boy Advance", "PlayStation 2").
- kind: "recomp" or "decomp" if stated, otherwise "".
- oses: target platforms (operating systems or consoles) the user mentions (for add: platforms the port runs on; for query/remove: platforms the user wants to play on).
Distinguish the original system (where the game came FROM) from target platforms (where the port RUNS): "a SNES recomp for 3DS" => original_system "Super Nintendo Entertainment System", oses ["3ds"]. In queries, "games from N64" or "N64 recomps" means original_system; "can I play X on my 3DS/Vita/Steam Deck" means oses.
For queries only, you are given the games currently in the list as "Title | Original system" lines:
- titles: the exact titles (the part before " | ", copied character-for-character) of the games the user asks about. Account for accents, abbreviations, subtitles, numbering and regional names ("Pokemon" == "Pokémon", "OoT" and "Zelda 64" == "Ocarina of Time"). A series name without a specific entry ("Zelda") matches every entry in the series. [] if the user didn't ask about specific games or none match.
- systems: the exact original systems (the part after " | ", copied character-for-character) the user asks about, e.g. "N64 decomps" => every spelling of Nintendo 64 in the list. [] if not asked or none match.
- list_all: true only if the user explicitly asks to see the whole list ("what's in the list?", "list everything").
For add/remove, leave titles and systems empty and list_all false.
` // osRules appended at runtime

const readmePrompt = `You read the README (or, if no README was found, the web page) of a git repository that is a recompilation ("recomp", static recompilation / port of a game binary) or a decompilation ("decomp", matching or non-matching source reconstruction) of a video game.
Extract:
- game: the full official title of the original game (e.g. "Pokémon Emerald", "The Legend of Zelda: Majora's Mask").
- original_system: the original console/platform (e.g. "Game Boy Advance", "Nintendo 64", "Xbox 360"). Use full common names.
- kind: "recomp" if it is a recompilation/port produced by recompiling the original binary, "decomp" if it is a decompilation project.
- oses: platforms (operating systems or consoles/handhelds) on which the resulting game runs natively, as stated by the README (supported platforms, release downloads, build instructions). If the README only describes building the original ROM (a matching decomp) and no native port, return [].
If the README does not say, use "" or [].
- is_game_port: true only if this repository is a recompilation, decompilation or source port of one specific video game. False for emulators, recompiler tools/frameworks (e.g. N64Recomp itself), libraries, mods/randomizers/romhacks, launchers, game engines not tied to one game, and unrelated software.
` // osRules appended at runtime

func (c *Client) complete(ctx context.Context, system, user, schemaName string, schema map[string]any, out any) error {
	params := openai.ChatCompletionNewParams{
		Model: c.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(system),
			openai.UserMessage(user),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   schemaName,
					Strict: openai.Bool(true),
					Schema: schema,
				},
			},
		},
	}
	if c.reasoningEffort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(c.reasoningEffort)
	}
	resp, err := c.api.Chat.Completions.New(ctx, params)
	if err != nil {
		return err
	}
	if len(resp.Choices) == 0 {
		return errors.New("empty response from OpenAI")
	}
	msg := resp.Choices[0].Message
	if msg.Refusal != "" {
		return fmt.Errorf("model refused: %s", msg.Refusal)
	}
	if err := json.Unmarshal([]byte(msg.Content), out); err != nil {
		return fmt.Errorf("decoding model output: %w", err)
	}
	return nil
}

// ParseIntent interprets a chat message (with the bot mention removed). known lists
// the games in the list as "Title | Original system", so a query's game and system
// names can be resolved to exact stored values in the same call.
func (c *Client) ParseIntent(ctx context.Context, message string, known []string) (*Intent, error) {
	schema := objectSchema(map[string]any{
		"intent":          enumProp("What the user wants", "add", "remove", "query", "unknown"),
		"repo_url":        strProp("Repository URL, or empty"),
		"game":            strProp("Original game title, or empty"),
		"original_system": strProp("Original console/platform, or empty"),
		"kind":            enumProp("recomp, decomp, or empty if not stated", "recomp", "decomp", ""),
		"oses":            osesProp("Target platforms (OSes or consoles) mentioned"),
		"titles":          map[string]any{"type": "array", "description": "Query: exact known titles asked about", "items": map[string]any{"type": "string"}},
		"systems":         map[string]any{"type": "array", "description": "Query: exact known original systems asked about", "items": map[string]any{"type": "string"}},
		"list_all":        map[string]any{"type": "boolean", "description": "Query: user explicitly asks for the whole list"},
	})
	user := "Message: " + message + "\n\nGames in the list:\n"
	if len(known) == 0 {
		user += "(the list is empty)"
	} else {
		user += strings.Join(known, "\n")
	}
	var out Intent
	if err := c.complete(ctx, intentPrompt+osRules, user, "intent", schema, &out); err != nil {
		return nil, err
	}
	out.OSes, _ = oses.NormalizeList(out.OSes)
	out.Titles, out.Systems = resolveKnown(known, out.Titles, out.Systems)
	return &out, nil
}

// resolveKnown keeps only titles and systems that really exist in known, so the
// model can't invent filters.
func resolveKnown(known, titles, systems []string) (validTitles, validSystems []string) {
	isTitle, isSystem := map[string]bool{}, map[string]bool{}
	for _, k := range known {
		title, system, _ := strings.Cut(k, " | ")
		isTitle[title], isSystem[system] = true, true
	}
	for _, t := range titles {
		if isTitle[t] && !slices.Contains(validTitles, t) {
			validTitles = append(validTitles, t)
		}
	}
	for _, sys := range systems {
		if isSystem[sys] && !slices.Contains(validSystems, sys) {
			validSystems = append(validSystems, sys)
		}
	}
	return validTitles, validSystems
}

// ExtractFromReadme determines game/system/kind/OSes from a README.
func (c *Client) ExtractFromReadme(ctx context.Context, repoURL, readme string) (*RepoInfo, error) {
	schema := objectSchema(map[string]any{
		"is_game_port":    map[string]any{"type": "boolean", "description": "Whether this repository is a recomp/decomp/source port of one specific game"},
		"game":            strProp("Original game title"),
		"original_system": strProp("Original console/platform"),
		"kind":            enumProp("recomp or decomp", "recomp", "decomp", ""),
		"oses":            osesProp("Platforms (OSes or consoles) the result runs on natively"),
	})
	var out RepoInfo
	user := "Repository: " + repoURL + "\n\nREADME:\n" + readme
	if err := c.complete(ctx, readmePrompt+osRules, user, "repo_info", schema, &out); err != nil {
		return nil, err
	}
	out.OSes, _ = oses.NormalizeList(out.OSes)
	return &out, nil
}
