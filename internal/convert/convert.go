package convert

import (
	"fmt"

	"sql2csv/internal/csvout"
	"sql2csv/internal/insert"
	"sql2csv/internal/logx"
	"sql2csv/internal/scan"
)

// Result summarizes one input file. Open errors are reported to the caller without stopping the run.
type Result struct {
	Created  int // Successfully committed INSERT statements.
	CSV      int // Newly published CSV files; appends do not count.
	Skipped  int
	PIISkip  int
	UnitFail int
	Paths    []string
	OpenErr  error
	Failed   bool // A critical read or CSV write failure occurred.
}

// session contains state for one SQL file and is mutated only by the commitQ goroutine.
type session struct {
	log      *logx.Logger
	reg      *csvout.Registry
	sql      scan.SQLFile
	dir      string
	created  int
	csvNew   int
	skipped  int
	piiN     int
	unitFail int
	failed   bool
	paths    []string
	dirty    []string
}

func skipQuiet(reason string) bool {
	switch reason {
	// A missing INTO marks INSERT used in a GRANT or trigger rather than a data statement.
	case "INSERT ... SELECT", "INSERT ... SET", "нет VALUES", "пустой список колонок", "нет строк VALUES", "нет INTO":
		return true
	default:
		return false
	}
}

func (s *session) skip(sk insert.Skip) {
	s.skipped++
	if skipQuiet(sk.Reason) {
		return
	}
	s.unitFail++
	// Include the INSERT line in the path so operators can locate the failure directly.
	where := s.sql.Path
	if sk.Line > 0 {
		where = fmt.Sprintf("%s:%d", s.sql.Path, sk.Line)
	}
	if sk.Table != "" {
		s.log.Errorf("%s таблица %s: %s", where, sk.Table, sk.Reason)
		return
	}
	s.log.Errorf("%s: %s", where, sk.Reason)
}
