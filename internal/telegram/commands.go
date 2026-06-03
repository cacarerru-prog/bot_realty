package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
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
				"Цена: %s\n"+
				"Комнаты: %s\n"+
				"Площадь: %s\n"+
				"Район/адрес: %s\n"+
				"Этаж: %s\n"+
				"Прислано: %d",
			status, u.City,
			formatPriceRange(u.PriceMin, u.PriceMax),
			formatRooms(u.Rooms),
			formatAreaRange(u.AreaMin, u.AreaMax),
			orAny(u.District),
			formatFloorFilter(u.NoEdge),
			u.Sent)

	case "/pause":
		d.Users.SetPaused(chatID, true)
		reply = "⏸ Уведомления приостановлены. /resume — возобновить."

	case "/resume":
		d.Users.SetPaused(chatID, false)
		reply = "▶️ Уведомления возобновлены."

	case "/price":
		reply = handlePrice(d, chatID, fields)

	case "/rooms":
		reply = handleRooms(d, chatID, fields)

	case "/area":
		reply = handleArea(d, chatID, fields)

	case "/district":
		reply = handleDistrict(d, chatID, fields)

	case "/floor":
		u, _ := d.Users.Get(chatID)
		on := !u.NoEdge
		d.Users.SetNoEdge(chatID, on)
		if on {
			reply = "✅ Первый и последний этаж теперь исключены. /floor — выключить."
		} else {
			reply = "✅ Этаж снова без ограничений."
		}

	case "/reset":
		d.Users.ResetFilters(chatID)
		reply = "♻️ Фильтры сброшены к настройкам по умолчанию. /status — проверить."

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
		"/last — свежие объявления под мой фильтр\n" +
		"/pause — приостановить уведомления\n" +
		"/resume — возобновить уведомления\n\n" +
		"<b>Фильтры</b>\n" +
		"/price 60000 — макс. цена (или /price 30000 80000 — диапазон)\n" +
		"/rooms 2 3 — нужное число комнат (/rooms — любое)\n" +
		"/area 40 70 — площадь, м² (/area — любая)\n" +
		"/district Фрунзенский — район/улица/ЖК в адресе (/district - — снять)\n" +
		"/floor — исключить первый и последний этаж (повторно — вернуть)\n" +
		"/reset — сбросить все фильтры\n\n" +
		"/help — эта справка"
}

// isClear распознаёт слова сброса фильтра.
func isClear(s string) bool {
	switch strings.ToLower(s) {
	case "-", "0", "любой", "любая", "любые", "любое", "сброс", "any", "off", "нет":
		return true
	}
	return false
}

func handlePrice(d Deps, chatID int64, fields []string) string {
	if len(fields) < 2 {
		return "Укажи цену: <code>/price 60000</code> (макс) или <code>/price 30000 80000</code> (диапазон)."
	}
	clean := func(s string) string { return strings.ReplaceAll(s, " ", "") }
	a, err := strconv.Atoi(clean(fields[1]))
	if err != nil || a < 0 {
		return "Не понял число. Пример: <code>/price 60000</code>"
	}
	// Один аргумент — это максимальная цена, нижнюю границу сохраняем.
	if len(fields) == 2 {
		u, _ := d.Users.Get(chatID)
		d.Users.SetPriceRange(chatID, u.PriceMin, a)
		return fmt.Sprintf("✅ Цена: %s.", formatPriceRange(u.PriceMin, a))
	}
	b, err := strconv.Atoi(clean(fields[2]))
	if err != nil || b < 0 {
		return "Не понял число. Пример: <code>/price 30000 80000</code>"
	}
	if a > b {
		a, b = b, a
	}
	d.Users.SetPriceRange(chatID, a, b)
	return fmt.Sprintf("✅ Цена: %s.", formatPriceRange(a, b))
}

func handleRooms(d Deps, chatID int64, fields []string) string {
	if len(fields) < 2 || isClear(fields[1]) {
		d.Users.SetRooms(chatID, nil)
		return "✅ Фильтр по комнатам снят (любое число)."
	}
	var rooms []int
	seen := map[int]bool{}
	for _, f := range fields[1:] {
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > 9 {
			return "Укажи число комнат от 1 до 9, напр. <code>/rooms 2 3</code>."
		}
		if !seen[n] {
			seen[n] = true
			rooms = append(rooms, n)
		}
	}
	d.Users.SetRooms(chatID, rooms)
	return "✅ Комнаты: " + formatRooms(rooms) + "."
}

func handleArea(d Deps, chatID int64, fields []string) string {
	if len(fields) < 2 || isClear(fields[1]) {
		d.Users.SetArea(chatID, 0, 0)
		return "✅ Фильтр по площади снят (любая)."
	}
	min, err := strconv.ParseFloat(strings.ReplaceAll(fields[1], ",", "."), 64)
	if err != nil || min < 0 {
		return "Не понял число. Пример: <code>/area 40 70</code> или <code>/area 40</code> (от 40 м²)."
	}
	var max float64
	if len(fields) >= 3 {
		max, err = strconv.ParseFloat(strings.ReplaceAll(fields[2], ",", "."), 64)
		if err != nil || max < 0 {
			return "Не понял число. Пример: <code>/area 40 70</code>."
		}
	}
	if max > 0 && min > max {
		min, max = max, min
	}
	d.Users.SetArea(chatID, min, max)
	return "✅ Площадь: " + formatAreaRange(min, max) + "."
}

func handleDistrict(d Deps, chatID int64, fields []string) string {
	if len(fields) < 2 || isClear(fields[1]) {
		d.Users.SetDistrict(chatID, "")
		return "✅ Фильтр по району/адресу снят."
	}
	v := strings.TrimSpace(strings.Join(fields[1:], " "))
	d.Users.SetDistrict(chatID, v)
	return fmt.Sprintf("✅ Показываю только адреса со словом «%s».", html.EscapeString(v))
}

// formatPriceRange форматирует диапазон цены для отображения.
func formatPriceRange(min, max int) string {
	switch {
	case min > 0 && max > 0:
		return fmt.Sprintf("%s–%s $", formatPrice(min), formatPrice(max))
	case max > 0:
		return fmt.Sprintf("до %s $", formatPrice(max))
	case min > 0:
		return fmt.Sprintf("от %s $", formatPrice(min))
	default:
		return "любая"
	}
}

func formatRooms(rooms []int) string {
	if len(rooms) == 0 {
		return "любое"
	}
	parts := make([]string, len(rooms))
	for i, r := range rooms {
		parts[i] = strconv.Itoa(r)
	}
	return strings.Join(parts, ", ")
}

func formatAreaRange(min, max float64) string {
	switch {
	case min > 0 && max > 0:
		return fmt.Sprintf("%g–%g м²", min, max)
	case max > 0:
		return fmt.Sprintf("до %g м²", max)
	case min > 0:
		return fmt.Sprintf("от %g м²", min)
	default:
		return "любая"
	}
}

func formatFloorFilter(noEdge bool) string {
	if noEdge {
		return "не первый/последний"
	}
	return "любой"
}

func orAny(s string) string {
	if s == "" {
		return "любой"
	}
	return html.EscapeString(s)
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
