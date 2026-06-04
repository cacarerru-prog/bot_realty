package main

import (
	"context"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"flatradar/internal/app"
)

func main() {
	// Логи пишем и в консоль, и в файл — чтобы при фоновом запуске
	// без окна история не терялась. Файл первым: при отсутствии консоли
	// запись в stdout падает, а MultiWriter останавливается на первом
	// упавшем writer.
	var out io.Writer = os.Stdout
	if f, err := os.OpenFile("flatradar.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		out = io.MultiWriter(f, os.Stdout)
	}
	logger := log.New(out, "", log.LstdFlags)

	configPath := os.Getenv("FLATRADAR_CONFIG")
	if configPath == "" {
		configPath = "config.json"
	}

	a, err := app.New(configPath, logger)
	if err != nil {
		logger.Fatalf("запуск: %v", err)
	}
	defer a.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a.Run(ctx)
}
