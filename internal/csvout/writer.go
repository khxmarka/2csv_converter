package csvout

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const tmpPattern = ".2csv-*.tmp"

// CleanupTemps удаляет незавершённые временные файлы прошлого аварийного
// запуска в конкретной рабочей директории.
func CleanupTemps(dir string) error {
	paths, err := filepath.Glob(filepath.Join(dir, tmpPattern))
	if err != nil {
		return err
	}
	var errs []error
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
	Path       string
	PaddedRows int
	Appended   bool
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
	s := reg.acquire(dir, base)

	tmp, err := os.CreateTemp(dir, tmpPattern)
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
	tmp, err := os.CreateTemp(dir, tmpPattern)
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

func (w *Writer) PaddedRows() int { return w.padded }

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
	if err := w.tmp.Close(); err != nil {
		w.closed = true
		w.tmp = nil
		_ = os.Remove(tmpName)
		return empty, err
	}
	w.tmp = nil
	w.closed = true

	if w.appending {
		err := appendCopy(w.slot.path, tmpName)
		_ = os.Remove(tmpName)
		if err != nil {
			return empty, err
		}
		return Result{Path: w.slot.path, PaddedRows: w.padded, Appended: true}, nil
	}

	final, err := w.place(tmpName)
	if err != nil {
		_ = os.Remove(tmpName)
		return empty, err
	}
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
	path := filepath.Join(w.dir, csvName(w.base, 0))
	if w.reg.isWritten(path) {
		return "", fmt.Errorf("%w: %s", ErrNameTaken, filepath.Base(path))
	}
	if err := replaceFile(tmpName, path); err != nil {
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
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := out.Stat()
	if err != nil {
		return errors.Join(err, out.Close())
	}
	originalSize := info.Size()
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		rollbackErr := os.Truncate(dst, originalSize)
		return errors.Join(copyErr, closeErr, rollbackErr)
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
		_ = os.Remove(name)
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
		return os.Remove(name)
	}
	return nil
}
