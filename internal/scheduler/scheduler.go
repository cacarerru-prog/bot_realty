package scheduler

import (
	"context"
	"log"
	"time"

	"flatradar/internal/collector"
	"flatradar/internal/filter"
	"flatradar/internal/model"
	"flatradar/internal/state"
	"flatradar/internal/storage"
)

// Notifier — то, что умеет отправлять объявление пользователю.
type Notifier interface {
	NotifyListing(ctx context.Context, l model.Listing) error
}

// Service связывает коллекторы, состояние, хранилище и нотификатор
// и периодически опрашивает площадки.
type Service struct {
	Collectors []collector.Collector
	State      *state.State
	Store      *storage.Store
	Notifier   Notifier
	Log        *log.Logger
	SkipWarmup bool // тестовый режим: отправить текущие подходящие лоты сразу
}

// Run выполняет «прогрев» (помечает текущие объявления как виденные без
// уведомлений), затем по тикеру опрашивает площадки и шлёт только новое.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	if s.SkipWarmup {
		s.Log.Printf("ТЕСТ: режим без прогрева — отправляю текущие подходящие лоты")
		s.poll(ctx, true)
	} else {
		s.Log.Printf("прогрев: помечаем текущие объявления как виденные…")
		s.poll(ctx, false)
	}
	s.Log.Printf("слежу за новыми (интервал %s)", interval)

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

// poll опрашивает все коллекторы один раз.
// notify=false — режим прогрева: только помечаем, не отправляем.
func (s *Service) poll(ctx context.Context, notify bool) {
	f := collector.Filter{
		City:     s.State.City(),
		PriceMin: s.State.PriceMin(),
		PriceMax: s.State.PriceMax(),
	}
	for _, c := range s.Collectors {
		listings, err := c.Fetch(ctx, f)
		s.State.UpdateSource(c.Name(), len(listings), err)
		if err != nil {
			s.Log.Printf("[%s] ошибка: %v", c.Name(), err)
			continue
		}
		s.process(ctx, listings, notify)
	}
}

func (s *Service) process(ctx context.Context, listings []model.Listing, notify bool) {
	crit := filter.Criteria{
		PriceMin: s.State.PriceMin(),
		PriceMax: s.State.PriceMax(),
	}
	for _, l := range listings {
		if s.Store.Seen(l.Key()) {
			continue
		}
		if !filter.Match(l, crit) {
			continue
		}

		// На паузе мы не шлём, но помечаем как виденное,
		// чтобы после /resume не пришёл накопившийся поток.
		if notify && !s.State.Paused() {
			if err := s.Notifier.NotifyListing(ctx, l); err != nil {
				s.Log.Printf("[%s] не отправлено %s: %v", l.Source, l.Key(), err)
				continue // не помечаем — попробуем в следующий раз
			}
			s.State.AddSent(l)
			s.Log.Printf("[%s] отправлено: %s — %d $", l.Source, l.Address, l.PriceUSD)
		}

		if err := s.Store.Mark(l.Key()); err != nil {
			s.Log.Printf("ошибка сохранения %s: %v", l.Key(), err)
		}
	}
}
