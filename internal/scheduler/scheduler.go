package scheduler

import (
	"context"
	"log"
	"time"

	"flatradar/internal/collector"
	"flatradar/internal/model"
	"flatradar/internal/state"
	"flatradar/internal/users"
)

// Notifier отправляет объявление конкретному чату.
type Notifier interface {
	NotifyListing(ctx context.Context, chatID int64, l model.Listing) error
}

// Service опрашивает площадки и раздаёт объявления подписчикам.
type Service struct {
	Collectors []collector.Collector
	Users      *users.Manager
	State      *state.State
	Notifier   Notifier
	Log        *log.Logger
	City       string // город опроса (общий для всех)
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
