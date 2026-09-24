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

// Registry держит слоты склейки: ключ = (директория .sql, FileBase таблицы).
// Слот сериализует первое создание файла и последующие дописывания.
type Registry struct {
	mu    sync.Mutex
	byKey map[string]*slot
	byDir map[string]*sync.Mutex
	// slotsIn — слоты склейки по canonicalPath директории, для CloseDir.
	slotsIn map[string][]*slot
	// outs — CSV этого запуска по canonicalPath директории. Map, а не общий
	// список: иначе каждый новый CSV сканировал бы все CSV запуска (O(n²)).
	outs map[string][]output
	// written — canonicalPath каждого CSV, записанного в этом запуске.
	written map[string]struct{}
}

// ErrNameTaken — целевое имя уже занято CSV этого запуска от другого ключа.
// Замена затёрла бы результат, исходник которого потом удаляется (§15).
var ErrNameTaken = errors.New("имя CSV уже занято другим источником в этом запуске")

type slot struct {
	mu        sync.Mutex
	path      string
	nCol      int
	hasHeader bool
	// app — открытый на дописывание path. Живёт между INSERT одного .sql,
	// чтобы не открывать CSV на каждый INSERT; закрывает CloseDir.
	app *os.File
}

// appendTo дописывает в path ключа через кэшированный дескриптор. Провал
// откатывает файл к прежнему размеру (§6). Truncate — по пути: у дескриптора
// O_APPEND в Windows нет права менять длину файла.
func (s *slot) appendTo(write func(io.Writer) error) error {
	if s.app == nil {
		f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		s.app = f
	}
	info, err := s.app.Stat()
	if err != nil {
		return err
	}
	if err := write(s.app); err != nil {
		return errors.Join(err, os.Truncate(s.path, info.Size()))
	}
	return nil
}

func (s *slot) closeApp() error {
	if s.app == nil {
		return nil
	}
	err := s.app.Close()
	s.app = nil
	return err
}

type output struct {
	key       string // canonicalPath(path)
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
		byKey:   make(map[string]*slot),
		byDir:   make(map[string]*sync.Mutex),
		slotsIn: make(map[string][]*slot),
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
		d := canonicalPath(dir)
		r.slotsIn[d] = append(r.slotsIn[d], s)
	}
	r.mu.Unlock()
	s.mu.Lock()
	return s
}

// CloseDir закрывает дескрипторы дописывания CSV директории dir. Вызывать,
// когда в dir больше не пишут INSERT этого файла: до нарезки и замены CSV
// (в Windows открытый файл не переименовать) и до конца запуска.
func (r *Registry) CloseDir(dir string) error {
	r.mu.Lock()
	slots := r.slotsIn[canonicalPath(dir)]
	r.mu.Unlock()
	var errs []error
	for _, s := range slots {
		s.mu.Lock()
		errs = append(errs, s.closeApp())
		s.mu.Unlock()
	}
	return errors.Join(errs...)
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

// OutputsIn возвращает CSV, которые этот запуск записал в dir.
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
