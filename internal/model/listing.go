package model

import "time"

// Listing — единая модель объявления, к которой коллекторы приводят данные
// со всех площадок.
type Listing struct {
	Source     string    // "onliner" | "kufar" | "realt"
	ExternalID string    // id на площадке
	Title      string    // заголовок/адрес для отображения
	PriceUSD   int       // цена в долларах США
	Rooms      int       // количество комнат (0 = неизвестно/студия)
	Area       float64   // общая площадь, м²
	Floor      string    // этаж в формате "5/9" — для отображения
	FloorNum   int       // этаж квартиры числом (0 = неизвестно)
	FloorTotal int       // этажность дома числом (0 = неизвестно)
	Address    string    // адрес
	URL        string    // ссылка на объявление
	Photo      string    // ссылка на превью-фото
	Lat        float64   // широта (0 = неизвестно)
	Lon        float64   // долгота (0 = неизвестно)
	CreatedAt  time.Time // когда объявление появилось на площадке

	// Поля ниже заполняются из хранилища перед отправкой карточки
	// (рыночная аналитика). В выдаче коллекторов они пустые.
	Stats *Stats `json:"-"`
}

// Stats — рыночная аналитика по лоту для карточки.
type Stats struct {
	DaysOnMarket int // сколько дней лот в продаже (по first_seen)
	PriceDrops   int // сколько раз снижали цену
	MinPriceUSD  int // минимальная цена за всю историю лота
	BelowMarket  int // на сколько % цена ниже средней по сегменту (0 = не ниже/нет данных)
}

// Key — уникальный ключ объявления для дедупликации между запусками.
func (l Listing) Key() string {
	return l.Source + ":" + l.ExternalID
}

// PricePerM2 — цена за квадратный метр (0, если площадь неизвестна).
func (l Listing) PricePerM2() float64 {
	if l.Area <= 0 {
		return 0
	}
	return float64(l.PriceUSD) / l.Area
}
