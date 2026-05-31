// Package state хранит глобальную runtime-статистику бота —
// статус подключённых площадок (для команды /sources).
// Пользовательские фильтры и дедуп живут в пакете users.
package state

import (
	"sync"
	"time"
)

// SourceStat — статус одной подключённой площадки.
type SourceStat struct {
	Name      string    // "onliner", "kufar", ...
	LastCount int       // сколько объявлений было в последней выдаче
	LastError string    // текст последней ошибки (пусто — всё ок)
	LastTime  time.Time // время последнего опроса
}

// State — потокобезопасная статистика площадок.
type State struct {
	mu      sync.RWMutex
	sources map[string]*SourceStat
	order   []string
}

// New создаёт пустую статистику.
func New() *State {
	return &State{sources: make(map[string]*SourceStat)}
}

// RegisterSource добавляет площадку в список подключённых.
func (s *State) RegisterSource(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sources[name]; !ok {
		s.sources[name] = &SourceStat{Name: name}
		s.order = append(s.order, name)
	}
}

// UpdateSource обновляет статус площадки после опроса.
func (s *State) UpdateSource(name string, count int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.sources[name]
	if st == nil {
		return
	}
	st.LastTime = time.Now()
	if err != nil {
		st.LastError = err.Error()
	} else {
		st.LastError = ""
		st.LastCount = count
	}
}

// Sources возвращает статусы площадок в порядке подключения.
func (s *State) Sources() []SourceStat {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SourceStat, 0, len(s.order))
	for _, n := range s.order {
		out = append(out, *s.sources[n])
	}
	return out
}
