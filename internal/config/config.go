package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Config — настройки бота. Читается из config.json.
type Config struct {
	TelegramToken string   `json:"telegram_token"`
	ChatID        int64    `json:"chat_id"`
	PollSeconds   int      `json:"poll_interval_seconds"`
	City          string   `json:"city"`
	PriceMin      int      `json:"price_min"`
	PriceMax      int      `json:"price_max"`
	Sources       []string `json:"sources"`

	DBPath         string `json:"db_path"`               // файл SQLite с историей
	ScanMinutes    int    `json:"scan_interval_minutes"` // интервал полного скана рынка
	BelowMarketPct int    `json:"below_market_pct"`      // порог скидки к рынку для метки 🔥
	MinSample      int    `json:"min_sample"`            // минимум «ушедших» в сегменте
}

// PollInterval возвращает интервал опроса как time.Duration.
func (c Config) PollInterval() time.Duration {
	return time.Duration(c.PollSeconds) * time.Second
}

// ScanInterval возвращает интервал полного скана рынка.
func (c Config) ScanInterval() time.Duration {
	return time.Duration(c.ScanMinutes) * time.Minute
}

// Load читает конфиг из файла и подставляет значения по умолчанию.
func Load(path string) (Config, error) {
	c := Config{
		PollSeconds:    60,
		City:           "Минск",
		PriceMin:       0,
		PriceMax:       80000,
		Sources:        []string{"onliner"},
		DBPath:         "flatradar.db",
		ScanMinutes:    60,
		BelowMarketPct: 7,
		MinSample:      5,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return c, fmt.Errorf("не удалось прочитать %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("не удалось разобрать %s: %w", path, err)
	}

	if c.TelegramToken == "" {
		return c, fmt.Errorf("в конфиге не задан telegram_token")
	}
	// chat_id больше не обязателен: бот многопользовательский, подписчики
	// регистрируются сами командой /start.
	if c.PollSeconds <= 0 {
		c.PollSeconds = 60
	}
	if c.DBPath == "" {
		c.DBPath = "flatradar.db"
	}
	if c.ScanMinutes <= 0 {
		c.ScanMinutes = 60
	}
	if c.BelowMarketPct <= 0 {
		c.BelowMarketPct = 7
	}
	if c.MinSample <= 0 {
		c.MinSample = 5
	}
	return c, nil
}
