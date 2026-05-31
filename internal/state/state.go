package state

import (
	"sync"
	"time"

	"flatradar/internal/model"
)

const recentCap = 10

// SourceStat — статус одной подключённой площадки.
type SourceStat struct {
	Name      string    // "onliner", "kufar", ...
	LastCount int       // сколько подходящих было в последней выдаче
	LastError string    // текст последней ошибки (пусто — всё ок)
	LastTime  time.Time // время последнего опроса
}

// State — потокобезопасное изменяемое состояние бота.
// Его читает планировщик и меняют команды из Telegram.
type State struct {
	mu       sync.RWMutex
	paused   bool
	priceMin int
	priceMax int
	city     string
	recent   []model.Listing // последние отправленные (кольцевой буфер)
	sent     int             // всего отправлено за сессию

	sources map[string]*SourceStat // статус площадок
	order   []string               // порядок добавления площадок
}

// New создаёт состояние из стартовых настроек.
func New(city string, priceMin, priceMax int) *State {
	return &State{
		city:     city,
		priceMin: priceMin,
		priceMax: priceMax,
		sources:  make(map[string]*SourceStat),
	}
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

func (s *State) Paused() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.paused
}

func (s *State) SetPaused(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = v
}

func (s *State) PriceMin() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.priceMin
}

func (s *State) PriceMax() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.priceMax
}

func (s *State) SetPriceMax(v int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.priceMax = v
}

func (s *State) City() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.city
}

// AddSent запоминает отправленное объявление и увеличивает счётчик.
func (s *State) AddSent(l model.Listing) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent++
	s.recent = append(s.recent, l)
	if len(s.recent) > recentCap {
		s.recent = s.recent[len(s.recent)-recentCap:]
	}
}

// Recent возвращает до n последних объявлений (новые — первыми).
func (s *State) Recent(n int) []model.Listing {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if n > len(s.recent) {
		n = len(s.recent)
	}
	out := make([]model.Listing, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, s.recent[len(s.recent)-1-i])
	}
	return out
}

// Snapshot — мгновенный срез настроек для команды /status.
type Snapshot struct {
	Paused   bool
	PriceMin int
	PriceMax int
	City     string
	Sent     int
}

func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Snapshot{
		Paused:   s.paused,
		PriceMin: s.priceMin,
		PriceMax: s.priceMax,
		City:     s.city,
		Sent:     s.sent,
	}
}
