package telegram

import (
	"context"
	"strconv"
	"strings"

	"flatradar/internal/users"
)

// Пресеты диапазонов для кнопок. Меняешь тут — меняются и подписи, и логика.
var pricePresets = []struct {
	Label    string
	Min, Max int
}{
	{"до 50к", 0, 50000},
	{"50–80к", 50000, 80000},
	{"80–120к", 80000, 120000},
}

var areaPresets = []struct {
	Label    string
	Min, Max float64
}{
	{"до 40", 0, 40},
	{"40–60", 40, 60},
	{"60–80", 60, 80},
}

// filterPanelText — заголовок панели с текущим состоянием фильтра.
func filterPanelText(u users.User) string {
	return "<b>🔍 Фильтр</b> — настрой кнопками ниже.\n\n" +
		"💵 Цена: " + formatPriceRange(u.PriceMin, u.PriceMax) + "\n" +
		"🚪 Комнаты: " + formatRooms(u.Rooms) + "\n" +
		"📐 Площадь: " + formatAreaRange(u.AreaMin, u.AreaMax) + "\n" +
		"🏢 Этаж: " + formatFloorFilter(u.NoEdge) + "\n" +
		"🏙 Район: " + orAny(u.District) + " <i>(меняется командой /district)</i>"
}

// btn — короткий конструктор инлайн-кнопки.
func btn(text, data string) map[string]string {
	return map[string]string{"text": text, "callback_data": data}
}

// check добавляет галочку к подписи выбранной кнопки.
func check(on bool, label string) string {
	if on {
		return "✅ " + label
	}
	return label
}

// filterKeyboard собирает инлайн-клавиатуру под текущее состояние фильтра.
func filterKeyboard(u users.User) map[string]any {
	hasRoom := func(n int) bool {
		for _, r := range u.Rooms {
			if r == n {
				return true
			}
		}
		return false
	}

	rooms := []map[string]string{
		btn(check(hasRoom(1), "1"), "f:r:1"),
		btn(check(hasRoom(2), "2"), "f:r:2"),
		btn(check(hasRoom(3), "3"), "f:r:3"),
		btn(check(hasRoom(4), "4"), "f:r:4"),
		btn(check(hasRoom(5), "5+"), "f:r:5plus"),
	}

	price := make([]map[string]string, 0, len(pricePresets))
	for _, p := range pricePresets {
		on := u.PriceMin == p.Min && u.PriceMax == p.Max
		price = append(price, btn(check(on, "💵 "+p.Label), "f:p:"+strconv.Itoa(p.Min)+"-"+strconv.Itoa(p.Max)))
	}

	area := make([]map[string]string, 0, len(areaPresets))
	for _, p := range areaPresets {
		on := u.AreaMin == p.Min && u.AreaMax == p.Max
		area = append(area, btn(check(on, "📐 "+p.Label), "f:a:"+formatFloat(p.Min)+"-"+formatFloat(p.Max)))
	}

	floor := []map[string]string{
		btn(check(u.NoEdge, "🏢 не первый/последний этаж"), "f:fl"),
	}

	bottom := []map[string]string{
		btn("♻️ Сброс", "f:reset"),
		btn("✅ Готово", "f:done"),
	}

	return map[string]any{
		"inline_keyboard": [][]map[string]string{rooms, floor, price, area, bottom},
	}
}

// handleCallback обрабатывает нажатие инлайн-кнопки панели фильтров.
func (c *Client) handleCallback(ctx context.Context, d Deps, cb *callbackQuery) {
	if cb.Message == nil {
		_ = c.answerCallback(ctx, cb.ID, "")
		return
	}
	chatID := cb.Message.Chat.ID
	d.Users.Ensure(chatID)

	done := applyFilterCallback(d.Users, chatID, cb.Data)
	u, _ := d.Users.Get(chatID)

	if done {
		// Прячем клавиатуру и показываем итог.
		_ = c.editMessage(ctx, chatID, cb.Message.MessageID,
			"<b>✅ Фильтр сохранён</b>\n\n"+filterSummary(u),
			map[string]any{"inline_keyboard": [][]map[string]string{}})
	} else {
		_ = c.editMessage(ctx, chatID, cb.Message.MessageID, filterPanelText(u), filterKeyboard(u))
	}
	_ = c.answerCallback(ctx, cb.ID, "")
}

// applyFilterCallback меняет фильтр пользователя по данным кнопки.
// Возвращает true, если нажато «Готово» (панель надо закрыть).
func applyFilterCallback(m *users.Manager, chatID int64, data string) bool {
	u, _ := m.Get(chatID)
	switch {
	case data == "f:done":
		return true

	case data == "f:reset":
		m.ResetFilters(chatID)

	case data == "f:fl":
		m.SetNoEdge(chatID, !u.NoEdge)

	case strings.HasPrefix(data, "f:r:"):
		m.SetRooms(chatID, toggleRooms(u.Rooms, strings.TrimPrefix(data, "f:r:")))

	case strings.HasPrefix(data, "f:p:"):
		min, max := parseRange(strings.TrimPrefix(data, "f:p:"))
		// Повторное нажатие активного пресета снимает ограничение по цене.
		if u.PriceMin == min && u.PriceMax == max {
			m.SetPriceRange(chatID, 0, 0)
		} else {
			m.SetPriceRange(chatID, min, max)
		}

	case strings.HasPrefix(data, "f:a:"):
		min, max := parseRange(strings.TrimPrefix(data, "f:a:"))
		if int(u.AreaMin) == min && int(u.AreaMax) == max {
			m.SetArea(chatID, 0, 0)
		} else {
			m.SetArea(chatID, float64(min), float64(max))
		}
	}
	return false
}

// toggleRooms добавляет/убирает значение комнат. "5plus" = диапазон 5–9.
func toggleRooms(cur []int, key string) []int {
	set := map[int]bool{}
	for _, r := range cur {
		set[r] = true
	}
	if key == "5plus" {
		if set[5] {
			for n := 5; n <= 9; n++ {
				delete(set, n)
			}
		} else {
			for n := 5; n <= 9; n++ {
				set[n] = true
			}
		}
	} else {
		n, _ := strconv.Atoi(key)
		if set[n] {
			delete(set, n)
		} else {
			set[n] = true
		}
	}
	var out []int
	for n := 1; n <= 9; n++ {
		if set[n] {
			out = append(out, n)
		}
	}
	return out
}

// parseRange разбирает "min-max" в два целых.
func parseRange(s string) (int, int) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	min, _ := strconv.Atoi(parts[0])
	max, _ := strconv.Atoi(parts[1])
	return min, max
}

// formatFloat печатает число без лишних нулей (40, а не 40.0).
func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// filterSummary — краткая сводка фильтра для экрана «Готово».
func filterSummary(u users.User) string {
	return "💵 Цена: " + formatPriceRange(u.PriceMin, u.PriceMax) + "\n" +
		"🚪 Комнаты: " + formatRooms(u.Rooms) + "\n" +
		"📐 Площадь: " + formatAreaRange(u.AreaMin, u.AreaMax) + "\n" +
		"🏢 Этаж: " + formatFloorFilter(u.NoEdge) + "\n" +
		"🏙 Район: " + orAny(u.District) + "\n\n" +
		"Изменить — снова «🔍 Фильтры». Жду новые лоты."
}
