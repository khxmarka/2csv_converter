// Package app — сценарий запуска. CLI только разбирает флаги.
package app

import (
	"fmt"
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

// Run проверяет корень, находит .sql/.xlsx/.xls/.csv, пишет CSV рядом с исходниками и нарезает большие CSV.
func Run(log *logx.Logger, root string) (Result, error) {
	var empty Result
	if err := scan.ValidateRoot(root); err != nil {
		return empty, err
	}
	releaseRoot, err := acquireRootLock(root)
	if err != nil {
		return empty, err
	}
	defer releaseRoot()

	completed, err := converted.Read(root)
	if err != nil {
		log.Errorf("не удалось прочитать %s: %v", converted.Path(root), err)
		completed = make(map[string]struct{})
	}

	found, err := scan.FindSkipping(root, completed)
	if err != nil {
		return empty, err
	}

	blocked := make(map[string]struct{})
	scanFails := 0
	for _, skipped := range found.Skips {
		if !skipped.BlocksCompletion {
			continue
		}
		scanFails++
		log.Errorf("%s: ошибка обхода: %s", skipped.Path, skipped.Reason)
		if skipped.TopFolder != "" {
			blocked[skipped.TopFolder] = struct{}{}
		}
	}

	acc := processFiles(log, root, found.Files, blocked)
	acc.completeEmptyTops(found.TopDirs)
	insertOK, insertSkip, csvCount, filesFail, tops := acc.snapshot()
	out := Result{
		Scan:        found,
		InsertOK:    insertOK,
		InsertSkip:  insertSkip,
		CSV:         csvCount,
		FilesFail:   filesFail + scanFails,
		SuccessTops: mapsKeys(tops),
	}
	log.Hang("")
	if err := log.Err(); err != nil {
		return out, fmt.Errorf("не удалось писать лог: %w", err)
	}
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
