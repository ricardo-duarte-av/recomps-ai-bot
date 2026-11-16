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

> Adding https://github.com/owner/repo , a recomp of **Game** for System, for Windows, Linux and macOS.

with `OK` and `Cancel` reactions. Nothing is written until **a moderator** reacts with `OK` (✅/👍 also work); `Cancel` (❌/👎) discards it. **Remove** works the same way. Prompts expire after 24 hours.

Each supported OS is stored as its own row. OS names are normalized to `windows, linux, macos, android, ios, switch, web, freebsd` — e.g. Steam Deck/Fedora/Ubuntu/Debian/Arch → `linux`, OSX/Mac → `macos`.

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
