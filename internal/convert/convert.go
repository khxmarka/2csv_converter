package convert

import (
	"io"
	"os"
	"path/filepath"

	"sql2csv/internal/csvout"
	"sql2csv/internal/insert"
	"sql2csv/internal/logx"
	"sql2csv/internal/pii"
	"sql2csv/internal/scan"
)

// Result — итог одного файла: ошибки открытия не роняют процесс, их видит вызывающий.
type Result struct {
	Created  int // успешные INSERT
	CSV      int // новые CSV-файлы (дописывания не считаются)
	Skipped  int
	PIISkip  int
	UnitFail int
	Paths    []string
	OpenErr  error
	Failed   bool // в файле была критическая ошибка чтения/записи CSV
}

// File разбирает sql-файл и пишет CSV в ту же директорию.
// В консоль идут только критические ошибки записи/чтения, не каждый INSERT.
func File(log *logx.Logger, reg *csvout.Registry, sql scan.SQLFile) Result {
	f, err := os.Open(sql.Path)
	if err != nil {
		return Result{OpenErr: err}
	}
	defer f.Close()

	s := &session{
		log: log,
		reg: reg,
		sql: sql,
		dir: filepath.Dir(sql.Path),
	}
	tab, rest, err := sniffTable(f)
	if err != nil {
		log.Errorf("%s: %v", sql.Path, err)
		return Result{Skipped: 1, Failed: true}
	}
	if tab != nil {
		return convertTable(log, reg, sql, tab, rest)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		log.Errorf("%s: %v", sql.Path, err)
		return Result{Skipped: 1, Failed: true}
	}
	if err := insert.Parse(f, insert.Handler{
		Begin: s.begin,
		Row:   s.row,
		End:   s.end,
		Skip:  s.skip,
	}); err != nil {
		s.dropWriter()
		s.skipped++
		s.failed = true
		log.Errorf("%s: %v", sql.Path, err)
	}
	return Result{
		Created:  s.created,
		CSV:      s.csvNew,
		Skipped:  s.skipped,
		PIISkip:  s.piiN,
		UnitFail: s.unitFail,
		Paths:    s.paths,
		Failed:   s.failed,
	}
}

type session struct {
	log      *logx.Logger
	reg      *csvout.Registry
	sql      scan.SQLFile
	dir      string
	writer   *csvout.Writer
	meta     insert.Meta
	created  int
	csvNew   int
	skipped  int
	piiN     int
	unitFail int
	failed   bool
	piiSkip  bool
	paths    []string
}

func (s *session) dropWriter() {
	if s.writer == nil {
		return
	}
	_ = s.writer.Abort()
	s.writer = nil
}

func (s *session) begin(meta insert.Meta) error {
	s.dropWriter()
	s.meta = meta
	s.piiSkip = false
	if !pii.Match(meta.Table) && !pii.MatchColumns(meta.Columns) {
		s.skipped++
		s.piiN++
		s.piiSkip = true
		return nil
	}
	w, err := csvout.Create(s.reg, s.dir, meta.Table, meta.Columns)
	if err != nil {
		s.skipped++
		s.failed = true
		s.log.Errorf("%s таблица %s: не удалось создать CSV: %v", s.sql.Path, meta.Table, err)
		return nil
	}
	s.writer = w
	return nil
}

func (s *session) row(cells []insert.Cell) error {
	if s.writer == nil {
		return nil
	}
	values, _, err := csvout.NormalizeRow(cells, s.writer.NCol())
	if err != nil {
		s.dropWriter()
		s.skipped++
		s.unitFail++
		s.log.Errorf("%s таблица %s: %v", s.sql.Path, s.meta.Table, err)
		return nil
	}
	if err := s.writer.Row(values); err != nil {
		s.dropWriter()
		s.skipped++
		s.failed = true
		s.log.Errorf("%s таблица %s: запись CSV: %v", s.sql.Path, s.meta.Table, err)
	}
	return nil
}

func (s *session) end() error {
	s.piiSkip = false
	if s.writer == nil {
		return nil
	}
	w := s.writer
	s.writer = nil
	res, err := w.Commit()
	if err != nil {
		s.skipped++
		s.failed = true
		s.log.Errorf("%s таблица %s: не удалось записать CSV: %v", s.sql.Path, s.meta.Table, err)
		return nil
	}
	s.created++
	s.paths = append(s.paths, res.Path)
	if !res.Appended {
		s.csvNew++
	}
	return nil
}

func skipQuiet(reason string) bool {
	switch reason {
	case "INSERT ... SELECT", "INSERT ... SET", "нет VALUES", "пустой список колонок", "нет строк VALUES":
		return true
	default:
		return false
	}
}

func (s *session) skip(sk insert.Skip) {
	hadWriter := s.writer != nil
	s.dropWriter()
	if s.piiSkip {
		s.piiSkip = false
		return
	}
	s.skipped++
	if skipQuiet(sk.Reason) && !hadWriter {
		return
	}
	s.unitFail++
	if sk.Table != "" {
		s.log.Errorf("%s таблица %s: %s", s.sql.Path, sk.Table, sk.Reason)
		return
	}
	s.log.Errorf("%s: %s", s.sql.Path, sk.Reason)
}
