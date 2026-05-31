package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"time"

	"flatradar/internal/model"
)

// Client — клиент Telegram Bot API: отправляет сообщения произвольным
// чатам и слушает команды (long polling). Без внешних зависимостей.
type Client struct {
	token  string
	client *http.Client
}

// New создаёт клиент.
func New(token string) *Client {
	return &Client{
		token:  token,
		client: &http.Client{Timeout: 40 * time.Second},
	}
}

func (c *Client) apiURL(method string) string {
	return fmt.Sprintf("https://api.telegram.org/bot%s/%s", c.token, method)
}

// SendText отправляет HTML-текст в указанный чат.
func (c *Client) SendText(ctx context.Context, chatID int64, text string) error {
	return c.sendMessage(ctx, chatID, text, nil)
}

// SendMenu отправляет текст с кнопочной панелью (reply keyboard).
func (c *Client) SendMenu(ctx context.Context, chatID int64, text string) error {
	keyboard := map[string]any{
		"keyboard": [][]map[string]string{
			{{"text": "📊 Статус"}, {"text": "🌐 Площадки"}},
			{{"text": "⏸ Пауза"}, {"text": "▶️ Возобновить"}},
			{{"text": "🆕 Последние"}, {"text": "❓ Помощь"}},
		},
		"resize_keyboard": true,
	}
	return c.sendMessage(ctx, chatID, text, keyboard)
}

// NotifyListing форматирует и отправляет объявление в указанный чат.
func (c *Client) NotifyListing(ctx context.Context, chatID int64, l model.Listing) error {
	return c.sendMessage(ctx, chatID, formatListing(l), nil)
}

// sendMessage — низкоуровневая отправка с опциональной клавиатурой.
func (c *Client) sendMessage(ctx context.Context, chatID int64, text string, replyMarkup any) error {
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": false,
	}
	if replyMarkup != nil {
		payload["reply_markup"] = replyMarkup
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL("sendMessage"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: отправка: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram: статус %d", resp.StatusCode)
	}
	return nil
}

// SetCommands регистрирует список команд — он появляется в меню «/».
func (c *Client) SetCommands(ctx context.Context) error {
	cmds := []map[string]string{
		{"command": "start", "description": "Подписаться и открыть панель"},
		{"command": "menu", "description": "Показать панель кнопок"},
		{"command": "status", "description": "Мои настройки и статус"},
		{"command": "sources", "description": "Подключённые площадки"},
		{"command": "last", "description": "Свежие объявления под мой фильтр"},
		{"command": "pause", "description": "Приостановить уведомления"},
		{"command": "resume", "description": "Возобновить уведомления"},
		{"command": "price", "description": "Изменить макс. цену, напр. /price 60000"},
		{"command": "help", "description": "Справка по командам"},
	}
	body, err := json.Marshal(map[string]any{"commands": cmds})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL("setMyCommands"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram: setMyCommands статус %d", resp.StatusCode)
	}
	return nil
}

func formatListing(l model.Listing) string {
	source := map[string]string{
		"onliner": "Onliner",
		"kufar":   "Kufar",
		"realt":   "Realt",
	}[l.Source]
	if source == "" {
		source = l.Source
	}

	return fmt.Sprintf(
		"🏠 <b>Новое объявление</b> (%s)\n"+
			"💵 %s $ · %d комн · %.0f м² · %s эт\n"+
			"📍 %s\n"+
			"🔗 %s",
		html.EscapeString(source),
		formatPrice(l.PriceUSD),
		l.Rooms,
		l.Area,
		html.EscapeString(l.Floor),
		html.EscapeString(l.Address),
		html.EscapeString(l.URL),
	)
}

// formatPrice разбивает число на разряды пробелом: 65000 -> "65 000".
func formatPrice(v int) string {
	s := fmt.Sprintf("%d", v)
	n := len(s)
	if n <= 3 {
		return s
	}
	var out []byte
	for i, ch := range []byte(s) {
		if i > 0 && (n-i)%3 == 0 {
			out = append(out, ' ')
		}
		out = append(out, ch)
	}
	return string(out)
}
