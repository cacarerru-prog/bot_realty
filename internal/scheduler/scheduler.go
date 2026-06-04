package scheduler

import (
	"context"
	"log"
	"time"

	"flatradar/internal/collector"
	"flatradar/internal/geo"
	"flatradar/internal/model"
	"flatradar/internal/state"
	"flatradar/internal/store"
	"flatradar/internal/users"
)

// Notifier отправляет объявление конкретному чату.
type Notifier interface {
	NotifyListing(ctx context.Context, chatID int64, l model.Listing) error
}

// maxScanPages — предохранитель для постраничного скана Onliner.
const maxScanPages = 90

// Service опрашивает площадки и раздаёт объявления подписчикам.
type Service struct {
	Collectors []collector.Collector
	Users      *users.Manager
	State      *state.State
	Notifier   Notifier
	Store      *store.Store // архив истории (может быть nil — тогда без аналитики)
	Log        *log.Logger
	City       string // город опроса (общий для всех)
	BelowPct   int    // порог скидки к рынку для метки 🔥 (напр. 7)
	MinSample  int    // минимум «ушедших» в сегменте, чтобы доверять средней
	SkipWarmup bool
}

// Run выполняет прогрев (помечает текущие лоты показанными всем
// подписчикам без уведомлений), затем по тикеру опрашивает площадки.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	if s.SkipWarmup {
		s.Log.Printf("ТЕСТ: режим без прогрева")
		s.poll(ctx, true)
	} else {
		s.Log.Printf("прогрев: помечаем текущие лоты показанными подписчикам…")
		s.poll(ctx, false)
	}
	s.Log.Printf("слежу за новыми (интервал %s, подписчиков: %d)", interval, s.Users.Count())

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.Log.Printf("остановка планировщика")
			return
		case <-ticker.C:
			s.poll(ctx, true)
		}
	}
}

// poll опрашивает площадки один раз и раздаёт результат подписчикам.
// notify=false — прогрев: только помечаем, не отправляем.
func (s *Service) poll(ctx context.Context, notify bool) {
	// Широкий фильтр: тянем все лоты города, цену фильтруем на пользователя.
	f := collector.Filter{City: s.City}

	var all []model.Listing
	for _, c := range s.Collectors {
		listings, err := c.Fetch(ctx, f)
		s.State.UpdateSource(c.Name(), len(listings), err)
		if err != nil {
			s.Log.Printf("[%s] ошибка: %v", c.Name(), err)
			continue
		}
		all = append(all, listings...)
	}

	// Пишем всё увиденное в архив и обогащаем карточки аналитикой.
	if s.Store != nil {
		now := time.Now()
		for i := range all {
			if err := s.Store.Record(all[i], now); err != nil {
				s.Log.Printf("store: запись %s: %v", all[i].Key(), err)
			}
		}
		for i := range all {
			all[i].Stats = s.statsFor(all[i], now)
		}
	}

	for _, u := range s.Users.All() {
		for _, l := range all {
			if s.Users.HasSeen(u.ChatID, l.Key()) {
				continue
			}
			if !u.Matches(l) {
				continue
			}
			// Отправляем, только если уведомляем и пользователь не на паузе.
			if notify && !u.Paused {
				if err := s.Notifier.NotifyListing(ctx, u.ChatID, l); err != nil {
					s.Log.Printf("[%s] не отправлено %d: %v", l.Source, u.ChatID, err)
					continue // не помечаем — попробуем в следующий раз
				}
				s.Users.IncSent(u.ChatID)
				s.Log.Printf("[%s] -> %d: %s — %d $", l.Source, u.ChatID, l.Address, l.PriceUSD)
			}
			// Помечаем показанным (в т.ч. на паузе и при прогреве),
			// чтобы после /resume не пришёл накопившийся поток.
			s.Users.MarkSeen(u.ChatID, l.Key())
		}
	}
	s.Users.Save()
}

// statsFor собирает аналитику по лоту: дни на рынке, снижения, минимум
// и насколько цена ниже средней по сегменту (комнаты + гео-ячейка).
func (s *Service) statsFor(l model.Listing, now time.Time) *model.Stats {
	st, ok := s.Store.Stats(l.Key(), now)
	if !ok {
		return nil
	}
	avg, n := s.Store.SegmentAvgPPM2(l.Rooms, geo.Cell(l.Lat, l.Lon), now.AddDate(0, 0, -30))
	ppm2 := l.PricePerM2()
	if n >= s.MinSample && avg > 0 && ppm2 > 0 {
		pct := (avg - ppm2) / avg * 100
		if pct >= float64(s.BelowPct) {
			st.BelowMarket = int(pct + 0.5)
		}
	}
	return &st
}

// RunScanner раз в interval полностью обходит рынок (для истории цен и
// отметки «ушедших»). Блокируется до отмены ctx — запускать в горутине.
func (s *Service) RunScanner(ctx context.Context, interval time.Duration) {
	if s.Store == nil {
		return
	}
	s.scanOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scanOnce(ctx)
		}
	}
}

// scanOnce — один полный проход: Onliner постранично (полное покрытие),
// Kufar — широкая выдача. Затем помечает ушедшие лоты.
func (s *Service) scanOnce(ctx context.Context) {
	passStart := time.Now()
	var recorded int

	for _, c := range s.Collectors {
		switch c.Name() {
		case "onliner":
			for page := 1; page <= maxScanPages; page++ {
				if ctx.Err() != nil {
					return
				}
				ls, err := c.Fetch(ctx, collector.Filter{City: s.City, Page: page})
				if err != nil {
					s.Log.Printf("скан [onliner] стр.%d: %v", page, err)
					break
				}
				if len(ls) == 0 {
					break
				}
				for _, l := range ls {
					if err := s.Store.Record(l, time.Now()); err == nil {
						recorded++
					}
				}
			}
			// Полное покрытие: чего не встретили в этом проходе — ушло.
			if n, err := s.Store.MarkRemovedSourceBefore("onliner", passStart, time.Now()); err == nil && n > 0 {
				s.Log.Printf("скан [onliner]: помечено ушедшими %d", n)
			}

		case "kufar":
			ls, err := c.Fetch(ctx, collector.Filter{City: s.City, Size: 200})
			if err != nil {
				s.Log.Printf("скан [kufar]: %v", err)
				continue
			}
			for _, l := range ls {
				if err := s.Store.Record(l, time.Now()); err == nil {
					recorded++
				}
			}
		}
	}

	// Страховка для площадок без полного скана: давно не видели — считаем ушедшим.
	if n, err := s.Store.MarkRemovedNotSeenSince(passStart.Add(-72*time.Hour), time.Now()); err == nil && n > 0 {
		s.Log.Printf("скан: помечено ушедшими по таймауту %d", n)
	}
	s.Log.Printf("скан рынка завершён: записей обработано %d за %s", recorded, time.Since(passStart).Round(time.Second))
}
