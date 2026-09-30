package csvout

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Registry serializes initial publication and later appends for each
// (SQL directory, table FileBase) merge key.
type Registry struct {
	mu    sync.Mutex
	byKey map[string]*slot
	byDir map[string]*sync.Mutex
	// outs indexes this run's CSV outputs by canonical directory path to avoid
	// scanning every prior output for each new file.
	outs map[string][]output
	// written tracks the canonical path of every CSV published in this run.
	written map[string]struct{}
}

// ErrNameTaken reports that another key already owns the target path. Replacing
// it could erase output whose successful source is later deleted.
var ErrNameTaken = errors.New("имя CSV уже занято другим источником в этом запуске")

type slot struct {
	mu        sync.Mutex
	path      string
	nCol      int
	hasHeader bool
}

func (s *slot) appendTo(write func(io.Writer) error) error {
	return appendFile(s.path, write, false)
}

type output struct {
	key       string // canonicalPath(path)
	path      string
	hasHeader bool
}

// Output describes a CSV published during the current run.
type Output struct {
	Path      string
	HasHeader bool
}

func NewRegistry() *Registry {
	return &Registry{
		byKey:   make(map[string]*slot),
		byDir:   make(map[string]*sync.Mutex),
		outs:    make(map[string][]output),
		written: make(map[string]struct{}),
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

// acquireUnique reserves a non-merge output path under the directory lock.
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
	r.written[pathKey] = struct{}{}
	item := output{key: pathKey, path: path, hasHeader: hasHeader}
	list := r.outs[dir]
	for i, o := range list {
		if o.key == pathKey {
			list[i] = item
			return
		}
	}
	r.outs[dir] = append(list, item)
}

func (r *Registry) isWritten(path string) bool {
	key := canonicalPath(path)
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.written[key]
	return ok
}

// OutputsIn returns CSV files published in dir during this run.
func (r *Registry) OutputsIn(dir string) []Output {
	if r == nil {
		return nil
	}
	dir = canonicalPath(dir)
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.outs[dir]
	out := make([]Output, len(list))
	for i, o := range list {
		out[i] = Output{Path: o.path, HasHeader: o.hasHeader}
	}
	return out
}

// SyncFiles makes deferred INSERT appends durable before their source is completed.
func SyncFiles(paths []string) error {
	seen := make(map[string]struct{})
	for _, path := range paths {
		key := canonicalPath(path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		if err := errors.Join(syncOutputFile(f), closeOutputFile(f)); err != nil {
			return err
		}
	}
	return nil
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
