package convert

import (
	"sql2csv/internal/csvout"
	"sql2csv/internal/insert"
	"sql2csv/internal/logx"
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

// session — счётчики одного .sql. Меняется только из горутины commitQ.
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
}

func skipQuiet(reason string) bool {
	switch reason {
	// «нет INTO» — INSERT как слово в GRANT/TRIGGER, а не оператор данных.
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
	if sk.Table != "" {
		s.log.Errorf("%s таблица %s: %s", s.sql.Path, sk.Table, sk.Reason)
		return
	}
	s.log.Errorf("%s: %s", s.sql.Path, sk.Reason)
}
