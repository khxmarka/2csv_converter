package csvout

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maxNameIndex = 1_000_000

// Writer пишет один INSERT во временный файл. После Commit: если CSV ключа
// ещё нет — переименовывает temp в свободное {table}.csv / {table}(n).csv;
// если ключ уже открыт в этом запуске — дописывает только строки данных.
type Writer struct {
	slot      *slot
	dir       string
	base      string
	nCol      int
	tmp       *os.File
	buf       *bufio.Writer
	padded    int
	closed    bool
	appending bool
}

// Result — итог успешного Commit.
type Result struct {
	Path       string
	PaddedRows int
	Appended   bool
}

// Create открывает временный файл в dir. Заголовок пишется только если это
// первый успешный INSERT ключа (слот ещё без пути). Иначе колонки игнорируются,
// ширина берётся из заголовка, в temp идут только строки данных.
func Create(reg *Registry, dir, table string, columns []string) (*Writer, error) {
	if reg == nil {
		return nil, fmt.Errorf("csvout: нужен Registry")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	base := FileBase(table)
	s := reg.acquire(dir, base)

	tmp, err := os.CreateTemp(dir, ".sql2csv-*.tmp")
	if err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("временный CSV: %w", err)
	}
	w := &Writer{
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
	w.nCol = len(columns)
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
	return Result{Path: final, PaddedRows: w.padded}, nil
}

func (w *Writer) place(tmpName string) (string, error) {
	for n := 0; n < maxNameIndex; n++ {
		path := filepath.Join(w.dir, csvName(w.base, n))
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", err
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(path)
			return "", err
		}
		if err := os.Remove(path); err != nil {
			return "", err
		}
		if err := os.Rename(tmpName, path); err != nil {
			return "", err
		}
		return path, nil
	}
	return "", fmt.Errorf("нет свободного имени для %s.csv в %s", w.base, w.dir)
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
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
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
