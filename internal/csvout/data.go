package csvout

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"io"
	"os"
)

// dataMemLimit keeps typical INSERT batches in memory and spills larger batches
// to a temporary file, avoiding one file per small INSERT.
const dataMemLimit = 1 << 20

// DataFile stages CSV-formatted rows for one INSERT without a header or width
// padding. Cells can be streamed so large values do not need to fit in memory;
// data exceeding dataMemLimit spills to disk.
type DataFile struct {
	dir     string
	mem     bytes.Buffer
	f       *os.File
	buf     *bufio.Writer
	size    int64 // Row bytes staged in memory and on disk.
	scratch []byte
	rows    int
	// Row widths let Commit choose between direct copying and record normalization.
	first, minCells, maxCells int
	rowMark                   int64
	rowCells                  int
	esc                       quoteEscaper // Reused writer avoids wrapping every cell.
}

// CreateData prepares row staging and creates a temporary file in dir only after spilling.
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

// Row stages one record. An empty VALUES tuple becomes one empty cell because an
// empty CSV record would otherwise be skipped during Commit.
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

// BeginRow starts a streamed record and saves the rollback position.
func (d *DataFile) BeginRow() error {
	d.rowMark = d.size
	d.rowCells = 0
	return nil
}

// WriteNullCell writes an empty quoted cell for NULL or a missing value.
func (d *DataFile) WriteNullCell() error {
	return d.WriteTextCellStream(func(io.Writer) error { return nil })
}

// WriteTextCellStream writes cell text in chunks while escaping quotes incrementally.
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

// EndRow commits the current streamed record to staged data.
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

// RollbackRow discards the current record after a surplus value or malformed literal.
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

// quoteEscaper doubles quotes. Chunk boundaries are safe because '"' cannot
// occur inside a multibyte UTF-8 sequence.
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
	f, err := createTemp(d.dir)
	if err != nil {
		return err
	}
	d.f = f
	d.buf = bufio.NewWriterSize(f, 256*1024)
	_, err = d.buf.Write(d.mem.Bytes())
	d.mem = bytes.Buffer{}
	return err
}

// Finish flushes spilled data when a temporary file exists.
func (d *DataFile) Finish() error {
	if d == nil || d.f == nil {
		return nil
	}
	if err := d.buf.Flush(); err != nil {
		return err
	}
	return d.f.Sync()
}

// open returns staged rows from the beginning.
func (d *DataFile) open() (io.Reader, error) {
	if d.f == nil {
		return bytes.NewReader(d.mem.Bytes()), nil
	}
	if _, err := d.f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return bufio.NewReaderSize(d.f, 256*1024), nil
}

// Abort releases staged data and removes its temporary file, if any.
func (d *DataFile) Abort() {
	if d == nil {
		return
	}
	d.mem = bytes.Buffer{}
	if d.f != nil {
		name := d.f.Name()
		_ = d.f.Close()
		_ = removeTemp(name)
		d.f = nil
	}
}

// CommitPrepared merges staged rows into a key. columns becomes the header only
// for the first successful INSERT. Rows wider than the established width are
// skipped individually; if none remain, the output stays unchanged and
// ErrTooManyValues is returned. Uniform rows are copied without reparsing.
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
	// newWriter owns the slot; ensure panic or early return cannot leave the key locked.
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

// CommitPlain publishes a tabular sheet. The output width is the widest header
// or data row, and shorter rows are padded with empty cells. Because the width
// is known only after reading the sheet, data is staged before writing the header.
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

// copyRows writes staged records at width. Uniform records are copied directly;
// shorter records are padded and wider records are skipped.
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
