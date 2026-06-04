package store

import (
	"path/filepath"
	"testing"
	"time"

	"flatradar/internal/geo"
	"flatradar/internal/model"
)

func TestArchiveAndSegment(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now()
	t0 := now.Add(-10 * 24 * time.Hour) // лот появился 10 дней назад
	lat, lon := 53.9006, 27.5590

	a := model.Listing{Source: "onliner", ExternalID: "1", Rooms: 2, Area: 50, PriceUSD: 100000, Lat: lat, Lon: lon}
	if err := s.Record(a, t0); err != nil {
		t.Fatal(err)
	}
	// Через 3 дня цену снизили до 90000.
	a.PriceUSD = 90000
	if err := s.Record(a, t0.Add(3*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	st, ok := s.Stats(a.Key(), now)
	if !ok {
		t.Fatal("нет статистики")
	}
	if st.PriceDrops != 1 {
		t.Errorf("снижений: got %d, want 1", st.PriceDrops)
	}
	if st.MinPriceUSD != 90000 {
		t.Errorf("минимум: got %d, want 90000", st.MinPriceUSD)
	}
	if st.DaysOnMarket < 9 || st.DaysOnMarket > 11 {
		t.Errorf("дни на рынке: got %d, want ~10", st.DaysOnMarket)
	}

	// Ещё два «ушедших» лота того же сегмента для средней цены.
	for i, p := range []struct {
		id    string
		price int
		area  float64
	}{
		{"2", 110000, 55}, {"3", 95000, 48},
	} {
		l := model.Listing{Source: "onliner", ExternalID: p.id, Rooms: 2, Area: p.area, PriceUSD: p.price, Lat: lat, Lon: lon}
		if err := s.Record(l, t0.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// Помечаем всё ушедшим (last_seen старый).
	if _, err := s.MarkRemovedNotSeenSince(now.Add(-time.Hour), now); err != nil {
		t.Fatal(err)
	}

	cell := geo.Cell(lat, lon)
	avg, n := s.SegmentAvgPPM2(2, cell, now.Add(-30*24*time.Hour))
	if n != 3 {
		t.Errorf("выборка: got %d, want 3", n)
	}
	// средневзвешенная = (90000+110000+95000)/(50+55+48)
	want := float64(90000+110000+95000) / float64(50+55+48)
	if avg < want-0.5 || avg > want+0.5 {
		t.Errorf("средняя цена/м²: got %.2f, want %.2f", avg, want)
	}
	t.Logf("cell=%s avg/m²=%.0f n=%d", cell, avg, n)
}
