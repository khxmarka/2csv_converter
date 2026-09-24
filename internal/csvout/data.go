package csvout

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"io"
	"os"
)

// dataMemLimit — сколько байт строк одного INSERT держать в памяти. Типичный
// INSERT (одна строка или пачка mysqldump) целиком в памяти: без временного
// файла на каждый INSERT. Больше — выгрузка во временный файл (§9).
const dataMemLimit = 1 << 20

// DataFile — строки данных одного INSERT, без заголовка, уже в формате §6.
// Строки хранятся без дополнения: ширину ключа (§6) применяет CommitPrepared.
type DataFile struct {
	dir     string
	mem     bytes.Buffer
	f       *os.File
	buf     *bufio.Writer
	scratch []byte
	rows    int
	// Ширина первой, самой узкой и самой широкой строки: по ним Commit
	// решает, копировать байты как есть или дополнять строки до ключа.
	first, minCells, maxCells int
}

// CreateData готовит приёмник строк. Временный файл в dir появится, только
// если строки не поместятся в dataMemLimit.
func CreateData(dir string) *DataFile {
	return &DataFile{dir: dir}
}

func (d *DataFile) Rows() int {
	if d == nil {
		return 0
	}
	return d.rows
}

// Row пишет одну строку данных как есть. Пустая строка VALUES () становится
// одной пустой ячейкой: пустую запись CSV-ридер при Commit пропустил бы.
func (d *DataFile) Row(values []string) error {
	if len(values) == 0 {
		values = []string{""}
	}
	n := len(values)
	if d.rows == 0 {
		d.first, d.minCells, d.maxCells = n, n, n
	}
	d.minCells = min(d.minCells, n)
	d.maxCells = max(d.maxCells, n)
	d.scratch = appendRow(d.scratch[:0], values)
	if d.f == nil && d.mem.Len()+len(d.scratch) > dataMemLimit {
		if err := d.spill(); err != nil {
			return err
		}
	}
	var err error
	if d.f != nil {
		_, err = d.buf.Write(d.scratch)
	} else {
		_, err = d.mem.Write(d.scratch)
	}
	if err != nil {
		return err
	}
	d.rows++
	return nil
}

func (d *DataFile) spill() error {
	f, err := os.CreateTemp(d.dir, tmpPattern)
	if err != nil {
		return err
	}
	d.f = f
	d.buf = bufio.NewWriterSize(f, 256*1024)
	_, err = d.buf.Write(d.mem.Bytes())
	d.mem = bytes.Buffer{}
	return err
}

// Finish сбрасывает буфер временного файла, если он есть.
func (d *DataFile) Finish() error {
	if d == nil || d.f == nil {
		return nil
	}
	return d.buf.Flush()
}

// open отдаёт записанные строки с начала.
func (d *DataFile) open() (io.Reader, error) {
	if d.f == nil {
		return bytes.NewReader(d.mem.Bytes()), nil
	}
	if _, err := d.f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return bufio.NewReaderSize(d.f, 256*1024), nil
}

// Abort освобождает память и удаляет временный файл, если он был.
func (d *DataFile) Abort() {
	if d == nil {
		return
	}
	d.mem = bytes.Buffer{}
	if d.f != nil {
		name := d.f.Name()
		_ = d.f.Close()
		_ = os.Remove(name)
		d.f = nil
	}
}

// CommitPrepared вливает подготовленные строки данных в ключ склейки.
// Заголовок берётся из columns только если это первый успешный INSERT ключа.
// Если все строки ровно по ширине ключа, байты копируются без повторного
// разбора; строки короче ключа дополняются пустыми ячейками (§6).
func CommitPrepared(reg *Registry, dir, table string, columns []string, data *DataFile) (Result, error) {
	var empty Result
	base := limitCSVBase(FileBase(table))
	s := reg.acquire(dir, base)
	if s.path != "" {
		defer s.mu.Unlock()
		if data.maxCells > s.nCol {
			return empty, ErrTooManyValues
		}
		src, err := data.open()
		if err != nil {
			return empty, err
		}
		if err := s.appendTo(func(w io.Writer) error {
			return copyRows(w, src, s.nCol, data.minCells < s.nCol)
		}); err != nil {
			return empty, err
		}
		return Result{Path: s.path, Appended: true}, nil
	}

	w, err := newWriter(reg, s, dir, base, columns)
	if err != nil {
		return empty, err
	}
	// newWriter держит слот. Паника или выход без Commit/Abort оставили бы
	// ключ заблокированным навсегда — следующая запись в таблицу зависла бы.
	defer func() { _ = w.Abort() }()
	if w.nCol == 0 {
		w.nCol = data.first
	}
	if data.maxCells > w.nCol {
		return empty, ErrTooManyValues
	}
	src, err := data.open()
	if err != nil {
		return empty, err
	}
	if err := copyRows(w.buf, src, w.nCol, data.minCells < w.nCol); err != nil {
		return empty, err
	}
	return w.Commit()
}

// copyRows переносит строки DataFile в dst. Без pad — байты как есть;
// с pad — строки перечитываются и дополняются до width пустыми ячейками.
func copyRows(dst io.Writer, src io.Reader, width int, pad bool) error {
	if !pad {
		_, err := io.Copy(dst, src)
		return err
	}
	bw := bufio.NewWriterSize(dst, 64*1024)
	if err := padRows(bw, src, width); err != nil {
		return err
	}
	return bw.Flush()
}

func padRows(dst io.Writer, src io.Reader, width int) error {
	r := csv.NewReader(src)
	r.FieldsPerRecord = -1
	r.ReuseRecord = true
	row := make([]string, width)
	var line []byte
	for {
		rec, err := r.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		clear(row)
		copy(row, rec)
		line = appendRow(line[:0], row)
		if _, err := dst.Write(line); err != nil {
			return err
		}
	}
}
