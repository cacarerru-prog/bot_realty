// Package store — постоянное хранилище истории объявлений на SQLite
// (чистый Go, без CGO). Держит архив лотов с отметкой «ушёл» (removed_at)
// и историю цен — на этом строится рыночная аналитика карточки.
package store

import (
	"database/sql"
	"time"

	"flatradar/internal/geo"
	"flatradar/internal/model"

	_ "modernc.org/sqlite"
)

// Store — обёртка над БД.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS listings_archive (
    key           TEXT PRIMARY KEY,
    source        TEXT NOT NULL,
    rooms         INTEGER,
    area          REAL,
    cell          TEXT,
    price_usd     INTEGER,
    min_price_usd INTEGER,
    price_drops   INTEGER DEFAULT 0,
    first_seen    DATETIME,
    last_seen     DATETIME,
    removed_at    DATETIME
);
CREATE INDEX IF NOT EXISTS idx_segment ON listings_archive(rooms, cell, removed_at);
CREATE INDEX IF NOT EXISTS idx_last_seen ON listings_archive(source, last_seen);

CREATE TABLE IF NOT EXISTS price_history (
    key       TEXT NOT NULL,
    price_usd INTEGER,
    at        DATETIME
);
CREATE INDEX IF NOT EXISTS idx_ph_key ON price_history(key);
`

// Open открывает (или создаёт) базу и применяет схему.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite — один писатель; пул из одного соединения избегает «database is locked».
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close закрывает БД.
func (s *Store) Close() error { return s.db.Close() }

// Record фиксирует встреченный лот: создаёт запись или обновляет цену,
// историю и last_seen. Если лот ранее был помечен ушедшим — «воскрешает».
func (s *Store) Record(l model.Listing, now time.Time) error {
	key := l.Key()
	var (
		oldPrice  int
		minPrice  int
		exists    bool
		wasRemove sql.NullTime
	)
	row := s.db.QueryRow(
		`SELECT price_usd, min_price_usd, removed_at FROM listings_archive WHERE key=?`, key)
	switch err := row.Scan(&oldPrice, &minPrice, &wasRemove); err {
	case nil:
		exists = true
	case sql.ErrNoRows:
		exists = false
	default:
		return err
	}

	if !exists {
		_, err := s.db.Exec(`
            INSERT INTO listings_archive
                (key, source, rooms, area, cell, price_usd, min_price_usd, price_drops, first_seen, last_seen, removed_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, NULL)`,
			key, l.Source, l.Rooms, l.Area, geo.Cell(l.Lat, l.Lon),
			l.PriceUSD, l.PriceUSD, now, now)
		if err != nil {
			return err
		}
		return s.addHistory(key, l.PriceUSD, now)
	}

	// Уже знаем лот.
	if l.PriceUSD != oldPrice && l.PriceUSD > 0 {
		drop := 0
		if l.PriceUSD < oldPrice {
			drop = 1
		}
		newMin := minPrice
		if l.PriceUSD < newMin || newMin == 0 {
			newMin = l.PriceUSD
		}
		if _, err := s.db.Exec(`
            UPDATE listings_archive
               SET price_usd=?, min_price_usd=?, price_drops=price_drops+?, last_seen=?, removed_at=NULL
             WHERE key=?`,
			l.PriceUSD, newMin, drop, now, key); err != nil {
			return err
		}
		return s.addHistory(key, l.PriceUSD, now)
	}

	// Цена та же — обновляем присутствие (и снимаем removed_at при «воскрешении»).
	_, err := s.db.Exec(
		`UPDATE listings_archive SET last_seen=?, removed_at=NULL WHERE key=?`, now, key)
	return err
}

func (s *Store) addHistory(key string, price int, at time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO price_history (key, price_usd, at) VALUES (?, ?, ?)`, key, price, at)
	return err
}

// MarkRemovedSourceBefore помечает ушедшими лоты площадки, не встреченные
// в завершившемся полном проходе (last_seen раньше его старта). Точно — только
// для площадок с полным покрытием (Onliner). Возвращает число помеченных.
func (s *Store) MarkRemovedSourceBefore(source string, passStart, now time.Time) (int64, error) {
	res, err := s.db.Exec(`
        UPDATE listings_archive SET removed_at=?
         WHERE source=? AND removed_at IS NULL AND last_seen < ?`,
		now, source, passStart)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// MarkRemovedNotSeenSince помечает ушедшими любые лоты, которых не видели
// дольше cutoff (страховка для площадок без полного скана, напр. Kufar).
func (s *Store) MarkRemovedNotSeenSince(cutoff, now time.Time) (int64, error) {
	res, err := s.db.Exec(`
        UPDATE listings_archive SET removed_at=?
         WHERE removed_at IS NULL AND last_seen < ?`, now, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Stats возвращает аналитику по лоту (дни на рынке, снижения, минимум).
// Поле BelowMarket тут не считается — его заполняет вызывающая сторона.
func (s *Store) Stats(key string, now time.Time) (model.Stats, bool) {
	var (
		firstSeen time.Time
		drops     int
		minPrice  int
	)
	err := s.db.QueryRow(
		`SELECT first_seen, price_drops, min_price_usd FROM listings_archive WHERE key=?`, key).
		Scan(&firstSeen, &drops, &minPrice)
	if err != nil {
		return model.Stats{}, false
	}
	days := int(now.Sub(firstSeen).Hours() / 24)
	if days < 0 {
		days = 0
	}
	return model.Stats{
		DaysOnMarket: days,
		PriceDrops:   drops,
		MinPriceUSD:  minPrice,
	}, true
}

// SegmentAvgPPM2 — средневзвешенная по площади цена за м² среди ушедших
// лотов сегмента (комнаты + гео-ячейка) за период since..now.
// Возвращает среднюю и размер выборки.
func (s *Store) SegmentAvgPPM2(rooms int, cell string, since time.Time) (avg float64, n int) {
	if cell == "" {
		return 0, 0
	}
	var sumPrice, sumArea sql.NullFloat64
	var cnt int
	err := s.db.QueryRow(`
        SELECT COALESCE(SUM(price_usd),0), COALESCE(SUM(area),0), COUNT(*)
          FROM listings_archive
         WHERE rooms=? AND cell=? AND area>0 AND price_usd>0
           AND removed_at IS NOT NULL AND removed_at >= ?`,
		rooms, cell, since).Scan(&sumPrice, &sumArea, &cnt)
	if err != nil || !sumArea.Valid || sumArea.Float64 <= 0 {
		return 0, 0
	}
	return sumPrice.Float64 / sumArea.Float64, cnt
}
