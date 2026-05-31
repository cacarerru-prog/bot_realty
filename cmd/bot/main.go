package main

import (
	"context"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"flatradar/internal/collector"
	"flatradar/internal/config"
	"flatradar/internal/scheduler"
	"flatradar/internal/state"
	"flatradar/internal/storage"
	"flatradar/internal/telegram"
)

func main() {
	// Логи пишем и в консоль, и в файл — чтобы при фоновом запуске
	// без окна (Планировщик задач) история не терялась.
	// Файл — первым в MultiWriter: при фоновом запуске без консоли запись
	// в stdout возвращает ошибку, а MultiWriter останавливается на первом
	// упавшем writer. Поэтому файл должен идти раньше stdout.
	var out io.Writer = os.Stdout
	if f, err := os.OpenFile("flatradar.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		out = io.MultiWriter(f, os.Stdout)
	}
	logger := log.New(out, "", log.LstdFlags)

	// Путь к конфигу можно переопределить переменной окружения.
	configPath := os.Getenv("FLATRADAR_CONFIG")
	if configPath == "" {
		configPath = "config.json"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Fatalf("конфиг: %v", err)
	}

	store, err := storage.Open("seen.json")
	if err != nil {
		logger.Fatalf("хранилище: %v", err)
	}

	st := state.New(cfg.City, cfg.PriceMin, cfg.PriceMax)
	tg := telegram.New(cfg.TelegramToken, cfg.ChatID)

	// Регистрируем коллекторы согласно cfg.Sources.
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
		logger.Fatalf("не задано ни одного рабочего источника")
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
	}

	// Корректная остановка по Ctrl+C / SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Регистрируем меню команд (кнопка «/» в чате).
	if err := tg.SetCommands(ctx); err != nil {
		logger.Printf("не удалось установить меню команд: %v", err)
	}

	// Слушатель команд из чата — параллельно опросу площадок.
	go tg.ListenCommands(ctx, st, logger.Printf)

	logger.Printf("FlatRadar запущен. Город: %s, цена до %d $, источники: %v",
		cfg.City, cfg.PriceMax, cfg.Sources)
	svc.Run(ctx, cfg.PollInterval())
}
