package csvout

import (
	"bufio"
	"encoding/csv"
	"io"
	"os"
)

// DataFile — временный файл только со строками данных одного INSERT, без заголовка.
type DataFile struct {
	f        *os.File
	buf      *bufio.Writer
	width    int
	rows     int
	path     string
	rowMark  int64
	rowCells int
	rowOpen  bool
}

// CreateData открывает временный файл в dir.
func CreateData(dir string) (*DataFile, error) {
	f, err := os.CreateTemp(dir, tmpPattern)
	if err != nil {
		return nil, err
	}
	return &DataFile{
		f:    f,
		buf:  bufio.NewWriterSize(f, 64*1024),
		path: f.Name(),
	}, nil
}

func (d *DataFile) Width() int {
	if d == nil {
		return 0
	}
	return d.width
}

func (d *DataFile) Rows() int {
	if d == nil {
		return 0
	}
	return d.rows
}

func (d *DataFile) Path() string {
	if d == nil {
		return ""
	}
	return d.path
}

// Row пишет одну уже нормализованную строку данных.
func (d *DataFile) Row(values []string) error {
	if d == nil || d.f == nil {
		return os.ErrClosed
	}
	if d.width == 0 {
		d.width = len(values)
	}
	if len(values) > d.width {
		return ErrTooManyValues
	}
	if len(values) < d.width {
		padded := make([]string, d.width)
		copy(padded, values)
		values = padded
	}
	if _, err := io.WriteString(d.buf, encodeRow(values)); err != nil {
		return err
	}
	d.rows++
	return nil
}

// BeginRow фиксирует позицию перед потоковой записью строки.
func (d *DataFile) BeginRow() error {
	if d == nil || d.f == nil {
		return os.ErrClosed
	}
	if err := d.buf.Flush(); err != nil {
		return err
	}
	off, err := d.f.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	d.rowMark = off
	d.rowCells = 0
	d.rowOpen = true
	return nil
}

// WriteNullCell пишет пустую ячейку в текущую потоковую строку.
func (d *DataFile) WriteNullCell() error {
	return d.writeCell(func() error { return WriteNullField(d.buf) })
}

// WriteTextCell пишет текстовую ячейку целиком.
func (d *DataFile) WriteTextCell(s string) error {
	return d.writeCell(func() error { return WriteField(d.buf, s) })
}

// WriteTextCellStream пишет текстовую ячейку, копируя байты через write.
func (d *DataFile) WriteTextCellStream(write func(io.Writer) error) error {
	return d.writeCell(func() error { return WriteFieldStream(d.buf, write) })
}

func (d *DataFile) writeCell(write func() error) error {
	if d == nil || d.f == nil || !d.rowOpen {
		return os.ErrClosed
	}
	if d.rowCells > 0 {
		if err := d.buf.WriteByte(','); err != nil {
			return err
		}
	}
	if err := write(); err != nil {
		return err
	}
	d.rowCells++
	return nil
}

// EndRow завершает потоковую строку: surplus → ErrTooManyValues без записи.
func (d *DataFile) EndRow() error {
	if d == nil || d.f == nil || !d.rowOpen {
		return os.ErrClosed
	}
	n := d.rowCells
	if d.width == 0 {
		d.width = n
	}
	if n > d.width {
		if err := d.RollbackRow(); err != nil {
			return err
		}
		return ErrTooManyValues
	}
	for n < d.width {
		if err := d.buf.WriteByte(','); err != nil {
			return err
		}
		if err := WriteNullField(d.buf); err != nil {
			return err
		}
		n++
	}
	if err := d.buf.WriteByte('\n'); err != nil {
		return err
	}
	d.rows++
	d.rowOpen = false
	d.rowCells = 0
	return nil
}

// RollbackRow откатывает незавершённую или отвергнутую потоковую строку.
func (d *DataFile) RollbackRow() error {
	if d == nil || d.f == nil {
		return nil
	}
	d.buf.Reset(d.f)
	if _, err := d.f.Seek(d.rowMark, io.SeekStart); err != nil {
		return err
	}
	if err := d.f.Truncate(d.rowMark); err != nil {
		return err
	}
	d.rowOpen = false
	d.rowCells = 0
	return nil
}

// Finish сбрасывает буфер и закрывает файл, оставляя его на диске.
func (d *DataFile) Finish() error {
	if d == nil || d.f == nil {
		return nil
	}
	err := d.buf.Flush()
	closeErr := d.f.Close()
	d.f = nil
	if err != nil {
		return err
	}
	return closeErr
}

// Abort закрывает и удаляет временный файл.
func (d *DataFile) Abort() {
	if d == nil {
		return
	}
	if d.f != nil {
		_ = d.buf.Flush()
		_ = d.f.Close()
		d.f = nil
	}
	if d.path != "" {
		_ = os.Remove(d.path)
		d.path = ""
	}
}

// CommitPrepared вливает подготовленные строки данных в ключ склейки.
// Заголовок берётся из columns только если это первый успешный INSERT ключа.
// Строки шире ключа пропускаются; если не осталось ни одной — Abort и ErrTooManyValues
// только когда все строки были surplus (вызывающий логирует).
func CommitPrepared(reg *Registry, dir, table string, columns []string, dataPath string) (Result, error) {
	var empty Result
	f, err := os.Open(dataPath)
	if err != nil {
		return empty, err
	}
	defer f.Close()

	w, err := Create(reg, dir, table, columns)
	if err != nil {
		return empty, err
	}
	// Create держит слот. Паника или любой выход без Commit/Abort
	// оставляет ключ заблокированным навсегда — следующая запись в эту
	// таблицу зависает, а с ней и вся директория.
	defer func() { _ = w.Abort() }()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.ReuseRecord = true
	wrote := 0
	skipped := 0
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return empty, err
		}
		row := append([]string(nil), rec...)
		if w.NCol() > 0 && len(row) > w.NCol() {
			skipped++
			continue
		}
		if err := w.Row(row); err != nil {
			if err == ErrTooManyValues {
				skipped++
				continue
			}
			return empty, err
		}
		wrote++
	}
	if wrote == 0 {
		if skipped > 0 {
			return Result{SkippedRows: skipped}, ErrTooManyValues
		}
		return empty, nil
	}
	res, err := w.Commit()
	if err != nil {
		return empty, err
	}
	res.SkippedRows = skipped
	return res, nil
}
