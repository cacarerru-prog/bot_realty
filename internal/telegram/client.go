package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
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
			{{"text": "🔍 Фильтры"}},
			{{"text": "📊 Статус"}, {"text": "🌐 Площадки"}},
			{{"text": "⏸ Пауза"}, {"text": "▶️ Возобновить"}},
			{{"text": "🆕 Последние"}, {"text": "❓ Помощь"}},
		},
		"resize_keyboard": true,
	}
	return c.sendMessage(ctx, chatID, text, keyboard)
}

// NotifyListing форматирует и отправляет объявление в указанный чат.
// Если у лота есть фото — шлём карточкой с картинкой, иначе текстом.
func (c *Client) NotifyListing(ctx context.Context, chatID int64, l model.Listing) error {
	text := formatListing(l)
	if l.Photo != "" {
		if err := c.sendPhoto(ctx, chatID, l.Photo, text); err == nil {
			return nil
		}
		// Фото не ушло (битая ссылка/таймаут) — отправим хотя бы текст.
	}
	return c.sendMessage(ctx, chatID, text, nil)
}

// sendPhoto отправляет фото с HTML-подписью.
func (c *Client) sendPhoto(ctx context.Context, chatID int64, photoURL, caption string) error {
	payload := map[string]any{
		"chat_id":    chatID,
		"photo":      photoURL,
		"caption":    caption,
		"parse_mode": "HTML",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL("sendPhoto"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: sendPhoto: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram: sendPhoto статус %d", resp.StatusCode)
	}
	return nil
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

// editMessage заменяет текст и инлайн-клавиатуру у ранее отправленного
// сообщения (для интерактивной панели фильтров).
func (c *Client) editMessage(ctx context.Context, chatID, messageID int64, text string, replyMarkup any) error {
	payload := map[string]any{
		"chat_id":                  chatID,
		"message_id":               messageID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if replyMarkup != nil {
		payload["reply_markup"] = replyMarkup
	}
	return c.post(ctx, "editMessageText", payload)
}

// answerCallback гасит «часики» на инлайн-кнопке после нажатия.
func (c *Client) answerCallback(ctx context.Context, callbackID, text string) error {
	payload := map[string]any{"callback_query_id": callbackID}
	if text != "" {
		payload["text"] = text
	}
	return c.post(ctx, "answerCallbackQuery", payload)
}

// post — общая отправка JSON-запроса к методу Bot API.
func (c *Client) post(ctx context.Context, method string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL(method), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: %s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram: %s статус %d", method, resp.StatusCode)
	}
	return nil
}

// SetCommands регистрирует список команд — он появляется в меню «/».
func (c *Client) SetCommands(ctx context.Context) error {
	cmds := []map[string]string{
		{"command": "start", "description": "Подписаться и открыть панель"},
		{"command": "menu", "description": "Показать панель кнопок"},
		{"command": "filters", "description": "Панель фильтров с кнопками"},
		{"command": "status", "description": "Мои настройки и статус"},
		{"command": "sources", "description": "Подключённые площадки"},
		{"command": "last", "description": "Свежие объявления под мой фильтр"},
		{"command": "pause", "description": "Приостановить уведомления"},
		{"command": "resume", "description": "Возобновить уведомления"},
		{"command": "price", "description": "Цена: /price 60000 или /price 30000 80000"},
		{"command": "rooms", "description": "Комнаты: /rooms 2 3"},
		{"command": "area", "description": "Площадь: /area 40 70"},
		{"command": "district", "description": "Район/адрес: /district Фрунзенский"},
		{"command": "floor", "description": "Исключить первый/последний этаж"},
		{"command": "reset", "description": "Сбросить все фильтры"},
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
	var b strings.Builder

	// Шапка: тип квартиры · площадь · этаж.
	head := []string{roomsLabel(l.Rooms)}
	if l.Area > 0 {
		head = append(head, fmt.Sprintf("%.0f м²", l.Area))
	}
	if l.Floor != "" {
		head = append(head, l.Floor+" этаж")
	}
	b.WriteString("🏠 <b>" + html.EscapeString(strings.Join(head, " · ")) + "</b>\n")

	// Цена + метка «ниже рынка».
	b.WriteString("💰 " + formatPrice(l.PriceUSD) + " $")
	if l.Stats != nil && l.Stats.BelowMarket > 0 {
		b.WriteString(fmt.Sprintf("  🔥 −%d%% к рынку", l.Stats.BelowMarket))
	}
	b.WriteString("\n")

	if l.Address != "" {
		b.WriteString("📍 " + html.EscapeString(l.Address) + "\n")
	}
	b.WriteString(fmt.Sprintf("🔗 <a href=\"%s\">%s</a>", html.EscapeString(l.URL), html.EscapeString(sourceName(l.Source))))

	if line := statsLine(l.Stats, l.PriceUSD); line != "" {
		b.WriteString("\n📊 " + line)
	}
	return b.String()
}

// sourceName — отображаемое имя площадки.
func sourceName(src string) string {
	if name := map[string]string{
		"onliner": "Onliner",
		"kufar":   "Kufar",
		"realt":   "Realt",
	}[src]; name != "" {
		return name
	}
	return src
}

// roomsLabel — «2-к квартира», «Студия» для 0.
func roomsLabel(rooms int) string {
	if rooms <= 0 {
		return "Студия"
	}
	return fmt.Sprintf("%d-к квартира", rooms)
}

// statsLine собирает строку 📊: дни на рынке, снижения цены, минимум.
func statsLine(st *model.Stats, curPrice int) string {
	if st == nil {
		return ""
	}
	var parts []string
	if st.DaysOnMarket <= 0 {
		parts = append(parts, "Сегодня на рынке")
	} else {
		parts = append(parts, fmt.Sprintf("На рынке %d %s", st.DaysOnMarket,
			plural(st.DaysOnMarket, "день", "дня", "дней")))
	}
	if st.PriceDrops > 0 {
		parts = append(parts, fmt.Sprintf("Цена снижена %d %s", st.PriceDrops,
			plural(st.PriceDrops, "раз", "раза", "раз")))
	}
	if st.MinPriceUSD > 0 && st.MinPriceUSD < curPrice {
		parts = append(parts, "Минимум: "+formatPrice(st.MinPriceUSD)+" $")
	}
	return strings.Join(parts, " | ")
}

// plural выбирает русскую форму слова по числу: 1 день, 2 дня, 5 дней.
func plural(n int, one, few, many string) string {
	n = n % 100
	if n >= 11 && n <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	default:
		return many
	}
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
