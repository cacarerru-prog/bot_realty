// Package users хранит подписчиков бота: у каждого свой фильтр, пауза и
// персональная история показанных объявлений (дедуп на пользователя).
package users

import (
	"bytes"
	"encoding/json"
	"os"
	"sync"
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

// User — один подписчик.
type User struct {
	ChatID   int64    `json:"chat_id"`
	City     string   `json:"city"`
	PriceMin int      `json:"price_min"`
	PriceMax int      `json:"price_max"`
	Paused   bool     `json:"paused"`
	Sent     int      `json:"sent"`
	Seen     []string `json:"seen"` // FIFO ключей показанных объявлений

	seenSet map[string]bool // индекс для быстрого поиска (не сериализуется)
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

func (m *Manager) SetPriceMax(chatID int64, v int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u := m.users[chatID]; u != nil {
		u.PriceMax = v
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
