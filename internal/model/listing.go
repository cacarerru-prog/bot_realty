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
	CreatedAt  time.Time // когда объявление появилось на площадке
}

// Key — уникальный ключ объявления для дедупликации между запусками.
func (l Listing) Key() string {
	return l.Source + ":" + l.ExternalID
}
