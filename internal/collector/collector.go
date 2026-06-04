package collector

import (
	"context"

	"flatradar/internal/model"
)

// Filter — критерии поиска объявлений.
type Filter struct {
	City     string
	PriceMin int
	PriceMax int
	Page     int // страница выдачи (Onliner), 0/1 — первая
	Size     int // размер выдачи (Kufar), 0 — значение по умолчанию
}

// Collector — источник объявлений (одна площадка).
// Чтобы добавить новую площадку, достаточно реализовать этот интерфейс
// и зарегистрировать коллектор в main.
type Collector interface {
	// Name — короткое имя площадки ("onliner", "kufar", ...).
	Name() string
	// Fetch возвращает свежие объявления, подходящие под фильтр.
	// Дедупликацию выполняет вызывающая сторона.
	Fetch(ctx context.Context, f Filter) ([]model.Listing, error)
}
