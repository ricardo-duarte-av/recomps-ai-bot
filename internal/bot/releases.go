package bot

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/forge"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

// releaseWindow is how far back a release report looks.
const releaseWindow = 24 * time.Hour

// maxReportBytes keeps each report message well under Matrix's 64 KiB event
// limit (the HTML copy roughly doubles the text); longer reports are split
// between games.
const maxReportBytes = 20000

// nextReport returns the first report time after now: today's or tomorrow's
// hour:minute in loc.
func nextReport(now time.Time, hour, minute int, loc *time.Location) time.Time {
	local := now.In(loc)
	t := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if !t.After(now) {
		t = time.Date(local.Year(), local.Month(), local.Day()+1, hour, minute, 0, 0, loc)
	}
	return t
}

// releaseLoop posts the release report every day at releases.time until ctx is cancelled.
func (b *Bot) releaseLoop(ctx context.Context) {
	cfg := b.cfg.Releases
	log := b.log.With().Str("component", "releases").Logger()
	for {
		at := nextReport(time.Now(), cfg.Hour, cfg.Minute, cfg.Location)
		log.Info().Time("next_report", at).Msg("Release report scheduled")
		t := time.NewTimer(time.Until(at))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		b.runReleaseReport(log.WithContext(ctx), at)
	}
}

// runReleaseReport checks every listed repo for releases published in the 24
// hours before until and posts them to the releases room. Only one report runs
// at a time; it returns false if one was already running.
func (b *Bot) runReleaseReport(ctx context.Context, until time.Time) bool {
	if !b.reportRunning.CompareAndSwap(false, true) {
		return false
	}
	defer b.reportRunning.Store(false)
	log := zerolog.Ctx(ctx)
	since := until.Add(-releaseWindow)
	start := time.Now()

	entries, err := b.store.All(ctx)
	if err != nil {
		log.Err(err).Msg("Failed to load entries for the release report")
		return true
	}
	byURL := map[string]store.Entry{}
	var repos []forge.Repo
	for _, e := range entries {
		r, err := b.forge.ParseRepo(e.RepoURL)
		if err != nil {
			log.Warn().Err(err).Str("repo", e.RepoURL).Msg("Skipping unparsable repo in release report")
			continue
		}
		byURL[r.URL()] = e
		repos = append(repos, r)
	}
	log.Info().Int("repos", len(repos)).Time("since", since).Time("until", until).Msg("Checking repositories for new releases")

	lastLog := time.Now()
	results := b.forge.Releases(ctx, repos, since, until, func(done int) {
		if time.Since(lastLog) >= 30*time.Second {
			lastLog = time.Now()
			log.Info().Int("done", done).Int("repos", len(repos)).Msg("Release check progress")
		}
	})

	var found []repoReleases
	failed, releases := 0, 0
	for _, res := range results {
		if res.Err != nil {
			failed++
			log.Warn().Err(res.Err).Str("repo", res.Repo.URL()).Msg("Couldn't check repo for releases")
			continue
		}
		if len(res.Releases) > 0 {
			found = append(found, repoReleases{Entry: byURL[res.Repo.URL()], Releases: res.Releases})
			releases += len(res.Releases)
		}
	}
	log.Info().Int("repos", len(repos)).Int("with_releases", len(found)).Int("releases", releases).Int("failed", failed).
		Str("took", time.Since(start).Round(time.Second).String()).Msg("Release check finished")
	if ctx.Err() != nil {
		return true
	}
	if len(found) == 0 {
		log.Info().Msg("No new releases; nothing to post")
		return true
	}

	var links map[string]store.IGDBLink
	if b.igdb != nil {
		if links, err = b.store.IGDBLinks(ctx); err != nil {
			log.Warn().Err(err).Msg("Failed to load IGDB links for the release report")
		}
	}
	msgs := releaseReport(found, links, b.cfg.Releases.Location)
	for i, md := range msgs {
		content := format.RenderMarkdown(md, true, false)
		content.MsgType = event.MsgNotice
		content.Mentions = &event.Mentions{} // a report pings nobody
		if _, err := b.client.SendMessageEvent(ctx, b.releasesRoom, event.EventMessage, &content); err != nil {
			log.Err(err).Int("part", i+1).Msg("Failed to post release report")
			return true
		}
	}
	log.Info().Int("messages", len(msgs)).Stringer("room", b.releasesRoom).Msg("Posted release report")
	return true
}

// repoReleases is a listed repo with its new releases.
type repoReleases struct {
	Entry    store.Entry
	Releases []forge.Release
}

// releaseReport renders the report as markdown messages: one section per game
// (toolkits per system), then each repo, then its releases, newest first.
// Sections are never split between messages.
func releaseReport(found []repoReleases, links map[string]store.IGDBLink, loc *time.Location) []string {
	type group struct {
		title   string
		repos   []repoReleases
		sortKey string
	}
	groups := map[string]*group{}
	total := 0
	for _, f := range found {
		e := f.Entry
		key, title := "g\x00"+e.Game+"\x00"+e.OriginalSystem, ""
		if e.Kind == store.KindToolkit {
			key = "t\x00" + e.OriginalSystem
			title = "Toolkits for " + mdLink(e.OriginalSystem, links[store.IGDBSystemKey(e.OriginalSystem)].URL)
		} else {
			title = "**" + mdLink(e.Game, links[store.IGDBGameKey(e.Game, e.OriginalSystem)].URL) + "** (" + escapeLinkText(e.OriginalSystem) + ")"
		}
		g := groups[key]
		if g == nil {
			// Toolkits after the games.
			g = &group{title: title, sortKey: strings.ToLower(e.Game) + "\x00" + strings.ToLower(e.OriginalSystem)}
			if e.Kind == store.KindToolkit {
				g.sortKey = "\xff" + strings.ToLower(e.OriginalSystem)
			}
			groups[key] = g
		}
		g.repos = append(g.repos, f)
		total += len(f.Releases)
	}
	sorted := make([]*group, 0, len(groups))
	for _, g := range groups {
		sort.Slice(g.repos, func(i, j int) bool { return g.repos[i].Entry.RepoURL < g.repos[j].Entry.RepoURL })
		sorted = append(sorted, g)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].sortKey < sorted[j].sortKey })

	header := fmt.Sprintf("**New releases in the last 24 hours:** %s in %s",
		plural(total, "release", "releases"), plural(len(found), "repository", "repositories"))
	var msgs []string
	var cur strings.Builder
	cur.WriteString(header + "\n")
	for _, g := range sorted {
		var sec strings.Builder
		sec.WriteString("\n" + g.title + "\n")
		for _, f := range g.repos {
			fmt.Fprintf(&sec, "- %s (%s)\n", f.Entry.RepoURL, f.Entry.Kind)
			for _, r := range f.Releases {
				sec.WriteString("  - " + releaseLine(r, loc) + "\n")
			}
		}
		if cur.Len()+sec.Len() > maxReportBytes && cur.Len() > 0 {
			msgs = append(msgs, strings.TrimSpace(cur.String()))
			cur.Reset()
			cur.WriteString("**New releases (continued)**\n")
		}
		cur.WriteString(sec.String())
	}
	return append(msgs, strings.TrimSpace(cur.String()))
}

// releaseLine is one release: its tag linked to the release page, its name when
// it says more than the tag, a pre-release note and the local publication time.
func releaseLine(r forge.Release, loc *time.Location) string {
	line := mdLink(r.Tag, r.URL)
	if r.Tag == "" {
		line = mdLink(r.Name, r.URL)
	} else if name := strings.TrimSpace(r.Name); name != "" && name != r.Tag && !strings.Contains(name, r.Tag) {
		line += " " + escapeLinkText(name)
	} else if name != "" && name != r.Tag {
		line = mdLink(name, r.URL)
	}
	if r.Prerelease {
		line += " (pre-release)"
	}
	return line + " · " + r.Published.In(loc).Format("Jan 2 15:04")
}

// releasesCommand reports whether text asks for the release report now.
func releasesCommand(text string) bool {
	t := strings.ToLower(strings.Trim(strings.TrimSpace(text), "!?."))
	return t == "releases" || t == "check releases" || t == "release report"
}

// handleReleasesCommand runs the release report now, for the last 24 hours, so
// moderators can try it without waiting for releases.time.
func (b *Bot) handleReleasesCommand(ctx context.Context, evt *event.Event) {
	if b.releasesRoom == "" {
		b.reply(ctx, evt, "The release report isn't configured (set `releases.time` in config.yaml).")
		return
	}
	if !b.isModerator(ctx, evt.RoomID, evt.Sender) {
		b.reply(ctx, evt, "Only moderators can run the release report.")
		return
	}
	log := zerolog.Ctx(ctx)
	log.Info().Msg("Release report requested")
	if b.reportRunning.Load() {
		b.reply(ctx, evt, "A release report is already running.")
		return
	}
	b.reply(ctx, evt, "Checking every repository for releases from the last 24 hours; the report will be posted when it's done (this takes a few minutes).")
	// A full check outlasts a command's time limit, so it runs on its own.
	go func() {
		ctx, cancel := context.WithTimeout(log.With().Str("component", "releases").Logger().WithContext(context.Background()), time.Hour)
		defer cancel()
		b.runReleaseReport(ctx, time.Now())
	}()
}
