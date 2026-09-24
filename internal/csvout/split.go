package csvout

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// SplitThreshold — максимум строк данных в одном файле без нарезки.
	SplitThreshold = 1_000_000
	// SplitChunkRows — максимум строк данных в одном куске после нарезки.
	SplitChunkRows = 500_000
)

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
)

// SetSplitLimits подменяет порог и размер куска до вызова restore.
func SetSplitLimits(threshold, chunkRows int) (restore func()) {
	prevT, prevC := splitLimitThreshold, splitLimitChunk
	splitLimitThreshold, splitLimitChunk = threshold, chunkRows
	return func() {
		splitLimitThreshold, splitLimitChunk = prevT, prevC
	}
}

// SplitIfNeeded режет path, если строк данных больше SplitThreshold.
// Файл с порогом и ниже не открывается на запись.
func SplitIfNeeded(path string, mode HeaderMode) (SplitResult, error) {
	return splitFile(path, mode, splitLimitThreshold, splitLimitChunk, nil)
}

// SplitIfNeeded режет path как csvout.SplitIfNeeded, но часть не может занять
// CSV, который этот запуск записал для другого ключа (таблица users_2 при
// нарезке users): такая нарезка — ошибка, монолит остаётся как был.
func (r *Registry) SplitIfNeeded(path string, mode HeaderMode) (SplitResult, error) {
	return splitFile(path, mode, splitLimitThreshold, splitLimitChunk, r.isWritten)
}

// taken — занятые имена частей; nil — занятых нет.
func splitFile(path string, mode HeaderMode, threshold, chunkRows int, taken func(string) bool) (SplitResult, error) {
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
	names, written, err := writeAndPublish(path, mode, chunkRows, taken)
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
	for {
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

func writeAndPublish(path string, mode HeaderMode, chunkRows int, taken func(string) bool) ([]string, int64, error) {
	dir := filepath.Dir(path)
	var (
		names []string
		temps []string
		// created — части, которых до прогона не было. Только их можно убрать
		// при провале: лежавший раньше {stem}_N.csv §14 не уничтожает.
		created  []string
		open     *os.File
		buf      *bufio.Writer
		header   []byte
		rows     int
		dataRows int64
		success  bool
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
			for _, p := range created {
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
		next := path
		if len(names) > 0 {
			next = partName(path, len(names)+1)
			if taken != nil && taken(next) {
				return fmt.Errorf("%w: %s", ErrNameTaken, filepath.Base(next))
			}
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
		existed, err := fileExists(names[i])
		if err != nil {
			return nil, 0, err
		}
		if err := replaceFile(temps[i], names[i]); err != nil {
			return nil, 0, err
		}
		temps[i] = ""
		if !existed {
			created = append(created, names[i])
		}
	}
	if err := replaceFile(temps[0], names[0]); err != nil {
		return nil, 0, err
	}
	temps[0] = ""
	success = true
	return names, dataRows, nil
}

// partName — имя n-й части (n ≥ 2) по §14: {stem}_n.csv от собственного stem
// файла. Лежащий на диске файл с этим именем заменяется частью.
func partName(path string, n int) string {
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	return filepath.Join(filepath.Dir(path), limitCSVBaseSuffix(stem, "_"+strconv.Itoa(n))+".csv")
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
