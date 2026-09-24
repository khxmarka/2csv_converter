package csvout

import (
	"bufio"
	"encoding/csv"
	"io"
	"os"
)

// DataFile — временный файл только со строками данных одного INSERT, без заголовка.
// Строки хранятся без дополнения: ширину ключа (§6) применяет Writer при Commit.
type DataFile struct {
	f    *os.File
	buf  *bufio.Writer
	rows int
	path string
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

// Row пишет одну строку данных как есть. Пустая строка VALUES () становится
// одной пустой ячейкой: пустую запись CSV-ридер при Commit пропустил бы.
func (d *DataFile) Row(values []string) error {
	if d == nil || d.f == nil {
		return os.ErrClosed
	}
	if len(values) == 0 {
		values = []string{""}
	}
	if _, err := io.WriteString(d.buf, encodeRow(values)); err != nil {
		return err
	}
	d.rows++
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
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return empty, err
		}
		row := append([]string(nil), rec...)
		if err := w.Row(row); err != nil {
			return empty, err
		}
	}
	return w.Commit()
}
