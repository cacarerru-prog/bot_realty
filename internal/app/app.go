// Package app собирает все компоненты бота вместе и предоставляет
// единую точку запуска — её используют и headless-бот (cmd/bot),
// и трей-приложение (cmd/tray).
package app

import (
	"context"
	"errors"
	"log"
	"os"

	"flatradar/internal/collector"
	"flatradar/internal/config"
	"flatradar/internal/scheduler"
	"flatradar/internal/state"
	"flatradar/internal/storage"
	"flatradar/internal/telegram"
)

// App — собранное приложение, готовое к запуску.
type App struct {
	cfg   config.Config
	tg    *telegram.Client
	state *state.State
	svc   *scheduler.Service
	log   *log.Logger
}

// New загружает конфиг, хранилище и коллекторы и собирает приложение.
func New(configPath string, logger *log.Logger) (*App, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	store, err := storage.Open("seen.json")
	if err != nil {
		return nil, err
	}

	st := state.New(cfg.City, cfg.PriceMin, cfg.PriceMax)
	tg := telegram.New(cfg.TelegramToken, cfg.ChatID)

	var collectors []collector.Collector
	for _, src := range cfg.Sources {
		switch src {
		case "onliner":
			collectors = append(collectors, collector.NewOnliner())
		case "kufar":
			collectors = append(collectors, collector.NewKufar())
		default:
			logger.Printf("неизвестный источник в конфиге: %q (пропущен)", src)
		}
	}
	if len(collectors) == 0 {
		return nil, errors.New("не задано ни одного рабочего источника")
	}
	for _, c := range collectors {
		st.RegisterSource(c.Name())
	}

	svc := &scheduler.Service{
		Collectors: collectors,
		State:      st,
		Store:      store,
		Notifier:   tg,
		Log:        logger,
		SkipWarmup: os.Getenv("FLATRADAR_NO_WARMUP") == "1",
	}

	return &App{cfg: cfg, tg: tg, state: st, svc: svc, log: logger}, nil
}

// Run запускает приём команд и опрос площадок. Блокируется до отмены ctx.
func (a *App) Run(ctx context.Context) {
	a.log.Printf("FlatRadar запущен. Город: %s, цена %d–%d $, источники: %v",
		a.cfg.City, a.cfg.PriceMin, a.cfg.PriceMax, a.cfg.Sources)

	if err := a.tg.SetCommands(ctx); err != nil {
		a.log.Printf("не удалось установить меню команд: %v", err)
	}
	go a.tg.ListenCommands(ctx, a.state, a.log.Printf)

	a.svc.Run(ctx, a.cfg.PollInterval())
}
