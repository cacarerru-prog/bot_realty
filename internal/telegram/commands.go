package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"flatradar/internal/state"
)

type update struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

type updatesResp struct {
	OK     bool     `json:"ok"`
	Result []update `json:"result"`
}

// ListenCommands слушает входящие сообщения (long polling) и выполняет
// команды управления. Блокируется до отмены ctx — запускать в горутине.
func (c *Client) ListenCommands(ctx context.Context, st *state.State, log func(string, ...any)) {
	var offset int64
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := c.getUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log("telegram: getUpdates: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}

		for _, u := range updates {
			offset = u.UpdateID + 1
			if u.Message == nil {
				continue
			}
			// Реагируем только на свой чат (защита от чужих).
			if u.Message.Chat.ID != c.chatID {
				continue
			}
			c.handle(ctx, st, strings.TrimSpace(u.Message.Text))
		}
	}
}

func (c *Client) getUpdates(ctx context.Context, offset int64) ([]update, error) {
	url := fmt.Sprintf("%s?offset=%d&timeout=30", c.apiURL("getUpdates"), offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var data updatesResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if !data.OK {
		return nil, fmt.Errorf("getUpdates: ok=false")
	}
	return data.Result, nil
}

// buttonToCommand сопоставляет подписи кнопок панели с командами.
var buttonToCommand = map[string]string{
	"📊 Статус":      "/status",
	"🌐 Площадки":    "/sources",
	"⏸ Пауза":       "/pause",
	"▶️ Возобновить": "/resume",
	"🆕 Последние":   "/last",
	"❓ Помощь":      "/help",
}

func (c *Client) handle(ctx context.Context, st *state.State, text string) {
	if text == "" {
		return
	}
	// Нажатие кнопки панели присылает её подпись — превращаем в команду.
	if mapped, ok := buttonToCommand[text]; ok {
		text = mapped
	}

	fields := strings.Fields(text)
	cmd := strings.ToLower(fields[0])

	// Панель с кнопками отправляем сразу, без текстового ответа.
	if cmd == "/start" || cmd == "/menu" {
		_ = c.SendMenu(ctx, "<b>FlatRadar</b> — панель управления.\nВыбери действие кнопкой ниже или command-меню «/».")
		return
	}

	var reply string
	switch cmd {
	case "/help":
		reply = helpText()

	case "/sources":
		reply = sourcesText(st)

	case "/status":
		s := st.Snapshot()
		status := "▶️ активен"
		if s.Paused {
			status = "⏸ на паузе"
		}
		reply = fmt.Sprintf(
			"<b>FlatRadar — статус</b>\n"+
				"Состояние: %s\n"+
				"Город: %s\n"+
				"Цена: %s–%s $\n"+
				"Отправлено за сессию: %d",
			status, s.City, formatPrice(s.PriceMin), formatPrice(s.PriceMax), s.Sent)

	case "/pause":
		st.SetPaused(true)
		reply = "⏸ Уведомления приостановлены. /resume — возобновить."

	case "/resume":
		st.SetPaused(false)
		reply = "▶️ Уведомления возобновлены."

	case "/price":
		if len(fields) < 2 {
			reply = "Укажи максимальную цену: <code>/price 60000</code>"
			break
		}
		v, err := strconv.Atoi(strings.ReplaceAll(fields[1], " ", ""))
		if err != nil || v < 0 {
			reply = "Не понял число. Пример: <code>/price 60000</code>"
			break
		}
		st.SetPriceMax(v)
		reply = fmt.Sprintf("✅ Максимальная цена теперь %s $.", formatPrice(v))

	case "/last":
		recent := st.Recent(5)
		if len(recent) == 0 {
			reply = "Пока ничего не найдено за эту сессию."
			break
		}
		var b strings.Builder
		b.WriteString("<b>Последние находки:</b>\n\n")
		for _, l := range recent {
			b.WriteString(formatListing(l))
			b.WriteString("\n\n")
		}
		reply = b.String()

	default:
		reply = "Не знаю такую команду. /help — список команд."
	}

	if err := c.SendText(ctx, reply); err != nil {
		// тихо игнорируем — не критично
		_ = err
	}
}

func helpText() string {
	return "<b>FlatRadar — команды</b>\n" +
		"/menu — панель с кнопками\n" +
		"/status — текущие настройки и статус\n" +
		"/sources — подключённые площадки\n" +
		"/pause — приостановить уведомления\n" +
		"/resume — возобновить уведомления\n" +
		"/price 60000 — изменить максимальную цену\n" +
		"/last — последние найденные объявления\n" +
		"/help — эта справка"
}

// sourcesText формирует список подключённых площадок с их статусом.
func sourcesText(st *state.State) string {
	var b strings.Builder
	b.WriteString("<b>🌐 Подключённые площадки</b>\n\n")
	for _, s := range st.Sources() {
		name := map[string]string{
			"onliner": "Onliner",
			"kufar":   "Kufar",
			"realt":   "Realt.by",
		}[s.Name]
		if name == "" {
			name = s.Name
		}

		switch {
		case s.LastTime.IsZero():
			b.WriteString(fmt.Sprintf("⏳ <b>%s</b> — ещё не опрашивалась\n", name))
		case s.LastError != "":
			b.WriteString(fmt.Sprintf("⚠️ <b>%s</b> — ошибка (%s)\n", name, s.LastTime.Format("15:04")))
		default:
			b.WriteString(fmt.Sprintf("✅ <b>%s</b> — в выдаче %d, обновлено %s\n",
				name, s.LastCount, s.LastTime.Format("15:04")))
		}
	}
	b.WriteString("\n⏳ <b>Realt.by</b> — в планах (этап 3)")
	return b.String()
}
