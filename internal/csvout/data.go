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
// Ячейку можно писать потоком (BeginRow/Write*Cell/EndRow): огромное значение
// не собирается в памяти, а после dataMemLimit уходит во временный файл.
type DataFile struct {
	dir     string
	mem     bytes.Buffer
	f       *os.File
	buf     *bufio.Writer
	size    int64 // байт строк записано (память + файл)
	scratch []byte
	rows    int
	// Ширина первой, самой узкой и самой широкой строки: по ним Commit
	// решает, копировать байты как есть или перечитывать строки.
	first, minCells, maxCells int
	rowMark                   int64
	rowCells                  int
	esc                       quoteEscaper // готовый io.Writer: без упаковки на каждую ячейку
}

// CreateData готовит приёмник строк. Временный файл в dir появится, только
// если строки не поместятся в dataMemLimit.
func CreateData(dir string) *DataFile {
	d := &DataFile{dir: dir}
	d.esc.d = d
	return d
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
	d.scratch = appendRow(d.scratch[:0], values)
	if err := d.write(d.scratch); err != nil {
		return err
	}
	d.noteRow(len(values))
	return nil
}

// BeginRow начинает потоковую строку; RollbackRow вернёт данные к этой точке.
func (d *DataFile) BeginRow() error {
	d.rowMark = d.size
	d.rowCells = 0
	return nil
}

// WriteNullCell пишет пустую ячейку "" (NULL и пропуск, §6).
func (d *DataFile) WriteNullCell() error {
	return d.WriteTextCellStream(func(io.Writer) error { return nil })
}

// WriteTextCellStream пишет ячейку, чей текст write отдаёт кусками;
// кавычки экранируются на лету (§6).
func (d *DataFile) WriteTextCellStream(write func(io.Writer) error) error {
	if d.rowCells > 0 {
		if err := d.write([]byte{','}); err != nil {
			return err
		}
	}
	if err := d.write([]byte{'"'}); err != nil {
		return err
	}
	if err := write(&d.esc); err != nil {
		return err
	}
	if err := d.write([]byte{'"'}); err != nil {
		return err
	}
	d.rowCells++
	return nil
}

// EndRow завершает потоковую строку.
func (d *DataFile) EndRow() error {
	n := d.rowCells
	if n == 0 {
		if err := d.WriteNullCell(); err != nil {
			return err
		}
		n = 1
	}
	if err := d.write([]byte{'\n'}); err != nil {
		return err
	}
	d.noteRow(n)
	return nil
}

// RollbackRow отбрасывает начатую строку (лишние значения, битый литерал).
func (d *DataFile) RollbackRow() error {
	d.rowCells = 0
	if d.f == nil {
		d.mem.Truncate(int(d.rowMark))
		d.size = d.rowMark
		return nil
	}
	if err := d.buf.Flush(); err != nil {
		return err
	}
	if err := d.f.Truncate(d.rowMark); err != nil {
		return err
	}
	if _, err := d.f.Seek(d.rowMark, io.SeekStart); err != nil {
		return err
	}
	d.size = d.rowMark
	return nil
}

// quoteEscaper удваивает '"' внутри ячейки. Байт '"' в UTF-8 не встречается
// внутри многобайтовых символов, поэтому куски можно резать где угодно.
type quoteEscaper struct{ d *DataFile }

func (q *quoteEscaper) Write(p []byte) (int, error) {
	n := len(p)
	for {
		i := bytes.IndexByte(p, '"')
		if i < 0 {
			return n, q.d.write(p)
		}
		if err := q.d.write(p[:i+1]); err != nil {
			return 0, err
		}
		if err := q.d.write([]byte{'"'}); err != nil {
			return 0, err
		}
		p = p[i+1:]
	}
}

func (d *DataFile) noteRow(n int) {
	if d.rows == 0 {
		d.first, d.minCells, d.maxCells = n, n, n
	}
	d.minCells = min(d.minCells, n)
	d.maxCells = max(d.maxCells, n)
	d.rows++
}

func (d *DataFile) write(p []byte) error {
	if d.f == nil && d.mem.Len()+len(p) > dataMemLimit {
		if err := d.spill(); err != nil {
			return err
		}
	}
	var err error
	if d.f != nil {
		_, err = d.buf.Write(p)
	} else {
		_, err = d.mem.Write(p)
	}
	d.size += int64(len(p))
	return err
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
// Строки шире ключа пропускаются по одной (§6), их число — Result.SkippedRows.
// Если не осталось ни одной строки — CSV не трогается, ErrTooManyValues.
// Если все строки ровно по ширине ключа, байты копируются без повторного разбора.
func CommitPrepared(reg *Registry, dir, table string, columns []string, data *DataFile) (Result, error) {
	var empty Result
	base := limitCSVBase(FileBase(table))
	s := reg.acquire(dir, base)
	if s.path != "" {
		defer s.mu.Unlock()
		src, err := data.open()
		if err != nil {
			return empty, err
		}
		var wrote, skipped int
		if err := s.appendTo(func(w io.Writer) error {
			wrote, skipped, err = copyRows(w, src, s.nCol, data)
			return err
		}); err != nil {
			return empty, err
		}
		if wrote == 0 {
			return Result{SkippedRows: skipped}, ErrTooManyValues
		}
		return Result{Path: s.path, Appended: true, SkippedRows: skipped}, nil
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
	src, err := data.open()
	if err != nil {
		return empty, err
	}
	wrote, skipped, err := copyRows(w.buf, src, w.nCol, data)
	if err != nil {
		return empty, err
	}
	if wrote == 0 {
		return Result{SkippedRows: skipped}, ErrTooManyValues
	}
	res, err := w.Commit()
	res.SkippedRows = skipped
	return res, err
}

// CommitPlain публикует таблицу листа: header — первая строка, data —
// остальные. Ширина = максимум колонок среди шапки и строк (§13), всё
// короче дополняется "" . Ширина известна только после всех строк, поэтому
// лист читается один раз в data, а шапка дописывается в конце.
func CommitPlain(reg *Registry, dir, base string, header []string, data *DataFile) (Result, error) {
	var empty Result
	width := len(header)
	if data.rows > 0 {
		width = max(width, data.maxCells)
	}
	w, err := CreatePlain(reg, dir, base, padTo(header, max(width, 1)))
	if err != nil {
		return empty, err
	}
	defer func() { _ = w.Abort() }()
	if data.rows > 0 {
		src, err := data.open()
		if err != nil {
			return empty, err
		}
		if _, _, err := copyRows(w.buf, src, w.nCol, data); err != nil {
			return empty, err
		}
	}
	return w.Commit()
}

func padTo(row []string, n int) []string {
	if len(row) >= n {
		return row
	}
	out := make([]string, n)
	copy(out, row)
	return out
}

// copyRows переносит строки data в dst по ширине width. Все строки ровно
// width — байты как есть. Иначе строки перечитываются: короче — дополняются
// пустыми ячейками, шире — пропускаются (skipped).
func copyRows(dst io.Writer, src io.Reader, width int, data *DataFile) (wrote, skipped int, err error) {
	if data.minCells == width && data.maxCells == width {
		_, err := io.Copy(dst, src)
		return data.rows, 0, err
	}
	bw := bufio.NewWriterSize(dst, 64*1024)
	r := csv.NewReader(src)
	r.FieldsPerRecord = -1
	r.ReuseRecord = true
	row := make([]string, width)
	var line []byte
	for {
		rec, err := r.Read()
		if err == io.EOF {
			return wrote, skipped, bw.Flush()
		}
		if err != nil {
			return wrote, skipped, err
		}
		if len(rec) > width {
			skipped++
			continue
		}
		clear(row)
		copy(row, rec)
		line = appendRow(line[:0], row)
		if _, err := bw.Write(line); err != nil {
			return wrote, skipped, err
		}
		wrote++
	}
}
