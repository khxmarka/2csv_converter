package csvout

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	// SplitThreshold — максимум строк данных в одном файле без нарезки.
	SplitThreshold = 1_000_000
	// SplitChunkRows — максимум строк данных в одном куске после нарезки.
	SplitChunkRows = 500_000
	// MaxRecordBytes — потолок одной CSV-записи при нарезке (защита от OOM).
	MaxRecordBytes = 64 << 20
)

// ErrRecordTooLarge — одна запись CSV длиннее MaxRecordBytes.
var ErrRecordTooLarge = errors.New("запись CSV длиннее допустимого")

// HeaderMode задаёт, есть ли в CSV строка заголовка.
type HeaderMode int

const (
	// SplitNoHeader — все записи файла являются данными (INSERT без списка колонок).
	SplitNoHeader HeaderMode = iota
	// SplitWithHeader — первая непустая запись является заголовком и копируется в каждый кусок.
	SplitWithHeader
)

// SplitResult — итог нарезки одного файла.
type SplitResult struct {
	Split    bool
	DataRows int64
	// Parts — пути кусков в порядке записи. Заполнен только когда Split.
	Parts []string
}

// Порог боя. Тесты подменяют его через SetSplitLimits и обязаны вернуть прежние значения.
var (
	splitLimitThreshold = SplitThreshold
	splitLimitChunk     = SplitChunkRows
	splitLimitRecord    = MaxRecordBytes
)

// SetSplitLimits подменяет порог и размер куска до вызова restore.
func SetSplitLimits(threshold, chunkRows int) (restore func()) {
	prevT, prevC := splitLimitThreshold, splitLimitChunk
	splitLimitThreshold, splitLimitChunk = threshold, chunkRows
	return func() {
		splitLimitThreshold, splitLimitChunk = prevT, prevC
	}
}

// SetMaxRecordBytes подменяет потолок записи для тестов.
func SetMaxRecordBytes(n int) (restore func()) {
	prev := splitLimitRecord
	splitLimitRecord = n
	return func() { splitLimitRecord = prev }
}

// SplitIfNeeded режет path, если строк данных больше SplitThreshold.
// Файл с порогом и ниже не открывается на запись.
func SplitIfNeeded(path string, mode HeaderMode) (SplitResult, error) {
	return splitFile(path, mode, splitLimitThreshold, splitLimitChunk)
}

func splitFile(path string, mode HeaderMode, threshold, chunkRows int) (SplitResult, error) {
	if threshold < 1 || chunkRows < 1 {
		return SplitResult{}, fmt.Errorf("csvout: неверный порог нарезки")
	}
	if err := rejectSplitPath(path); err != nil {
		return SplitResult{}, err
	}
	dataRows, over, err := countDataRowsUntil(path, mode, int64(threshold))
	if err != nil {
		return SplitResult{}, err
	}
	if !over {
		return SplitResult{DataRows: dataRows}, nil
	}
	names, written, err := writeAndPublish(path, mode, chunkRows)
	if err != nil {
		return SplitResult{}, err
	}
	return SplitResult{Split: true, DataRows: written, Parts: names}, nil
}

func rejectSplitPath(path string) error {
	base := filepath.Base(path)
	if strings.EqualFold(base, "converted.txt") {
		return fmt.Errorf("csvout: %s не нарезается", base)
	}
	lower := strings.ToLower(base)
	if strings.HasPrefix(lower, ".2csv-") && strings.HasSuffix(lower, ".tmp") {
		return fmt.Errorf("csvout: %s не нарезается", base)
	}
	if !strings.EqualFold(filepath.Ext(base), ".csv") {
		return fmt.Errorf("csvout: %s не csv", base)
	}
	return nil
}

func countDataRows(path string, mode HeaderMode) (int64, error) {
	n, _, err := countDataRowsUntil(path, mode, -1)
	return n, err
}

var errNeedSplit = errors.New("csvout: нужно нарезать")

// countDataRowsUntil считает строки данных. Если limit ≥ 0, останавливается
// сразу после limit+1 — для решения «резать / не резать» весь файл читать не нужно.
func countDataRowsUntil(path string, mode HeaderMode, limit int64) (int64, bool, error) {
	var n int64
	err := walkRecords(path, mode, func(_ []byte, header bool) error {
		if header {
			return nil
		}
		n++
		if limit >= 0 && n > limit {
			return errNeedSplit
		}
		return nil
	})
	if errors.Is(err, errNeedSplit) {
		return n, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	return n, false, nil
}

// walkRecords отдаёт заголовок и строки данных. Пустые хвостовые записи не отдаёт.
// Записи до первой непустой в режиме заголовка тоже пропускаются.
// Срез rec действителен только на время вызова fn.
func walkRecords(path string, mode HeaderMode, fn func(rec []byte, header bool) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	rr := recordReader{br: bufio.NewReaderSize(f, 256*1024)}
	var pending [][]byte
	haveHeader := mode == SplitNoHeader
	for {
		rec, err := rr.next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if csvRecordEmpty(rec) {
			if !haveHeader {
				continue
			}
			pending = append(pending, append([]byte(nil), rec...))
			continue
		}
		if !haveHeader {
			haveHeader = true
			if err := fn(rec, true); err != nil {
				return err
			}
			continue
		}
		for _, p := range pending {
			if err := fn(p, false); err != nil {
				return err
			}
		}
		pending = pending[:0]
		if err := fn(rec, false); err != nil {
			return err
		}
	}
}

type recordReader struct {
	br  *bufio.Reader
	buf []byte
}

func (r *recordReader) next() ([]byte, error) {
	r.buf = r.buf[:0]
	inQuotes := false
	limit := splitLimitRecord
	if limit < 1 {
		limit = MaxRecordBytes
	}
	for {
		if len(r.buf) > limit {
			return nil, fmt.Errorf("%w: %d байт", ErrRecordTooLarge, len(r.buf))
		}
		b, err := r.br.ReadByte()
		if err == io.EOF {
			if len(r.buf) == 0 {
				return nil, io.EOF
			}
			return r.buf, nil
		}
		if err != nil {
			return nil, err
		}
		r.buf = append(r.buf, b)
		if inQuotes {
			if b != '"' {
				continue
			}
			next, err := r.br.ReadByte()
			if err == io.EOF {
				inQuotes = false
				continue
			}
			if err != nil {
				return nil, err
			}
			if next == '"' {
				r.buf = append(r.buf, next)
				continue
			}
			if err := r.br.UnreadByte(); err != nil {
				return nil, err
			}
			inQuotes = false
			continue
		}
		switch b {
		case '"':
			inQuotes = true
		case '\n':
			return r.buf, nil
		case '\r':
			next, err := r.br.ReadByte()
			if err == io.EOF {
				return r.buf, nil
			}
			if err != nil {
				return nil, err
			}
			if next == '\n' {
				r.buf = append(r.buf, next)
				return r.buf, nil
			}
			if err := r.br.UnreadByte(); err != nil {
				return nil, err
			}
			return r.buf, nil
		}
	}
}

func csvRecordEmpty(raw []byte) bool {
	s := raw
	if len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '\r' {
		s = s[:len(s)-1]
	}
	for _, b := range s {
		if b != ' ' && b != '\t' && b != '\v' && b != '\f' {
			return false
		}
	}
	return true
}

func writeAndPublish(path string, mode HeaderMode, chunkRows int) ([]string, int64, error) {
	dir := filepath.Dir(path)
	namer := newPartNamer(path)
	var (
		names     []string
		temps     []string
		published []string
		open      *os.File
		buf       *bufio.Writer
		header    []byte
		rows      int
		dataRows  int64
		success   bool
	)
	defer func() {
		if open != nil {
			name := open.Name()
			_ = open.Close()
			_ = os.Remove(name)
		}
		for _, t := range temps {
			if t != "" {
				_ = os.Remove(t)
			}
		}
		if !success {
			for _, p := range published {
				_ = os.Remove(p)
			}
		}
	}()

	openNext := func() error {
		if buf != nil {
			if err := buf.Flush(); err != nil {
				return err
			}
			name := open.Name()
			if err := open.Close(); err != nil {
				_ = os.Remove(name)
				open = nil
				buf = nil
				return err
			}
			open = nil
			buf = nil
			temps = append(temps, name)
		}
		next, err := namer.next(len(names) == 0)
		if err != nil {
			return err
		}
		names = append(names, next)
		f, err := os.CreateTemp(dir, tmpPattern)
		if err != nil {
			return err
		}
		open = f
		buf = bufio.NewWriterSize(f, 256*1024)
		rows = 0
		if len(header) > 0 {
			if _, err := buf.Write(header); err != nil {
				return err
			}
		}
		return nil
	}

	err := walkRecords(path, mode, func(rec []byte, isHeader bool) error {
		if isHeader {
			header = append([]byte(nil), rec...)
			return nil
		}
		dataRows++
		if buf == nil || rows == chunkRows {
			if err := openNext(); err != nil {
				return err
			}
		}
		if _, err := buf.Write(rec); err != nil {
			return err
		}
		rows++
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if buf == nil {
		return nil, 0, fmt.Errorf("csvout: нарезка не записала данные")
	}
	if err := buf.Flush(); err != nil {
		return nil, 0, err
	}
	last := open.Name()
	if err := open.Close(); err != nil {
		_ = os.Remove(last)
		open = nil
		buf = nil
		return nil, 0, err
	}
	open = nil
	buf = nil
	temps = append(temps, last)
	if len(names) < 2 || len(temps) != len(names) {
		return nil, 0, fmt.Errorf("csvout: нарезка собрала %d частей", len(names))
	}

	for i := 1; i < len(names); i++ {
		if err := replaceFile(temps[i], names[i]); err != nil {
			return nil, 0, err
		}
		temps[i] = ""
		published = append(published, names[i])
	}
	if err := replaceFile(temps[0], names[0]); err != nil {
		return nil, 0, err
	}
	temps[0] = ""
	success = true
	return names, dataRows, nil
}

type partNamer struct {
	dir   string
	path  string
	root  string
	n     int
	taken map[string]struct{}
}

func newPartNamer(path string) *partNamer {
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	root, n := suffixStart(stem)
	return &partNamer{
		dir:   filepath.Dir(path),
		path:  path,
		root:  root,
		n:     n,
		taken: map[string]struct{}{foldKey(path): {}},
	}
}

func (p *partNamer) next(first bool) (string, error) {
	if first {
		return p.path, nil
	}
	for {
		if p.n <= 0 {
			return "", fmt.Errorf("csvout: не удалось подобрать имя части")
		}
		name := limitCSVBaseSuffix(p.root, "_"+strconv.Itoa(p.n)) + ".csv"
		p.n++
		full := filepath.Join(p.dir, name)
		key := foldKey(full)
		if _, ok := p.taken[key]; ok {
			continue
		}
		exists, err := fileExists(full)
		if err != nil {
			return "", err
		}
		p.taken[key] = struct{}{}
		if !exists {
			return full, nil
		}
	}
}

func suffixStart(stem string) (base string, n int) {
	i := strings.LastIndex(stem, "_")
	if i <= 0 || i == len(stem)-1 {
		return stem, 2
	}
	digits := stem[i+1:]
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return stem, 2
	}
	v, err := strconv.Atoi(digits)
	if err != nil || strconv.Itoa(v) != digits {
		return stem, 2
	}
	return stem[:i], v + 1
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func foldKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}
