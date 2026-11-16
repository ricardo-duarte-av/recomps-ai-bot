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
}

// RepoInfo is what was extracted from a README.
type RepoInfo struct {
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

var osRules = `Canonical OS values: ` + strings.Join(oses.All, ", ") + `.
Map distributions and devices onto them: Steam Deck, SteamOS, Fedora, Ubuntu, Debian, Arch, Mint and any other Linux distro => linux; OSX, OS X, Mac, macOS => macos; Win/PC => windows; Nintendo Switch => switch; browser/WebAssembly => web.`

const intentPrompt = `You interpret chat messages sent to a Matrix bot that maintains a list of game recompilations ("recomps") and decompilations ("decomps").
Classify the message into exactly one intent:
- "add": the user asks to add a repository to the list.
- "remove": the user asks to remove a repository from the list.
- "query": the user asks whether there are recomps/decomps/ports for a game, or asks about the list.
- "unknown": anything else (greetings, unrelated questions, help requests).
Extract only what the user explicitly states; use "" or [] for anything not stated. Do not guess.
- repo_url: the repository URL if present.
- game: the original game's title, spelled out in full when the user uses a well-known abbreviation (e.g. "OoT" => "The Legend of Zelda: Ocarina of Time", "SM64" => "Super Mario 64").
- original_system: the console/platform the game originally ran on, if stated (e.g. "Nintendo 64", "Game Boy Advance", "PlayStation 2").
- kind: "recomp" or "decomp" if stated, otherwise "".
- oses: target operating systems the user mentions (for add: platforms the port supports; for query/remove: platforms the user filters on).
` // osRules appended at runtime

const readmePrompt = `You read the README (or, if no README was found, the web page) of a git repository that is a recompilation ("recomp", static recompilation / port of a game binary) or a decompilation ("decomp", matching or non-matching source reconstruction) of a video game.
Extract:
- game: the full official title of the original game (e.g. "Pokémon Emerald", "The Legend of Zelda: Majora's Mask").
- original_system: the original console/platform (e.g. "Game Boy Advance", "Nintendo 64", "Xbox 360"). Use full common names.
- kind: "recomp" if it is a recompilation/port produced by recompiling the original binary, "decomp" if it is a decompilation project.
- oses: platforms on which the resulting game can be run natively, as stated by the README (supported platforms, release downloads, build instructions). If the README only describes building the original ROM (a matching decomp) and no native port, return [].
If the README does not say, use "" or [].
` // osRules appended at runtime

const matchPrompt = `You match a user's game query against a list of known games. Each known game is given as "Title | Original system".
Return the exact titles (the part before " | ", copied character-for-character) that refer to the game the user asked about.
Account for accents, abbreviations, subtitles, numbering and regional names (e.g. "Pokemon" == "Pokémon", "OoT" == "Ocarina of Time", "Zelda 64" == "Ocarina of Time" and "Majora's Mask").
If the user names a broad series without a specific entry (e.g. "Zelda"), return every matching entry. If nothing matches, return [].`

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

// ParseIntent interprets a chat message (with the bot mention removed).
func (c *Client) ParseIntent(ctx context.Context, message string) (*Intent, error) {
	schema := objectSchema(map[string]any{
		"intent":          enumProp("What the user wants", "add", "remove", "query", "unknown"),
		"repo_url":        strProp("Repository URL, or empty"),
		"game":            strProp("Original game title, or empty"),
		"original_system": strProp("Original console/platform, or empty"),
		"kind":            enumProp("recomp, decomp, or empty if not stated", "recomp", "decomp", ""),
		"oses":            osesProp("Target operating systems mentioned"),
	})
	var out Intent
	if err := c.complete(ctx, intentPrompt+osRules, message, "intent", schema, &out); err != nil {
		return nil, err
	}
	out.OSes, _ = oses.NormalizeList(out.OSes)
	return &out, nil
}

// ExtractFromReadme determines game/system/kind/OSes from a README.
func (c *Client) ExtractFromReadme(ctx context.Context, repoURL, readme string) (*RepoInfo, error) {
	schema := objectSchema(map[string]any{
		"game":            strProp("Original game title"),
		"original_system": strProp("Original console/platform"),
		"kind":            enumProp("recomp or decomp", "recomp", "decomp", ""),
		"oses":            osesProp("Platforms the result runs on natively"),
	})
	var out RepoInfo
	user := "Repository: " + repoURL + "\n\nREADME:\n" + readme
	if err := c.complete(ctx, readmePrompt+osRules, user, "repo_info", schema, &out); err != nil {
		return nil, err
	}
	out.OSes, _ = oses.NormalizeList(out.OSes)
	return &out, nil
}

// MatchTitles returns the subset of known titles that the query refers to.
// known entries are formatted "Title | System".
func (c *Client) MatchTitles(ctx context.Context, query string, known []string) ([]string, error) {
	if len(known) == 0 {
		return nil, nil
	}
	schema := objectSchema(map[string]any{
		"titles": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	})
	var out struct {
		Titles []string `json:"titles"`
	}
	user := "User asked about: " + query + "\n\nKnown games:\n" + strings.Join(known, "\n")
	if err := c.complete(ctx, matchPrompt, user, "matches", schema, &out); err != nil {
		return nil, err
	}
	valid := map[string]bool{}
	for _, k := range known {
		title, _, _ := strings.Cut(k, " | ")
		valid[title] = true
	}
	var titles []string
	for _, t := range out.Titles {
		if valid[t] && !slices.Contains(titles, t) {
			titles = append(titles, t)
		}
	}
	return titles, nil
}
