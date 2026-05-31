package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"flatradar/internal/model"
	"flatradar/internal/state"
	"flatradar/internal/users"
)

// LatestFunc возвращает до n свежих объявлений под фильтр пользователя.
type LatestFunc func(ctx context.Context, chatID int64, n int) []model.Listing

// WarmupFunc помечает текущие объявления показанными новому пользователю,
// чтобы он не получил лавину старых лотов сразу после подписки.
type WarmupFunc func(ctx context.Context, chatID int64)

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

// Deps — зависимости обработчика команд.
type Deps struct {
	Users  *users.Manager
	State  *state.State
	Latest LatestFunc
	Warmup WarmupFunc
	Log    func(string, ...any)
}

// ListenCommands слушает входящие сообщения (long polling) и выполняет
// команды управления. Блокируется до отмены ctx — запускать в горутине.
func (c *Client) ListenCommands(ctx context.Context, d Deps) {
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
			d.Log("telegram: getUpdates: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}

		for _, u := range updates {
			offset = u.UpdateID + 1
			if u.Message == nil {
				continue
			}
			c.handle(ctx, d, u.Message.Chat.ID, strings.TrimSpace(u.Message.Text))
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
	"📊 Статус":       "/status",
	"🌐 Площадки":     "/sources",
	"⏸ Пауза":        "/pause",
	"▶️ Возобновить": "/resume",
	"🆕 Последние":    "/last",
	"❓ Помощь":       "/help",
}

func (c *Client) handle(ctx context.Context, d Deps, chatID int64, text string) {
	if text == "" {
		return
	}
	// Любой написавший — авто-подписка; нового сразу «прогреваем».
	if d.Users.Ensure(chatID) && d.Warmup != nil {
		d.Warmup(ctx, chatID)
	}

	if mapped, ok := buttonToCommand[text]; ok {
		text = mapped
	}
	fields := strings.Fields(text)
	cmd := strings.ToLower(fields[0])

	if cmd == "/start" || cmd == "/menu" {
		_ = c.SendMenu(ctx, chatID, "<b>FlatRadar</b> — бот ищет новые квартиры в продаже.\n"+
			"Я подписал тебя на уведомления. Настрой цену командой <code>/price 60000</code> и жди новые лоты.\n"+
			"Управление — кнопками ниже или меню «/».")
		return
	}

	var reply string
	switch cmd {
	case "/help":
		reply = helpText()

	case "/sources":
		reply = sourcesText(d.State)

	case "/status":
		u, _ := d.Users.Get(chatID)
		status := "▶️ активен"
		if u.Paused {
			status = "⏸ на паузе"
		}
		reply = fmt.Sprintf(
			"<b>Мои настройки</b>\n"+
				"Состояние: %s\n"+
				"Город: %s\n"+
				"Цена: %s–%s $\n"+
				"Прислано: %d",
			status, u.City, formatPrice(u.PriceMin), formatPrice(u.PriceMax), u.Sent)

	case "/pause":
		d.Users.SetPaused(chatID, true)
		reply = "⏸ Уведомления приостановлены. /resume — возобновить."

	case "/resume":
		d.Users.SetPaused(chatID, false)
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
		d.Users.SetPriceMax(chatID, v)
		reply = fmt.Sprintf("✅ Максимальная цена теперь %s $.", formatPrice(v))

	case "/last":
		var listings []model.Listing
		if d.Latest != nil {
			listings = d.Latest(ctx, chatID, 5)
		}
		if len(listings) == 0 {
			reply = "Сейчас подходящих объявлений на площадках нет."
			break
		}
		var b strings.Builder
		b.WriteString("<b>🆕 Свежие объявления под твой фильтр:</b>\n\n")
		for _, l := range listings {
			b.WriteString(formatListing(l))
			b.WriteString("\n\n")
		}
		reply = b.String()

	default:
		reply = "Не знаю такую команду. /help — список команд."
	}

	if err := c.SendText(ctx, chatID, reply); err != nil {
		d.Log("telegram: ответ %d: %v", chatID, err)
	}
}

func helpText() string {
	return "<b>FlatRadar — команды</b>\n" +
		"/menu — панель с кнопками\n" +
		"/status — мои настройки и статус\n" +
		"/sources — подключённые площадки\n" +
		"/pause — приостановить уведомления\n" +
		"/resume — возобновить уведомления\n" +
		"/price 60000 — изменить максимальную цену\n" +
		"/last — свежие объявления под мой фильтр\n" +
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
