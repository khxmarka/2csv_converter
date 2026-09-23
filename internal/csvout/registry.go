package csvout

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Registry держит слоты склейки: ключ = (директория .sql, FileBase таблицы).
// Слот сериализует первое создание файла и последующие дописывания.
type Registry struct {
	mu    sync.Mutex
	byKey map[string]*slot
	byDir map[string]*sync.Mutex
	outs  []output
}

type slot struct {
	mu        sync.Mutex
	path      string
	nCol      int
	hasHeader bool
}

type output struct {
	dir       string
	path      string
	hasHeader bool
}

// Output — CSV, записанный в этом запуске.
type Output struct {
	Path      string
	HasHeader bool
}

func NewRegistry() *Registry {
	return &Registry{
		byKey: make(map[string]*slot),
		byDir: make(map[string]*sync.Mutex),
	}
}

func mergeKey(dir, base string) string {
	dir = canonicalPath(dir)
	if runtime.GOOS == "windows" {
		base = strings.ToLower(base)
	}
	return dir + "\x00" + base
}

func canonicalPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
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

func (r *Registry) addOutput(dir, path string, hasHeader bool) {
	if r == nil || path == "" {
		return
	}
	dir = canonicalPath(dir)
	pathKey := canonicalPath(path)
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, o := range r.outs {
		if o.dir == dir && canonicalPath(o.path) == pathKey {
			r.outs[i] = output{dir: dir, path: path, hasHeader: hasHeader}
			return
		}
	}
	r.outs = append(r.outs, output{dir: dir, path: path, hasHeader: hasHeader})
}

// OutputsIn возвращает CSV, которые этот запуск записал в dir.
func (r *Registry) OutputsIn(dir string) []Output {
	if r == nil {
		return nil
	}
	dir = canonicalPath(dir)
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Output
	for _, o := range r.outs {
		if o.dir == dir {
			out = append(out, Output{Path: o.path, HasHeader: o.hasHeader})
		}
	}
	return out
}

func (r *Registry) lockDir(dir string) *sync.Mutex {
	dir = canonicalPath(dir)
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
