# recomps-ai-bot

A Matrix bot that keeps a list of game **recomps** (static recompilations / native ports), **decomps** (decompilation projects) and **reimpls** (reimplementations), plus the **toolkits** used to make them (static recompilers, decompilation tooling and runtimes for a console, e.g. N64Recomp), and answers questions about it. It uses a lightweight OpenAI model (default `gpt-6-luna`) to understand messages and read repository READMEs. Works in encrypted and unencrypted rooms.

The bot only reacts when it's **explicitly mentioned** (or a message contains one of the optional `matrix.keywords`, see below), and only in the rooms listed in `config.yaml`. Replying to one of the bot's messages doesn't count as mentioning it, even though Matrix clients mark replies that way: a reply is only answered when its own text names the bot (e.g. `RecompBot: and for Mario?`). It answers with Matrix replies to the message it's answering; set `matrix.answer_with_reply: false` to send regular messages instead.

**Keywords:** `matrix.keywords` (e.g. `["!recomp"]`) lists words that make the bot act on a message without a mention, matched as whole words in any case. A message that **starts** with a keyword (`!recomp zelda`, `!recomp, count the decomps`) is addressed to the bot and handled exactly like a mention; a keyword alone gets the help text. A keyword elsewhere in a message may just be chat, so the bot only answers when it's a real request (a search, count, add, remove...); anything else gets no reply, no reactions, and is only checked for repository links as usual. Each keyword message costs one AI call to tell the two apart, so pick keywords that don't come up in ordinary conversation. Keywords are ignored in replies to the bot's own messages.

## Usage

Anyone:

```
@recompbot is there any recomp for Pokemon Emerald?
@recompbot any decomps for N64 games on macOS?
@recompbot which toolkits are there for PS3?
@recompbot how many entries support Linux?
@recompbot how many recomps from PS4?
@recompbot how many decomps per console?
```

Toolkits are left out of searches unless you ask for them; a search that matches some says how many.

Counting questions use the same filters as searches and answer with a number (plus a recomp/decomp/reimpl split); ask for a breakdown "per console", "by platform", "by type" or "per game" to get a ranked list.

Questions those filters can't express, such as comparisons, exclusions or combinations (`which games have both a recomp and a decomp?`, `what runs on Switch but not Windows?`, `are there more N64 decomps or recomps?`), are answered by letting the model look things up with two read-only tools: a search (filters, per-group counts, up to 50 entries) and a list of distinct values. It can make several lookups, up to 6 rounds, and then writes the answer itself.

**Safety:** the model never writes database queries. Simple questions only fill in a fixed set of filters (game titles and systems are checked against what's actually in the list), and the tools for harder questions only read an in-memory copy of the list entries (the same data the web page shows), with every argument validated. Nothing the model says can change the list or reach the rest of the database (pending prompts, encryption keys). Model-written answers are posted with no pings (any @room or user pill in them notifies nobody), and links that don't point to a repository in the list are removed.

Moderators (power level ≥ `moderator_level`, default 50):

```
@recompbot add this repo to the list https://github.com/owner/repo
@recompbot add this repo to the list https://github.com/owner/repo , a Super Mario 64 recomp for windows, linux and macos
@recompbot remove repo https://github.com/owner/repo from the list
```

Moderators can also attach free-form **extra info** to a repo that's already in the list, e.g. links to files it needs. Everything inside the code block is stored as-is (no AI involved) and shown in the web page's *Extra* column. Running it again replaces the text:

````
@recompbot add extra https://github.com/owner/repo
```
Needs the following files:
File1: https://example.org/file1
File2: https://example.org/file2
```
````

`@recompbot remove extra https://github.com/owner/repo` clears it. Asking `is https://github.com/owner/repo in the list?` also shows it.

Repositories can be on **any git host**: GitHub (and GitHub Enterprise), GitLab (gitlab.com or self-hosted, nested groups included), Gitea, Forgejo/Codeberg, SourceHut, cgit, and so on. Hosts the bot doesn't know are auto-detected; see `forges` in `config.yaml.sample` to set a host's type or an API token.

For **add**, the bot fetches the README, asks the model which game, original system, kind (recomp, decomp, reimpl, i.e. a reimplementation, or toolkit; toolkits have no game, only the system they work on) and supported OSes it is, and anything the moderator states in the message overrides that. It then posts:

> Adding https://github.com/owner/repo , a recomp of **Game** from System, for Windows, Linux and macOS.

with `OK` and `Cancel` reactions. Nothing is written until **a moderator** reacts with `OK` (✅/👍 also work); `Cancel` (❌/👎) discards it. **Remove** works the same way. Prompts expire after 24 hours. Once a moderator answers, the bot redacts all reactions on the prompt and reacts with the outcome instead (e.g. `Added by @mod:example.org`) rather than sending a message, so it needs permission to redact other users' events (its power level must be at least the room's `redact` level). Open link-detection prompts are pinned in the room so they're easy to find, and unpinned once answered (in any room) or expired; this needs the bot to be allowed to send `m.room.pinned_events` state events.

**Link detection:** the bot also watches every message in the allowed rooms (including notices from feed bots) for git repository links, such as a GitHub release page. It reduces each link to the main repo and skips repos already in the list (reacting to the message with the repo, e.g. `github.com/owner/repo`, instead). It then reads the README, and if the model says it's a game recomp/decomp/reimpl or a toolkit, the bot replies: While it checks a link the message gets ⏳, which is then replaced by a text reaction naming the outcome: `Captured` (suggested, see below), `Ignored` (not a game port or toolkit), `Missing data` (neither the README nor the repo description could be fetched), `AI fail` (the AI request failed) or `Missing info, help` (a game repo the bot couldn't fully describe; it also replies saying what's missing, with an example `add` request a moderator can complete). The last three are checked again the next time the link is posted after 24 hours.

> @mod1 @mod2: Detected a recomp not in the DB: https://github.com/owner/repo , **Game** from System, for Windows and Linux. Would you like to add it to the DB?

with the same `OK`/`Cancel` reactions. `OK` from a moderator adds it; `Cancel` dismisses it for good. If the same repo is posted in several rooms, the bot asks in each of them, and the first answer in any room applies everywhere (the other rooms' prompts get the same outcome reaction). While a question is open, posting the repo again in the same room is ignored. If no moderator answers within 6 hours of the latest question, the questions are dropped in all rooms and the repo is suggested again the next time anyone posts it (reusing the earlier classification, with no new AI call). Repos that are neither games nor toolkits (emulators, libraries, mods...) are remembered for 30 days so posting them again costs nothing. The suggestion ends with a line pinging that room's moderators, shown by their display names (joined members with at least `matrix.moderator_level`, looked up in each room when the suggestion is posted), so regular members aren't notified; set `detect.mention` to `room` for an `@room` ping instead, or `none` for no ping. Configure under `detect:` in `config.yaml`.

**Announcements:** set `matrix.announce_room` to a room ID or alias and the bot posts every new repo a moderator adds there, e.g. *New decompilation added to the DB: **sm64**, a decompilation of Super Mario 64 from Nintendo 64 for Windows and Linux - https://github.com/n64decomp/sm64 , by [Some Mod](https://matrix.to/#/%40mod%3Aexample.org)*. The room doesn't have to be in `allowed_rooms`: the bot only posts there and ignores commands. Updates to repos already in the list aren't announced.

**IGDB links:** with `igdb.client_id` and `igdb.client_secret` set in `config.yaml` (a Twitch application's credentials, see `config.yaml.sample`), game names and original systems link to their [IGDB](https://www.igdb.com) pages, on the web page and in announcements, e.g. *a decompilation of [Street Fighter Alpha Anthology](https://www.igdb.com/games/street-fighter-alpha-anthology) from [PlayStation 2](https://www.igdb.com/platforms/ps2)*. The game is looked up among IGDB's releases on that original system, so the same title on different systems gets the right entry; when the system isn't on IGDB, only an exact title match is linked. When IGDB has a picture, announcements are posted as that picture with the message as its caption: the game's cover (or the cover of the game it's an edition of), else official artwork, else a screenshot, all small sizes; toolkits get their system's logo. Without a picture, or if downloading or uploading it fails, the announcement is plain text as before. The web page shows that picture above each game's name. Links, IGDB game IDs and game pictures are cached in the database (pictures are served by the bot itself, so the page never calls IGDB): the bot links new entries as they're added and checks the whole list hourly, and games IGDB doesn't know, or that have no picture, are looked up again after a week.

**Daily release report:** set `releases.time` (e.g. `"20:00"`) and optionally `releases.timezone` (e.g. `"Europe/Lisbon"`, default UTC) and `releases.room` (default `matrix.announce_room`), and every day at that time the bot checks every repository in the list for releases published in the 24 hours before it and posts them as one report (split into several messages if it's very long), grouped by game, then repository, then release, newest first:

> **New releases in the last 24 hours:** 3 releases in 2 repositories
>
> **[Super Mario 64](https://www.igdb.com/games/super-mario-64)** (Nintendo 64)
> - https://github.com/owner/sm64-port (decomp)
>   - [v1.2](https://github.com/owner/sm64-port/releases/tag/v1.2) · Nov 26 14:03
>   - [v1.2-rc1](https://github.com/owner/sm64-port/releases/tag/v1.2-rc1) (pre-release) · Nov 26 09:12

Toolkits are listed after the games, per original system. Draft releases are left out; nothing is posted when there are no new releases. GitHub repositories are checked 50 at a time through GitHub's GraphQL API, so about 30 requests cover 1500 repositories; this needs `github.token`. GitLab and Gitea/Forgejo repositories are checked one by one through their APIs; other hosts (cgit, SourceHut...) have no releases API and are skipped. A full check takes a few minutes and logs its progress and a summary. A moderator can also mention the bot with `releases` to run the report right away for the last 24 hours.

Each supported target platform is stored as its own row. Targets can be operating systems or other consoles/handhelds (e.g. a SNES game recompiled for the Nintendo 3DS): `windows, linux, macos, freebsd, haiku, dos, android, ios, web, switch, 3ds, nds, wiiu, wii, gamecube, n64, gba, vita, psp, ps2, ps3, ps4, ps5, xbox, xbox360, xboxone, dreamcast`. Names are normalized, e.g. Steam Deck/Fedora/Ubuntu/Debian/Arch → `linux`, OSX/Mac → `macos`, New 3DS/2DS → `3ds`, Xbox Series X|S → `xboxone`.

## Web page

The bot also serves a read-only page (default `http://<host>:8080/`) listing every entry with its game, original system, type, supported OSes, URL and extra info (links in it are clickable). It reads the database on every load and refreshes itself every minute.

- A global search box searches every column (case- and accent-insensitive, so `pokemon` finds `Pokémon`); each column also has its own filter.
- Click a column header to sort. Filters and sort are kept in the URL, so a filtered view can be shared.
- No external libraries or CDNs: about 15 KB of HTML, CSS and JS, embedded in the binary. Light and dark mode follow the system setting.
- The JSON is at `/api/recomps`; `/healthz` returns `ok`.

Configure it under `web:` in `config.yaml` (`listen` address/port, `title`, or `enabled: false`), and keep the port in `docker-compose.yaml` in sync.

## Running with Docker Compose

```sh
cp config.yaml.sample config.yaml   # fill in Matrix, OpenAI (and optionally GitHub) settings
mkdir -p data && sudo chown 1000:1000 data   # the container runs as uid 1000
docker compose up -d
```

`./data/sqlite.db` holds the list, pending confirmations and the bot's E2EE keys. Back it up, and never change `pickle_key` once the bot has started, or it loses access to its encryption keys. The bot logs in with its password on startup and reuses the same device ID every time.

Images are built by GitHub Actions on every push and published to `ghcr.io/ricardo-duarte-av/recomps-ai-bot` (`latest` tracks `main`; branch and commit-SHA tags are also pushed).

## Development

```sh
go test -tags goolm ./...
RECOMPBOT_LIVE=1 go test -tags goolm -run Live ./internal/forge   # fetch READMEs from real forges
go run -tags goolm ./cmd/recompbot -config config.yaml -debug
```

`-tags goolm` uses mautrix's pure-Go Olm implementation, so libolm isn't needed. SQLite still requires CGO (a C compiler).
