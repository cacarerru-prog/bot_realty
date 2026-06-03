// Package users хранит подписчиков бота: у каждого свой фильтр, пауза и
// персональная история показанных объявлений (дедуп на пользователя).
package users

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"sync"

	"flatradar/internal/model"
)

// seenCap — сколько ключей объявлений держим на пользователя (FIFO).
// Объявления всё равно уходят с первых страниц площадок, так что
// большего и не нужно.
const seenCap = 600

// Defaults — настройки фильтра по умолчанию для новых пользователей.
type Defaults struct {
	City     string
	PriceMin int
	PriceMax int
}

// User — один подписчик. Все поля фильтра ниже — персональные;
// пустое/нулевое значение означает «не ограничивать».
type User struct {
	ChatID   int64    `json:"chat_id"`
	City     string   `json:"city"`
	PriceMin int      `json:"price_min"`
	PriceMax int      `json:"price_max"`
	Rooms    []int    `json:"rooms,omitempty"`    // допустимое число комнат (пусто = любое)
	AreaMin  float64  `json:"area_min,omitempty"` // мин. площадь, м²
	AreaMax  float64  `json:"area_max,omitempty"` // макс. площадь, м²
	District string   `json:"district,omitempty"` // подстрока адреса (район/улица/ЖК)
	NoEdge   bool     `json:"no_edge,omitempty"`  // исключать первый и последний этаж
	Paused   bool     `json:"paused"`
	Sent     int      `json:"sent"`
	Seen     []string `json:"seen"` // FIFO ключей показанных объявлений

	seenSet map[string]bool // индекс для быстрого поиска (не сериализуется)
}

// Matches проверяет, подходит ли объявление под все фильтры пользователя.
func (u User) Matches(l model.Listing) bool {
	if l.PriceUSD < u.PriceMin {
		return false
	}
	if u.PriceMax > 0 && l.PriceUSD > u.PriceMax {
		return false
	}
	if len(u.Rooms) > 0 {
		ok := false
		for _, r := range u.Rooms {
			if l.Rooms == r {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if u.AreaMin > 0 && l.Area > 0 && l.Area < u.AreaMin {
		return false
	}
	if u.AreaMax > 0 && l.Area > u.AreaMax {
		return false
	}
	if u.District != "" && !strings.Contains(strings.ToLower(l.Address), strings.ToLower(u.District)) {
		return false
	}
	if u.NoEdge && l.FloorNum > 0 && l.FloorTotal > 0 {
		if l.FloorNum == 1 || l.FloorNum == l.FloorTotal {
			return false
		}
	}
	return true
}

// Manager управляет всеми пользователями и их персистентностью.
type Manager struct {
	mu       sync.Mutex
	path     string
	users    map[int64]*User
	defaults Defaults
	dirty    bool
}

var bom = []byte{0xEF, 0xBB, 0xBF}

// Open загружает пользователей из файла (или создаёт пустой реестр).
func Open(path string, d Defaults) (*Manager, error) {
	m := &Manager{path: path, users: make(map[int64]*User), defaults: d}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, err
	}
	data = bytes.TrimPrefix(data, bom)

	var list []*User
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	for _, u := range list {
		u.seenSet = make(map[string]bool, len(u.Seen))
		for _, k := range u.Seen {
			u.seenSet[k] = true
		}
		m.users[u.ChatID] = u
	}
	return m, nil
}

// Ensure регистрирует пользователя, если его ещё нет.
// Возвращает true, если это новый пользователь.
func (m *Manager) Ensure(chatID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[chatID]; ok {
		return false
	}
	m.users[chatID] = &User{
		ChatID:   chatID,
		City:     m.defaults.City,
		PriceMin: m.defaults.PriceMin,
		PriceMax: m.defaults.PriceMax,
		seenSet:  make(map[string]bool),
	}
	m.saveLocked()
	return true
}

// Get возвращает копию данных пользователя.
func (m *Manager) Get(chatID int64) (User, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[chatID]
	if !ok {
		return User{}, false
	}
	return *u, true
}

// All возвращает копии всех пользователей (для рассылки).
func (m *Manager) All() []User {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]User, 0, len(m.users))
	for _, u := range m.users {
		out = append(out, *u)
	}
	return out
}

// Count — число подписчиков.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.users)
}

func (m *Manager) SetPaused(chatID int64, v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u := m.users[chatID]; u != nil {
		u.Paused = v
		m.saveLocked()
	}
}

// SetPriceRange задаёт минимальную и максимальную цену (0 в max = без верха).
func (m *Manager) SetPriceRange(chatID int64, min, max int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u := m.users[chatID]; u != nil {
		u.PriceMin = min
		u.PriceMax = max
		m.saveLocked()
	}
}

// SetRooms задаёт допустимое число комнат (nil/пусто = любое).
func (m *Manager) SetRooms(chatID int64, rooms []int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u := m.users[chatID]; u != nil {
		u.Rooms = rooms
		m.saveLocked()
	}
}

// SetArea задаёт диапазон площади в м² (0 = без ограничения).
func (m *Manager) SetArea(chatID int64, min, max float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u := m.users[chatID]; u != nil {
		u.AreaMin = min
		u.AreaMax = max
		m.saveLocked()
	}
}

// SetDistrict задаёт подстроку адреса (пусто = без ограничения).
func (m *Manager) SetDistrict(chatID int64, v string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u := m.users[chatID]; u != nil {
		u.District = v
		m.saveLocked()
	}
}

// SetNoEdge включает/выключает исключение первого и последнего этажа.
func (m *Manager) SetNoEdge(chatID int64, v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u := m.users[chatID]; u != nil {
		u.NoEdge = v
		m.saveLocked()
	}
}

// ResetFilters сбрасывает фильтры пользователя к значениям по умолчанию.
func (m *Manager) ResetFilters(chatID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u := m.users[chatID]; u != nil {
		u.PriceMin = m.defaults.PriceMin
		u.PriceMax = m.defaults.PriceMax
		u.Rooms = nil
		u.AreaMin = 0
		u.AreaMax = 0
		u.District = ""
		u.NoEdge = false
		m.saveLocked()
	}
}

// HasSeen сообщает, показывали ли объявление этому пользователю.
func (m *Manager) HasSeen(chatID int64, key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.users[chatID]
	return u != nil && u.seenSet[key]
}

// MarkSeen помечает объявление показанным пользователю (без записи на
// диск — вызови Save в конце цикла). Возвращает false, если уже было.
func (m *Manager) MarkSeen(chatID int64, key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.users[chatID]
	if u == nil || u.seenSet[key] {
		return false
	}
	u.seenSet[key] = true
	u.Seen = append(u.Seen, key)
	if len(u.Seen) > seenCap {
		drop := len(u.Seen) - seenCap
		for _, k := range u.Seen[:drop] {
			delete(u.seenSet, k)
		}
		u.Seen = u.Seen[drop:]
	}
	m.dirty = true
	return true
}

// IncSent увеличивает счётчик отправленных пользователю.
func (m *Manager) IncSent(chatID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u := m.users[chatID]; u != nil {
		u.Sent++
		m.dirty = true
	}
}

// Save сбрасывает на диск, если были изменения.
func (m *Manager) Save() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dirty {
		m.saveLocked()
		m.dirty = false
	}
}

func (m *Manager) saveLocked() {
	list := make([]*User, 0, len(m.users))
	for _, u := range m.users {
		list = append(list, u)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(m.path, data, 0o644)
}
