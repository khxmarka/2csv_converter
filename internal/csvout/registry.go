package csvout

import (
	"path/filepath"
	"sync"
)

// Registry держит слоты склейки: ключ = (директория .sql, FileBase таблицы).
// Слот сериализует первое создание файла и последующие дописывания.
type Registry struct {
	mu    sync.Mutex
	byKey map[string]*slot
	byDir map[string]*sync.Mutex
}

type slot struct {
	mu   sync.Mutex
	path string
	nCol int
}

func NewRegistry() *Registry {
	return &Registry{
		byKey: make(map[string]*slot),
		byDir: make(map[string]*sync.Mutex),
	}
}

func mergeKey(dir, base string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return dir + "\x00" + base
}

func (r *Registry) acquire(dir, base string) *slot {
	key := mergeKey(dir, base)
	r.mu.Lock()
	s, ok := r.byKey[key]
	if !ok {
		s = new(slot)
		r.byKey[key] = s
	}
	r.mu.Unlock()
	s.mu.Lock()
	return s
}

// acquireUnique — отдельный слот без склейки INSERT. Имя файла сериализует lockDir.
func (r *Registry) acquireUnique() *slot {
	s := new(slot)
	s.mu.Lock()
	return s
}

func (r *Registry) lockDir(dir string) *sync.Mutex {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	r.mu.Lock()
	m, ok := r.byDir[dir]
	if !ok {
		m = new(sync.Mutex)
		r.byDir[dir] = m
	}
	r.mu.Unlock()
	m.Lock()
	return m
}
