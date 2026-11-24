package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/bot"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/config"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/forge"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/igdb"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/llm"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/web"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config.yaml")
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	level := zerolog.InfoLevel
	if *debug {
		level = zerolog.DebugLevel
	}
	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.DateTime}).
		Level(level).With().Timestamp().Logger()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal().Err(err).Msg("Invalid config")
	}
	st, err := store.Open(cfg.Database.Path)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to open database")
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var linker *igdb.Linker
	if cfg.IGDB.IsEnabled() {
		linker = igdb.NewLinker(igdb.New(cfg.IGDB.ClientID, cfg.IGDB.ClientSecret), st,
			log.With().Str("component", "igdb").Logger())
		go linker.Run(ctx)
	}

	b := bot.New(cfg, log, st,
		llm.New(cfg.OpenAI.APIKey, cfg.OpenAI.Model, cfg.OpenAI.BaseURL, cfg.OpenAI.ReasoningEffort),
		forge.NewClient(forgeHosts(cfg), cfg.Forges.AllowPrivateNetworks), linker)

	if cfg.Web.IsEnabled() {
		srv, err := web.New(st, log.With().Str("component", "web").Logger(), cfg.Web.Title)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to set up web server")
		}
		go func() {
			if err := srv.Run(ctx, cfg.Web.Listen); err != nil {
				log.Error().Err(err).Msg("Web server failed")
				stop()
			}
		}()
	}

	if err := b.Run(ctx); err != nil {
		log.Error().Err(err).Msg("Bot stopped with error")
		os.Exit(1)
	}
	log.Info().Msg("Shut down")
}

// forgeHosts merges the legacy github.token setting with forges.hosts.
func forgeHosts(cfg *config.Config) map[string]forge.Host {
	hosts := map[string]forge.Host{}
	if cfg.GitHub.Token != "" {
		hosts["github.com"] = forge.Host{Type: forge.GitHub, Token: cfg.GitHub.Token}
	}
	for _, h := range cfg.Forges.Hosts {
		hosts[strings.ToLower(h.Host)] = forge.Host{Type: h.Type, Token: h.Token}
	}
	return hosts
}
