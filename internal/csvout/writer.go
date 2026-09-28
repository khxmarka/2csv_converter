package csvout

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	tmpDirName = ".2csv-tmp"
	tmpPattern = "output-*.tmp"
	tmpOwner   = "2csv temporary files\n"
)

var tempMu sync.Mutex

var (
	syncOutputFile    = (*os.File).Sync
	closeOutputFile   = (*os.File).Close
	publishOutputFile = replaceFile
)

func CleanupTemps(dir string) error {
	tempMu.Lock()
	defer tempMu.Unlock()
	root := filepath.Join(dir, tmpDirName)
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("csvout: %s занят не каталогом", root)
	}
	owner, err := os.ReadFile(filepath.Join(root, ".owner"))
	if err != nil || string(owner) != tmpOwner {
		return fmt.Errorf("csvout: %s не принадлежит 2csv", root)
	}
	paths, err := filepath.Glob(filepath.Join(root, tmpPattern))
	if err != nil {
		return err
	}
	var errs []error
	for _, path := range paths {
		entry, err := os.Lstat(path)
		if err == nil && entry.Mode().IsRegular() {
			err = os.Remove(path)
		}
		if err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		entries, readErr := os.ReadDir(root)
		if readErr != nil {
			errs = append(errs, readErr)
		} else if len(entries) == 1 && entries[0].Name() == ".owner" {
			if err := os.Remove(filepath.Join(root, ".owner")); err != nil {
				errs = append(errs, err)
			} else if err := os.Remove(root); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func createTemp(dir string) (*os.File, error) {
	tempMu.Lock()
	defer tempMu.Unlock()
	root := filepath.Join(dir, tmpDirName)
	created := false
	if err := os.Mkdir(root, 0o700); err == nil {
		created = true
	} else if !os.IsExist(err) {
		return nil, err
	}
	ownerPath := filepath.Join(root, ".owner")
	if created {
		if err := os.WriteFile(ownerPath, []byte(tmpOwner), 0o600); err != nil {
			_ = os.Remove(root)
			return nil, err
		}
	} else {
		owner, err := os.ReadFile(ownerPath)
		if err != nil || string(owner) != tmpOwner {
			return nil, fmt.Errorf("csvout: %s не принадлежит 2csv", root)
		}
	}
	return os.CreateTemp(root, tmpPattern)
}

func removeTemp(path string) error {
	err := os.Remove(path)
	_ = pruneTempDir(filepath.Dir(filepath.Dir(path)))
	return err
}

func pruneTempDir(dir string) error {
	tempMu.Lock()
	defer tempMu.Unlock()
	root := filepath.Join(dir, tmpDirName)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].Name() != ".owner" {
		return nil
	}
	if err := os.Remove(filepath.Join(root, ".owner")); err != nil {
		return err
	}
	return os.Remove(root)
}

// Writer пишет один INSERT во временный файл. После Commit: если CSV ключа
// ещё нет — заменяет целевой {table}.csv содержимым temp;
// если ключ уже открыт в этом запуске — дописывает только строки данных.
type Writer struct {
	reg         *Registry
	slot        *slot
	dir         string
	base        string
	nCol        int
	tmp         *os.File
	buf         *bufio.Writer
	padded      int
	closed      bool
	appending   bool
	wroteHeader bool
}

// Result — итог успешного Commit.
type Result struct {
	Path        string
	PaddedRows  int
	Appended    bool
	SkippedRows int // строки шире ключа, пропущенные при влитии
}

// Create открывает временный файл в dir. Заголовок пишется только если это
// первый успешный INSERT ключа (слот ещё без пути) и список колонок не пуст.
// Иначе колонки игнорируются, ширина берётся из заголовка или первой строки
// VALUES, в temp идут только строки данных.
func Create(reg *Registry, dir, table string, columns []string) (*Writer, error) {
	if reg == nil {
		return nil, fmt.Errorf("csvout: нужен Registry")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	base := limitCSVBase(FileBase(table))
	return newWriter(reg, reg.acquire(dir, base), dir, base, columns)
}

// newWriter открывает temp для уже захваченного слота s. При ошибке слот
// отпускается.
func newWriter(reg *Registry, s *slot, dir, base string, columns []string) (*Writer, error) {
	tmp, err := createTemp(dir)
	if err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("временный CSV: %w", err)
	}
	w := &Writer{
		reg:  reg,
		slot: s,
		dir:  dir,
		base: base,
		tmp:  tmp,
		buf:  bufio.NewWriterSize(tmp, 64*1024),
	}
	if s.path != "" {
		w.appending = true
		w.nCol = s.nCol
		return w, nil
	}
	if len(columns) == 0 {
		return w, nil
	}
	w.nCol = len(columns)
	w.wroteHeader = true
	if _, err := io.WriteString(w.buf, encodeRow(columns)); err != nil {
		_ = w.Abort()
		return nil, err
	}
	return w, nil
}

// CreatePlain открывает CSV со строкой заголовка и без склейки с INSERT.
// base уже санитайзнут; при Commit целевой {base}.csv заменяется.
func CreatePlain(reg *Registry, dir, base string, columns []string) (*Writer, error) {
	if reg == nil {
		return nil, fmt.Errorf("csvout: нужен Registry")
	}
	if len(columns) < 1 {
		return nil, fmt.Errorf("csvout: ширина листа должна быть ≥ 1")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	base = limitCSVBase(base)
	if base == "" {
		base = "table"
	}
	s := reg.acquireUnique()
	tmp, err := createTemp(dir)
	if err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("временный CSV: %w", err)
	}
	w := &Writer{
		reg:         reg,
		slot:        s,
		dir:         dir,
		base:        base,
		nCol:        len(columns),
		tmp:         tmp,
		buf:         bufio.NewWriterSize(tmp, 64*1024),
		wroteHeader: true,
	}
	if _, err := io.WriteString(w.buf, encodeRow(columns)); err != nil {
		_ = w.Abort()
		return nil, err
	}
	return w, nil
}

// NCol — ширина заголовка, с которой сверяется арность VALUES.
func (w *Writer) NCol() int {
	if w == nil {
		return 0
	}
	return w.nCol
}

// Row пишет одну строку данных. values уже нормализованы до nCol элементов.
func (w *Writer) Row(values []string) error {
	if w == nil || w.closed {
		return fmt.Errorf("csvout: запись в закрытый Writer")
	}
	if w.nCol == 0 {
		w.nCol = len(values)
	}
	if len(values) > w.nCol {
		return ErrTooManyValues
	}
	if len(values) < w.nCol {
		padded := make([]string, w.nCol)
		copy(padded, values)
		values = padded
		w.padded++
	}
	_, err := io.WriteString(w.buf, encodeRow(values))
	return err
}

// Commit вливает временный файл в итоговый CSV и снимает блокировку ключа.
func (w *Writer) Commit() (Result, error) {
	var empty Result
	if w == nil || w.closed {
		return empty, fmt.Errorf("csvout: Commit по закрытому Writer")
	}
	defer w.unlock()

	if err := w.buf.Flush(); err != nil {
		w.closed = true
		w.removeTmp()
		return empty, err
	}
	tmpName := w.tmp.Name()
	if err := syncOutputFile(w.tmp); err != nil {
		w.closed = true
		w.removeTmp()
		return empty, err
	}
	if err := closeOutputFile(w.tmp); err != nil {
		w.closed = true
		w.tmp = nil
		_ = removeTemp(tmpName)
		return empty, err
	}
	w.tmp = nil
	w.closed = true

	if w.appending {
		err := appendCopy(w.slot.path, tmpName)
		_ = removeTemp(tmpName)
		if err != nil {
			return empty, err
		}
		return Result{Path: w.slot.path, PaddedRows: w.padded, Appended: true}, nil
	}

	final, err := w.place(tmpName)
	if err != nil {
		_ = removeTemp(tmpName)
		return empty, err
	}
	_ = pruneTempDir(w.dir)
	w.slot.path = final
	w.slot.nCol = w.nCol
	w.slot.hasHeader = w.wroteHeader
	return Result{Path: final, PaddedRows: w.padded}, nil
}

// place публикует первый CSV ключа. Проверка занятости и регистрация идут
// под одним lockDir: иначе два ключа с одним именем проскочили бы оба.
func (w *Writer) place(tmpName string) (string, error) {
	dmu := w.reg.lockDir(w.dir)
	defer dmu.Unlock()
	path := filepath.Join(w.dir, w.base+".csv")
	if w.reg.isWritten(path) {
		return "", fmt.Errorf("%w: %s", ErrNameTaken, filepath.Base(path))
	}
	if err := publishOutputFile(tmpName, path); err != nil {
		return "", err
	}
	w.reg.addOutput(w.dir, path, w.wroteHeader)
	return path, nil
}

func appendCopy(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return appendTo(dst, func(w io.Writer) error {
		_, err := io.Copy(w, in)
		return err
	})
}

// appendTo дописывает в конец dst то, что пишет write. Провал откатывает
// dst к исходному размеру: уже записанный CSV ключа не портится (§6).
func appendTo(dst string, write func(io.Writer) error) error {
	return appendFile(dst, write, true)
}

func appendFile(dst string, write func(io.Writer) error, durable bool) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := out.Stat()
	if err != nil {
		return errors.Join(err, out.Close())
	}
	originalSize := info.Size()
	bw := bufio.NewWriter(out)
	writeErr := write(bw)
	if writeErr == nil {
		writeErr = bw.Flush()
	}
	if writeErr == nil && durable {
		writeErr = syncOutputFile(out)
	}
	closeErr := closeOutputFile(out)
	if writeErr != nil || closeErr != nil {
		rollbackErr := os.Truncate(dst, originalSize)
		return errors.Join(writeErr, closeErr, rollbackErr)
	}
	return nil
}

func (w *Writer) unlock() {
	if w == nil || w.slot == nil {
		return
	}
	w.slot.mu.Unlock()
	w.slot = nil
}

func (w *Writer) removeTmp() {
	if w.tmp == nil {
		return
	}
	name := w.tmp.Name()
	_ = w.tmp.Close()
	w.tmp = nil
	if name != "" {
		_ = removeTemp(name)
	}
}

// Abort удаляет временный файл и снимает блокировку ключа.
// Уже записанный CSV ключа не трогает.
func (w *Writer) Abort() error {
	if w == nil || w.closed {
		return nil
	}
	w.closed = true
	defer w.unlock()
	var name string
	if w.tmp != nil {
		name = w.tmp.Name()
		_ = w.tmp.Close()
		w.tmp = nil
	}
	if name != "" {
		return removeTemp(name)
	}
	return nil
}
