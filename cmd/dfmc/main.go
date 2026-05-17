package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/dontfuckmycode/dfmc/internal/bot"
	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/ui/cli"
)

var version = "dev"

func main() {
	// Single os.Exit at the top of the call stack so every defer inside
	// run() fires, including signal cancellation and engine shutdown.
	os.Exit(run())
}

func run() int {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	startup := parseStartupArgs(os.Args[1:])
	loadOpts := config.LoadOptions{}
	if startup.dataDir != "" {
		loadOpts.DataDirPath = startup.dataDir
	}

	cfg, err := config.LoadWithOptions(loadOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 1
	}

	if startup.telegramToken != "" {
		cfg.Telegram.Token = startup.telegramToken
	}
	if startup.sessionName != "" {
		cfg.Telegram.SessionName = startup.sessionName
	}

	tgBot, err := startTelegramBot(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "telegram init error: %v\n", err)
		return 1
	}

	if !checkHookConfigPermissions() {
		return 1
	}
	autoInitProjectState(cfg)

	eng, err := engine.NewWithVersion(cfg, version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "engine init error: %v\n", err)
		return 1
	}

	var tgStopFunc func()
	defer func() {
		_ = eng.Shutdown()
		cancel()
		if tgStopFunc != nil {
			tgStopFunc()
		}
	}()

	if tgBot != nil {
		tgStopFunc = wireTelegramBot(ctx, eng, tgBot, cfg)
	}

	if err := eng.Init(ctx); err != nil {
		if !allowsDegradedStartup(os.Args[1:]) {
			fmt.Fprintf(os.Stderr, "init error: %s\n", formatInitError(err))
			return 1
		}
		if !suppressInitWarning(os.Args[1:]) {
			fmt.Fprintf(os.Stderr, "init warning: %s\n", formatInitError(err))
		}
	}
	return cli.Run(ctx, eng, os.Args[1:], version)
}

func startTelegramBot(cfg *config.Config) (*bot.TelegramBot, error) {
	if cfg == nil || !cfg.Telegram.Enabled || cfg.Telegram.Token == "" {
		return nil, nil
	}
	tgBot, err := bot.New(cfg.Telegram.Token)
	if err != nil {
		return nil, err
	}
	log.Printf("[telegram] bot started (session: %s)", cfg.Telegram.SessionName)
	return tgBot, nil
}

func wireTelegramBot(ctx context.Context, eng *engine.Engine, tgBot *bot.TelegramBot, cfg *config.Config) func() {
	eng.SetTelegramBot(tgBot, cfg.Telegram.SessionName, cfg.Telegram.AllowedUsers)
	tgBot.SetOnMessage(func(userID int64, text string, replyFn func(string)) {
		go func() {
			resp, err := eng.Ask(ctx, text)
			if err != nil {
				log.Printf("[telegram] ask error: %v", err)
				replyFn("DFMC error: " + err.Error())
				return
			}
			if len(resp) > 4000 {
				resp = resp[:3997] + "..."
			}
			replyFn(resp)
		}()
	})
	go tgBot.Start()
	return func() { tgBot.Stop() }
}
