package convert

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"sql2csv/internal/csvout"
	"sql2csv/internal/insert"
	"sql2csv/internal/logx"
	"sql2csv/internal/pii"
	"sql2csv/internal/scan"
)

// Schedule читает sql-файл. PII-отказ не разбирает VALUES.
// Принятые INSERT отдаются в submit на разбор ячеек; Commit идёт по порядку обхода.
// Табличный .sql выполняется на вызывающей стороне, без пула INSERT.
func Schedule(log *logx.Logger, reg *csvout.Registry, sql scan.SQLFile, submit func(func())) (out Result) {
	if submit == nil {
		submit = func(fn func()) { fn() }
	}
	f, err := os.Open(sql.Path)
	if err != nil {
		return Result{OpenErr: err}
	}
	defer f.Close()

	st := &session{
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
	// Воркеры читают хвосты INSERT через ReadAt по своему дескриптору: сканер
	// идёт по f последовательно, а временных файлов на INSERT больше нет.
	// Закрывается после q (defer ниже выполняется раньше).
	body, err := os.Open(sql.Path)
	if err != nil {
		return Result{OpenErr: err}
	}
	defer body.Close()

	q := newCommitQ()
	done := q.start(func(rec any) {
		st.skipped++
		st.failed = true
		log.Errorf("%s: сбой записи INSERT (%v), INSERT пропущен", sql.Path, rec)
	})
	defer func() {
		q.close()
		<-done
		// Дескрипторы дописывания CSV живут только пока пишет этот .sql:
		// дальше Excel, нарезка и замена файлов этой директории.
		if err := reg.CloseDir(st.dir); err != nil {
			st.failed = true
			log.Errorf("%s: не удалось закрыть CSV: %v", sql.Path, err)
		}
		out = Result{
			Created:  st.created,
			CSV:      st.csvNew,
			Skipped:  st.skipped,
			PIISkip:  st.piiN,
			UnitFail: st.unitFail,
			Paths:    append([]string(nil), st.paths...),
			Failed:   st.failed,
		}
	}()

	err = insert.Parse(f, insert.Handler{
		BeforeValues: func(meta insert.Meta) (bool, error) {
			if pii.Match(meta.Table) || pii.MatchColumns(meta.Columns) {
				return false, nil
			}
			q.enqueue(func() {
				st.skipped++
				st.piiN++
			})
			return true, nil
		},
		ValuesAt: func(meta insert.Meta, off, n int64) error {
			meta.Columns = append([]string(nil), meta.Columns...)
			fill := q.reserve()
			submit(func() {
				var apply func()
				defer func() {
					if rec := recover(); rec != nil {
						apply = func() {
							st.skipped++
							st.failed = true
							st.log.Errorf("%s таблица %s: сбой обработки (%v), INSERT пропущен", st.sql.Path, meta.Table, rec)
						}
					}
					if apply == nil {
						apply = func() {}
					}
					fill(apply)
				}()
				prep := prepareInsert(st.dir, meta, io.NewSectionReader(body, off, n))
				apply = func() { st.apply(meta, prep) }
			})
			return nil
		},
		Skip: func(sk insert.Skip) {
			q.enqueue(func() { st.skip(sk) })
		},
	})
	if err != nil {
		q.enqueue(func() {
			st.skipped++
			st.failed = true
			st.log.Errorf("%s: %v", st.sql.Path, err)
		})
	}
	return out
}

type prepared struct {
	data    *csvout.DataFile
	skip    *insert.Skip
	err     error
	surplus int
	// cut — INSERT оборван после целых строк (обрезанный дамп): они пишутся,
	// в лог — причина и строка файла cutLine.
	cut     string
	cutLine int
}

func prepareInsert(dir string, meta insert.Meta, body io.Reader) (out prepared) {
	var data *csvout.DataFile
	var skipped *insert.Skip
	surplus := 0
	var cut string
	cutLine := 0
	var cellw dataCellWriter // один на INSERT, не на строку
	defer func() {
		if out.data == nil && data != nil {
			data.Abort()
		}
	}()
	err := insert.ParseValues(body, meta, insert.Handler{
		Begin: func(insert.Meta) error {
			data = csvout.CreateData(dir)
			return nil
		},
		StreamRow: func(emit func(insert.CellWriter) error) error {
			if data == nil {
				return io.ErrClosedPipe
			}
			if err := data.BeginRow(); err != nil {
				return err
			}
			cw := &cellw
			cw.d = data
			err := emit(cw)
			if errors.Is(err, insert.ErrRowSurplus) {
				_ = data.RollbackRow()
				return insert.ErrRowSurplus
			}
			if err != nil {
				_ = data.RollbackRow()
				return err
			}
			if err := data.EndRow(); err != nil {
				if errors.Is(err, csvout.ErrTooManyValues) {
					return insert.ErrRowSurplus
				}
				return err
			}
			return nil
		},
		RowSurplus: func() { surplus++ },
		Cut:        func(reason string, line int) { cut, cutLine = reason, line },
		Skip: func(sk insert.Skip) {
			cp := sk
			skipped = &cp
			if data != nil {
				data.Abort()
				data = nil
			}
		},
	})
	if err != nil {
		if data != nil {
			data.Abort()
			data = nil
		}
		return prepared{err: err, surplus: surplus}
	}
	if skipped != nil {
		return prepared{skip: skipped, surplus: surplus}
	}
	if data == nil {
		return prepared{surplus: surplus}
	}
	if err := data.Finish(); err != nil {
		return prepared{err: err, surplus: surplus}
	}
	out = prepared{data: data, surplus: surplus, cut: cut, cutLine: cutLine}
	return out
}

type dataCellWriter struct {
	d *csvout.DataFile
}

func (c *dataCellWriter) Null() error {
	return c.d.WriteNullCell()
}

func (c *dataCellWriter) Missing() error {
	return c.d.WriteNullCell()
}

func (c *dataCellWriter) Text(write func(io.Writer) error) error {
	return c.d.WriteTextCellStream(write)
}

func (s *session) apply(meta insert.Meta, prep prepared) {
	if prep.err != nil {
		s.skipped++
		s.failed = true
		s.log.Errorf("%s таблица %s: %v", s.sql.Path, meta.Table, prep.err)
		return
	}
	if prep.skip != nil {
		s.skip(*prep.skip)
		if prep.surplus > 0 {
			s.unitFail += prep.surplus
			s.log.Errorf("%s таблица %s: значений больше, чем колонок (%d строк пропущено)", s.sql.Path, meta.Table, prep.surplus)
		}
		return
	}
	if prep.data == nil || prep.data.Rows() == 0 {
		if prep.data != nil {
			prep.data.Abort()
		}
		if prep.surplus > 0 {
			s.skipped++
			s.unitFail += prep.surplus
			s.log.Errorf("%s таблица %s: значений больше, чем колонок (%d строк пропущено)", s.sql.Path, meta.Table, prep.surplus)
			return
		}
		s.skip(insert.Skip{Table: meta.Table, Reason: "нет строк VALUES", Offset: meta.Offset, Line: meta.Line})
		return
	}
	defer prep.data.Abort()
	res, err := csvout.CommitPrepared(s.reg, s.dir, meta.Table, meta.Columns, prep.data)
	if err != nil {
		if errors.Is(err, csvout.ErrTooManyValues) {
			n := res.SkippedRows
			if n < 1 {
				n = prep.data.Rows()
			}
			s.skipped++
			s.unitFail += n
			s.log.Errorf("%s таблица %s: значений больше, чем колонок (%d строк пропущено)", s.sql.Path, meta.Table, n)
			return
		}
		s.skipped++
		s.failed = true
		s.log.Errorf("%s таблица %s: не удалось записать CSV: %v", s.sql.Path, meta.Table, err)
		return
	}
	s.created++
	s.paths = append(s.paths, res.Path)
	if !res.Appended {
		s.csvNew++
	}
	totalSurplus := prep.surplus + res.SkippedRows
	if totalSurplus > 0 {
		s.unitFail += totalSurplus
		s.log.Errorf("%s таблица %s: значений больше, чем колонок (%d строк пропущено)", s.sql.Path, meta.Table, totalSurplus)
	}
	if prep.cut != "" {
		// Не молчим: хвост INSERT потерян, исходник остаётся (unitFail, §15).
		s.unitFail++
		s.log.Errorf("%s:%d таблица %s: INSERT оборван (%s), записано строк: %d",
			s.sql.Path, prep.cutLine, meta.Table, prep.cut, prep.data.Rows()-res.SkippedRows)
	}
}
