// Package app собирает все компоненты бота вместе и предоставляет
// единую точку запуска — её используют и headless-бот (cmd/bot),
// и трей-приложение (cmd/tray).
package app

import (
	"context"
	"errors"
	"log"
	"os"
	"sort"

	"flatradar/internal/collector"
	"flatradar/internal/config"
	"flatradar/internal/model"
	"flatradar/internal/scheduler"
	"flatradar/internal/state"
	"flatradar/internal/store"
	"flatradar/internal/telegram"
	"flatradar/internal/users"
)

// App — собранное приложение, готовое к запуску.
type App struct {
	cfg        config.Config
	tg         *telegram.Client
	users      *users.Manager
	state      *state.State
	store      *store.Store
	svc        *scheduler.Service
	collectors []collector.Collector
	log        *log.Logger
}

// New загружает конфиг, реестр пользователей и коллекторы.
func New(configPath string, logger *log.Logger) (*App, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}

	usrs, err := users.Open("users.json", users.Defaults{
		City:     cfg.City,
		PriceMin: cfg.PriceMin,
		PriceMax: cfg.PriceMax,
	})
	if err != nil {
		return nil, err
	}

	st := state.New()
	tg := telegram.New(cfg.TelegramToken)

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}

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
		Users:      usrs,
		State:      st,
		Notifier:   tg,
		Store:      db,
		Log:        logger,
		City:       cfg.City,
		BelowPct:   cfg.BelowMarketPct,
		MinSample:  cfg.MinSample,
		SkipWarmup: os.Getenv("FLATRADAR_NO_WARMUP") == "1",
	}

	return &App{
		cfg:        cfg,
		tg:         tg,
		users:      usrs,
		state:      st,
		store:      db,
		svc:        svc,
		collectors: collectors,
		log:        logger,
	}, nil
}

// Close освобождает ресурсы (БД). Вызывать после Run.
func (a *App) Close() {
	if a.store != nil {
		_ = a.store.Close()
	}
}

// Run запускает приём команд и опрос площадок. Блокируется до отмены ctx.
func (a *App) Run(ctx context.Context) {
	a.log.Printf("FlatRadar запущен. Город: %s, источники: %v, подписчиков: %d",
		a.cfg.City, a.cfg.Sources, a.users.Count())

	if err := a.tg.SetCommands(ctx); err != nil {
		a.log.Printf("не удалось установить меню команд: %v", err)
	}
	go a.tg.ListenCommands(ctx, telegram.Deps{
		Users:  a.users,
		State:  a.state,
		Latest: a.latest,
		Warmup: a.warmupUser,
		Log:    a.log.Printf,
	})

	// Фоновый полный скан рынка — для истории цен и отметки «ушедших».
	go a.svc.RunScanner(ctx, a.cfg.ScanInterval())

	a.svc.Run(ctx, a.cfg.PollInterval())
}

// fetchAll делает живой запрос ко всем площадкам (широкий фильтр города).
func (a *App) fetchAll(ctx context.Context) []model.Listing {
	f := collector.Filter{City: a.cfg.City}
	var all []model.Listing
	for _, c := range a.collectors {
		ls, err := c.Fetch(ctx, f)
		if err != nil {
			a.log.Printf("[%s] live-запрос: %v", c.Name(), err)
			continue
		}
		all = append(all, ls...)
	}
	return all
}

// latest возвращает до n свежих лотов под фильтр пользователя (для /last).
func (a *App) latest(ctx context.Context, chatID int64, n int) []model.Listing {
	u, ok := a.users.Get(chatID)
	if !ok {
		return nil
	}
	var matched []model.Listing
	for _, l := range a.fetchAll(ctx) {
		if !u.Matches(l) {
			continue
		}
		matched = append(matched, l)
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})
	if len(matched) > n {
		matched = matched[:n]
	}
	return matched
}

// warmupUser помечает текущие лоты показанными новому подписчику,
// чтобы он не получил лавину старых объявлений сразу после /start.
func (a *App) warmupUser(ctx context.Context, chatID int64) {
	for _, l := range a.fetchAll(ctx) {
		a.users.MarkSeen(chatID, l.Key())
	}
	a.users.Save()
}
