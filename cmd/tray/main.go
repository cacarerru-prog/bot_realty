// Команда tray — десктоп-приложение FlatRadar с иконкой в системном трее.
// Двойной клик по exe запускает бота; управление — через меню иконки.
package main

import (
	"context"
	_ "embed"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/getlantern/systray"

	"flatradar/internal/app"
)

//go:embed icon.ico
var iconData []byte

var (
	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
	logger  *log.Logger
	logPath string
)

func main() {
	// Работаем относительно папки с exe (config.json, seen.json, лог рядом).
	if exe, err := os.Executable(); err == nil {
		_ = os.Chdir(filepath.Dir(exe))
	}

	var out io.Writer = io.Discard
	if f, err := os.OpenFile("flatradar.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		out = f
	}
	logger = log.New(out, "", log.LstdFlags)
	logPath, _ = filepath.Abs("flatradar.log")

	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(iconData)
	systray.SetTitle("FlatRadar")
	systray.SetTooltip("FlatRadar — мониторинг квартир")

	mToggle := systray.AddMenuItem("⏸ Остановить", "Запустить или остановить бота")
	mLog := systray.AddMenuItem("Открыть лог", "Показать flatradar.log")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Выход", "Остановить бота и закрыть")

	// Автостарт при открытии приложения.
	startBot()
	refreshToggle(mToggle)

	go func() {
		for {
			select {
			case <-mToggle.ClickedCh:
				mu.Lock()
				r := running
				mu.Unlock()
				if r {
					stopBot()
				} else {
					startBot()
				}
				refreshToggle(mToggle)

			case <-mLog.ClickedCh:
				_ = exec.Command("notepad.exe", logPath).Start()

			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func onExit() {
	stopBot()
}

func startBot() {
	mu.Lock()
	defer mu.Unlock()
	if running {
		return
	}
	a, err := app.New("config.json", logger)
	if err != nil {
		logger.Printf("не удалось запустить: %v", err)
		systray.SetTooltip("FlatRadar — ошибка конфигурации")
		return
	}
	ctx, c := context.WithCancel(context.Background())
	cancel = c
	running = true
	go a.Run(ctx)
	systray.SetTooltip("FlatRadar — работает")
}

func stopBot() {
	mu.Lock()
	defer mu.Unlock()
	if !running {
		return
	}
	if cancel != nil {
		cancel()
	}
	running = false
	systray.SetTooltip("FlatRadar — остановлен")
}

func refreshToggle(m *systray.MenuItem) {
	mu.Lock()
	r := running
	mu.Unlock()
	if r {
		m.SetTitle("⏸ Остановить")
	} else {
		m.SetTitle("▶ Запустить")
	}
}
