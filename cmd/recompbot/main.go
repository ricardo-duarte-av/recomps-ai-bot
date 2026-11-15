package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/bot"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/config"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/github"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/llm"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
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

	b := bot.New(cfg, log, st,
		llm.New(cfg.OpenAI.APIKey, cfg.OpenAI.Model, cfg.OpenAI.BaseURL, cfg.OpenAI.ReasoningEffort),
		github.NewClient(cfg.GitHub.Token))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := b.Run(ctx); err != nil {
		log.Error().Err(err).Msg("Bot stopped with error")
		os.Exit(1)
	}
	log.Info().Msg("Shut down")
}
