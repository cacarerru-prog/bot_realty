package storage

import (
	"encoding/json"
	"os"
	"sync"
)

// Store хранит ключи уже обработанных объявлений, чтобы не слать дубли
// и переживать перезапуск бота. Простая реализация поверх JSON-файла.
//
// TODO (этап 2): заменить на SQLite (modernc.org/sqlite) при росте объёма.
type Store struct {
	path string
	mu   sync.Mutex
	seen map[string]bool
}

// Open загружает хранилище из файла (или создаёт пустое, если файла нет).
func Open(path string) (*Store, error) {
	s := &Store{path: path, seen: make(map[string]bool)}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}

	var keys []string
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, err
	}
	for _, k := range keys {
		s.seen[k] = true
	}
	return s, nil
}

// Seen сообщает, видели ли мы это объявление раньше.
func (s *Store) Seen(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[key]
}

// Mark помечает объявление как обработанное и сохраняет на диск.
func (s *Store) Mark(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen[key] {
		return nil
	}
	s.seen[key] = true
	return s.flushLocked()
}

func (s *Store) flushLocked() error {
	keys := make([]string, 0, len(s.seen))
	for k := range s.seen {
		keys = append(keys, k)
	}
	data, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}
