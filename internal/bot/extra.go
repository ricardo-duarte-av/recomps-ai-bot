package bot

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/event"
)

// maxExtraLen caps the extra text stored per repository.
const maxExtraLen = 4000

// extraCmdRe matches "add extra <repo>" / "remove extra <repo>" (and a few synonyms).
var extraCmdRe = regexp.MustCompile(`(?is)^(add|set|update|edit|remove|clear|delete)\s+extras?\b(.*)$`)

var preBlockRe = regexp.MustCompile(`(?is)<pre[^>]*>\s*<code[^>]*>(.*?)</code>\s*</pre>`)

type extraCmd struct {
	clear bool
	head  string // text before the code block (holds the repo URL)
}

// parseExtraCommand recognises the extra-text commands without an AI call, since
// the code block content must be stored verbatim.
func parseExtraCommand(text string) (extraCmd, bool) {
	m := extraCmdRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return extraCmd{}, false
	}
	verb := strings.ToLower(m[1])
	head := m[2]
	if i := strings.Index(head, "```"); i >= 0 {
		head = head[:i]
	}
	return extraCmd{clear: verb == "remove" || verb == "clear" || verb == "delete", head: head}, true
}

// extractCodeBlock returns the content of the first ``` fenced block in body, or of
// the first <pre><code> block in the HTML body for clients that don't keep the fences.
func extractCodeBlock(body, formatted string) (string, bool) {
	if start := strings.Index(body, "```"); start >= 0 {
		rest := body[start+3:]
		// Skip an optional language tag on the opening fence line.
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		} else {
			rest = "" // "```" with nothing after it on any line
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			rest = rest[:end]
		}
		return strings.Trim(rest, "\r\n"), true
	}
	if m := preBlockRe.FindStringSubmatch(formatted); m != nil {
		return strings.Trim(html.UnescapeString(m[1]), "\r\n"), true
	}
	return "", false
}

func (b *Bot) handleExtra(ctx context.Context, evt *event.Event, cmd extraCmd) {
	log := zerolog.Ctx(ctx)
	if !b.isModerator(ctx, evt.RoomID, evt.Sender) {
		b.reply(ctx, evt, "Only moderators can change the extra info of an entry.")
		return
	}
	repo, ok := b.forge.FindRepo(cmd.head)
	if !ok {
		b.reply(ctx, evt, "Which repository? e.g.\n\n    add extra https://github.com/owner/repo\n    ```\n    File1: https://example.org/file1\n    ```")
		return
	}
	existing, err := b.store.GetByRepo(ctx, repo.URL())
	if err != nil {
		log.Err(err).Msg("Failed to look up repo")
		markFailed(ctx)
		b.reply(ctx, evt, "Database error, sorry.")
		return
	} else if existing == nil {
		b.reply(ctx, evt, fmt.Sprintf("%s isn't in the list. Add it first.", repo.URL()))
		return
	}

	var text string
	if !cmd.clear {
		content := evt.Content.AsMessage()
		var found bool
		text, found = extractCodeBlock(content.Body, content.FormattedBody)
		if !found || strings.TrimSpace(text) == "" {
			b.reply(ctx, evt, "Put the text to store in a code block below the command, between ``` lines.")
			return
		}
		if len(text) > maxExtraLen {
			b.reply(ctx, evt, fmt.Sprintf("That's too long (%d characters, the limit is %d).", len(text), maxExtraLen))
			return
		}
	}
	if err := b.store.SetExtra(ctx, existing.RepoURL, text, evt.Sender.String()); err != nil {
		log.Err(err).Msg("Failed to save extra text")
		markFailed(ctx)
		b.reply(ctx, evt, "Database error, nothing was changed.")
		return
	}
	log.Info().Str("repo", existing.RepoURL).Bool("cleared", cmd.clear).Msg("Updated extra text")
	if cmd.clear {
		b.reply(ctx, evt, fmt.Sprintf("Removed the extra info from %s (**%s**).", existing.RepoURL, existing.Game))
		return
	}
	verb := "Added"
	if existing.Extra != "" {
		verb = "Replaced"
	}
	b.reply(ctx, evt, fmt.Sprintf("%s the extra info for %s (**%s**).", verb, existing.RepoURL, existing.Game))
}

// formatExtra renders stored extra text as a code block for a chat reply.
func formatExtra(text string) string {
	return "\n\n```\n" + strings.ReplaceAll(text, "```", "'''") + "\n```"
}
