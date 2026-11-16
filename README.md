# recomps-ai-bot

A Matrix bot that keeps a list of game **recomps** (static recompilations / native ports) and **decomps** (decompilation projects), and answers questions about it. It uses a lightweight OpenAI model (default `gpt-5-mini`) to understand messages and read repository READMEs. Works in encrypted and unencrypted rooms.

The bot only reacts when it's **explicitly mentioned**, and only in the rooms listed in `config.yaml`.

## Usage

Anyone:

```
@recompbot is there any recomp for Pokemon Emerald?
@recompbot any decomps for N64 games on macOS?
```

Moderators (power level ≥ `moderator_level`, default 50):

```
@recompbot add this repo to the list https://github.com/owner/repo
@recompbot add this repo to the list https://github.com/owner/repo , a Super Mario 64 recomp for windows, linux and macos
@recompbot remove repo https://github.com/owner/repo from the list
```

Repositories can be on **any git host**: GitHub (and GitHub Enterprise), GitLab (gitlab.com or self-hosted, nested groups included), Gitea, Forgejo/Codeberg, SourceHut, cgit, and so on. Hosts the bot doesn't know are auto-detected; see `forges` in `config.yaml.sample` to set a host's type or an API token.

For **add**, the bot fetches the README, asks the model which game, original system, recomp/decomp and supported OSes it is, and anything the moderator states in the message overrides that. It then posts:

> Adding https://github.com/owner/repo , a recomp of **Game** from System, for Windows, Linux and macOS.

with `OK` and `Cancel` reactions. Nothing is written until **a moderator** reacts with `OK` (✅/👍 also work); `Cancel` (❌/👎) discards it. **Remove** works the same way. Prompts expire after 24 hours.

**Link detection:** the bot also watches every message in the allowed rooms (including notices from feed bots) for git repository links, such as a GitHub release page. It reduces each link to the main repo and skips repos already in the list. It then reads the README, and if the model says it's a game recomp/decomp, the bot replies:

> @room Detected a recomp not in the DB: https://github.com/owner/repo , **Game** from System, for Windows and Linux. Would you like to add it to the DB?

with the same `OK`/`Cancel` reactions. `OK` from a moderator adds it; `Cancel` dismisses it for good. Repos that aren't games (emulators, tools, libraries...) are remembered for 30 days so posting them again costs nothing. Configure under `detect:` in `config.yaml`.

Each supported target platform is stored as its own row. Targets can be operating systems or other consoles/handhelds (e.g. a SNES game recompiled for the Nintendo 3DS): `windows, linux, macos, freebsd, haiku, dos, android, ios, web, switch, 3ds, nds, wiiu, wii, gamecube, n64, gba, vita, psp, ps2, ps3, ps4, ps5, xbox, xbox360, xboxone, dreamcast`. Names are normalized, e.g. Steam Deck/Fedora/Ubuntu/Debian/Arch → `linux`, OSX/Mac → `macos`, New 3DS/2DS → `3ds`, Xbox Series X|S → `xboxone`.

## Web page

The bot also serves a read-only page (default `http://<host>:8080/`) listing every entry with its game, original system, type, supported OSes and URL. It reads the database on every load and refreshes itself every minute.

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
