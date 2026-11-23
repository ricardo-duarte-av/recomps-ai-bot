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
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
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
	Intent         string   `json:"intent"` // add | remove | query | complex_query | unknown
	RepoURL        string   `json:"repo_url"`
	Game           string   `json:"game"`
	OriginalSystem string   `json:"original_system"`
	Kind           string   `json:"kind"` // recomp | decomp | reimpl | toolkit | "" (unspecified)
	OSes           []string `json:"oses"`

	// Search filters (query intent only), resolved against what's in the list.
	Titles  []string `json:"titles"`  // exact game titles in the list
	Systems []string `json:"systems"` // exact original systems in the list
	ListAll bool     `json:"list_all"`
	Count   bool     `json:"count"`    // "how many …?"
	GroupBy string   `json:"group_by"` // breakdown: kind | system | platform | game | ""
}

// GroupBy values.
var GroupBys = []string{"kind", "system", "platform", "game"}

// HasFilter reports whether a query narrows the list down in any way.
func (i *Intent) HasFilter() bool {
	return i.RepoURL != "" || i.Game != "" || i.OriginalSystem != "" || i.Kind != "" ||
		len(i.OSes) > 0 || len(i.Titles) > 0 || len(i.Systems) > 0
}

// RepoInfo is what was extracted from a README.
type RepoInfo struct {
	// InScope is false for repos that are neither a recomp/decomp/reimpl/port of a
	// specific game nor a recomp/decomp toolkit for a system (emulators, libraries,
	// mods, unrelated software).
	InScope        bool     `json:"in_scope"`
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

const intentPrompt = `You interpret chat messages sent to a Matrix bot that maintains a list of game recompilations ("recomps"), decompilations ("decomps"), reimplementations ("reimpls") and the toolkits used to make them (static recompilers, decompilation tooling and runtimes for a console, e.g. N64Recomp).
Classify the message into exactly one intent:
- "add": the user asks to add a repository to the list.
- "remove": the user asks to remove a repository from the list.
- "query": anything that asks about the list: whether a game, series, system, platform or repository is in it, what's available, what runs where. When in doubt and the message names a game, system, platform or repository, it's a query.
- "complex_query": a question about the list that the query fields below can't express on their own: comparisons ("are there more N64 decomps or recomps?"), set operations and exclusions ("games with both a recomp and a decomp", "what isn't on Windows?", "what runs on both Switch and Linux?"), superlatives that need several lookups, several questions in one message, or open-ended questions about what's in the list. A plain search, count or breakdown with the fields below is a "query", not a "complex_query".
- "unknown": only messages with nothing to look up (greetings, thanks, "what can you do?", unrelated chat).
Extract only what the user explicitly states; use "", [] or false for anything not stated. Do not guess.
- repo_url: the repository URL if present.
- game: the original game's title, spelled out in full when the user uses a well-known abbreviation (e.g. "OoT" => "The Legend of Zelda: Ocarina of Time", "SM64" => "Super Mario 64").
- original_system: the console/platform the game originally ran on, if stated (e.g. "Nintendo 64", "Game Boy Advance", "PlayStation 2").
- kind: "recomp", "decomp" or "reimpl" (reimplementation) if stated; "toolkit" if the user asks about or adds tools for making recomps/decomps rather than games (recompilers, toolkits, frameworks, runtimes, e.g. "which N64 recompilers are there?", "tools to recompile PS3 games"); otherwise "".
- oses: target platforms (operating systems or consoles) the user mentions (for add: platforms the port runs on; for query/remove: platforms the user wants to play on).
Distinguish the original system (where the game came FROM) from target platforms (where the port RUNS): "a SNES recomp for 3DS" => original_system "Super Nintendo Entertainment System", oses ["3ds"]. In queries, "games from N64" or "N64 recomps" means original_system; "can I play X on my 3DS/Vita/Steam Deck" means oses.
For queries only, you are given the games currently in the list as "Title | Original system" lines. A line titled "` + store.ToolkitTitle + `" means the list has toolkits for that system; it is not a game.
- titles: the exact titles (the part before " | ", copied character-for-character) of the games the user asks about, never "` + store.ToolkitTitle + `". Account for accents, abbreviations, subtitles, numbering and regional names ("Pokemon" == "Pokémon", "OoT" and "Zelda 64" == "Ocarina of Time"). A series name without a specific entry ("Zelda") matches every entry in the series. [] if the user didn't ask about specific games or none match.
- systems: the exact original systems (the part after " | ", copied character-for-character) the user asks about, e.g. "N64 decomps" => every spelling of Nintendo 64 in the list. [] if not asked or none match.
- list_all: true only if the user explicitly asks to see the whole list ("what's in the list?", "list everything").
- count: true if the user asks how many entries match rather than which ones ("how many decomps?", "how many recomps from PS4?", "how many entries support Linux?", "number of N64 games in the list"). Counting questions are queries; put their filters in the fields above as usual ("support Linux" => oses ["linux"], "from PS4" => original_system and systems).
- group_by: if the user asks for a breakdown or a ranking, what to break the results down by: "kind" (recomps vs decomps vs reimpls), "system" (original console, e.g. "per console", "which system has the most recomps?"), "platform" (target platforms, e.g. "by OS", "which platform is best supported?") or "game" ("which game has the most ports?"); otherwise "".
For add/remove, leave titles and systems empty, list_all and count false and group_by "".
` // osRules appended at runtime

const readmePrompt = `You read the README (or, if no README was found, the web page) of a git repository that is a recompilation ("recomp", static recompilation / port of a game binary), a decompilation ("decomp", matching or non-matching source reconstruction) or a reimplementation ("reimpl", new code rebuilt from reverse-engineered behaviour and formats) of a video game, or a toolkit used to make them ("toolkit").
Extract:
- game: the full official title of the original game (e.g. "Pokémon Emerald", "The Legend of Zelda: Majora's Mask"). "" for a toolkit.
- original_system: the original console/platform (e.g. "Game Boy Advance", "Nintendo 64", "Xbox 360"); for a toolkit, the console whose games it works on. Use full common names.
- kind: "recomp" if it is a recompilation/port produced by recompiling the original binary, "decomp" if it is a decompilation project, "reimpl" if it is a reimplementation: the game rebuilt as new code from reverse-engineered behaviour and file formats, without decompiling or recompiling the original binary (e.g. OpenMW, OpenRCT2); "toolkit" if it is not a port of one game but a static recompiler, decompilation tooling or runtime for making recomps/decomps of many games of one console (e.g. N64Recomp, ps3recomp, PS2Recomp, decomp-toolkit).
- oses: platforms (operating systems or consoles/handhelds) on which the resulting game (for a toolkit: the toolkit itself) runs natively, as stated by the README (supported platforms, release downloads, build instructions). If the README only describes building the original ROM (a matching decomp) and no native port, return [].
If the README does not say, use "" or [].
- in_scope: true only if this repository is a recompilation, decompilation, reimplementation or source port of one specific video game, or a toolkit as described above. False for emulators, general-purpose libraries, mods/randomizers/romhacks, launchers, game engines not tied to one game, and unrelated software.
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
		"intent":          enumProp("What the user wants", "add", "remove", "query", "complex_query", "unknown"),
		"repo_url":        strProp("Repository URL, or empty"),
		"game":            strProp("Original game title, or empty"),
		"original_system": strProp("Original console/platform, or empty"),
		"kind":            enumProp("recomp, decomp, reimpl, toolkit, or empty if not stated", append(slices.Clone(store.Kinds), "")...),
		"oses":            osesProp("Target platforms (OSes or consoles) mentioned"),
		"titles":          map[string]any{"type": "array", "description": "Query: exact known titles asked about", "items": map[string]any{"type": "string"}},
		"systems":         map[string]any{"type": "array", "description": "Query: exact known original systems asked about", "items": map[string]any{"type": "string"}},
		"list_all":        map[string]any{"type": "boolean", "description": "Query: user explicitly asks for the whole list"},
		"count":           map[string]any{"type": "boolean", "description": "Query: user asks how many entries match"},
		"group_by":        enumProp("Query: breakdown requested, or empty", append(slices.Clone(GroupBys), "")...),
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
		isTitle[title], isSystem[system] = title != store.ToolkitTitle, true
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
		"in_scope":        map[string]any{"type": "boolean", "description": "Whether this repository is a recomp/decomp/reimpl/source port of one specific game, or a recomp/decomp toolkit"},
		"game":            strProp("Original game title, empty for a toolkit"),
		"original_system": strProp("Original console/platform"),
		"kind":            enumProp("recomp, decomp, reimpl or toolkit", append(slices.Clone(store.Kinds), "")...),
		"oses":            osesProp("Platforms (OSes or consoles) the result runs on natively"),
	})
	var out RepoInfo
	user := "Repository: " + repoURL + "\n\nREADME:\n" + readme
	if err := c.complete(ctx, readmePrompt+osRules, user, "repo_info", schema, &out); err != nil {
		return nil, err
	}
	out.OSes, _ = oses.NormalizeList(out.OSes)
	if out.Kind == store.KindToolkit {
		out.Game = ""
	}
	return &out, nil
}
