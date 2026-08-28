// Package app — сценарий запуска. CLI только разбирает флаги.
package app

import (
	"sort"

	"sql2csv/internal/converted"
	"sql2csv/internal/logx"
	"sql2csv/internal/scan"
)

// Result — итог запуска после записи converted.txt.
type Result struct {
	Scan        scan.Result
	InsertOK    int
	InsertSkip  int
	CSV         int
	FilesFail   int
	SuccessTops []string
}

// Run проверяет корень, находит .sql и конвертирует INSERT в CSV рядом с ними.
func Run(log *logx.Logger, root string) (Result, error) {
	var empty Result
	if err := scan.ValidateRoot(root); err != nil {
		return empty, err
	}

	found, err := scan.Find(root)
	if err != nil {
		return empty, err
	}

	acc := processFiles(log, found.Files)
	insertOK, insertSkip, csvCount, filesFail, tops := acc.snapshot()
	out := Result{
		Scan:        found,
		InsertOK:    insertOK,
		InsertSkip:  insertSkip,
		CSV:         csvCount,
		FilesFail:   filesFail,
		SuccessTops: mapsKeys(tops),
	}
	if err := converted.Write(root, out.SuccessTops); err != nil {
		log.Errorf("не удалось записать %s: %v", converted.Path(root), err)
	}
	log.Hang("")
	return out, nil
}

func mapsKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
