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
}

// PollInterval возвращает интервал опроса как time.Duration.
func (c Config) PollInterval() time.Duration {
	return time.Duration(c.PollSeconds) * time.Second
}

// Load читает конфиг из файла и подставляет значения по умолчанию.
func Load(path string) (Config, error) {
	c := Config{
		PollSeconds: 60,
		City:        "Минск",
		PriceMin:    0,
		PriceMax:    80000,
		Sources:     []string{"onliner"},
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
	return c, nil
}
