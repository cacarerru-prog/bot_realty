package filter

import "flatradar/internal/model"

// Criteria — критерии, которым должно удовлетворять объявление.
type Criteria struct {
	City     string
	PriceMin int
	PriceMax int
}

// Match проверяет, подходит ли объявление под критерии.
// Это второй рубеж фильтрации (на случай, если коллектор вернул лишнее).
func Match(l model.Listing, c Criteria) bool {
	if l.PriceUSD < c.PriceMin {
		return false
	}
	if c.PriceMax > 0 && l.PriceUSD > c.PriceMax {
		return false
	}
	return true
}
