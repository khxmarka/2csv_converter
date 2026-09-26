// Package app — сценарий запуска. CLI только разбирает флаги.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"sql2csv/internal/logx"
	"sql2csv/internal/marks"
	"sql2csv/internal/scan"
)

// Result — итог запуска после записи списков состояний.
type Result struct {
	Scan        scan.Result
	InsertOK    int
	InsertSkip  int
	CSV         int
	FilesFail   int
	SuccessTops []string // верхние папки, попавшие в _convert_done_ или _splitter_done_
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

	closeLog := openLogFile(log, root)
	defer closeLog()

	lists := readLists(log, root)
	convSkip := union(lists[marks.ConvertDone], lists[marks.ConvertPassed])
	splitSkip := union(lists[marks.SplitDone], lists[marks.SplitPassed])
	// Папку целиком не открываем, только если оба этапа для неё закрыты.
	found, err := scan.FindSkipping(root, intersect(convSkip, splitSkip))
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

	acc := processFiles(log, root, found.Files, blocked, convSkip, splitSkip)
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
	if err := log.FileErr(); err != nil {
		log.Errorf("не удалось писать %s: %v", filepath.Join(root, marks.LogName), err)
	}
	if err := log.Err(); err != nil {
		return out, fmt.Errorf("не удалось писать лог: %w", err)
	}
	return out, nil
}

// openLogFile дописывает лог запуска в {корень}\_log.txt с заголовком
// времени. Не открылся — работа идёт, лог только в консоли.
func openLogFile(log *logx.Logger, root string) (closeFn func()) {
	path := filepath.Join(root, marks.LogName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Errorf("не удалось открыть %s: %v", path, err)
		return func() {}
	}
	log.SetFile(f)
	log.FileLinef("=== %s, корень %s ===", time.Now().Format("2006-01-02 15:04:05"), root)
	return func() {
		log.SetFile(nil)
		if err := f.Close(); err != nil {
			log.Errorf("не удалось закрыть %s: %v", path, err)
		}
	}
}

// readLists читает списки состояний. Нечитаемый список — пустой, с ошибкой.
func readLists(log *logx.Logger, root string) map[marks.List]map[string]struct{} {
	out := make(map[marks.List]map[string]struct{}, len(marks.Lists))
	for _, l := range marks.Lists {
		names, err := marks.Read(root, l)
		if err != nil {
			log.Errorf("не удалось прочитать %s: %v", marks.Path(root, l), err)
			names = make(map[string]struct{})
		}
		out[l] = marks.FoldSet(names)
	}
	return out
}

func union(a, b map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

func intersect(a, b map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{})
	for k := range a {
		if _, ok := b[k]; ok {
			out[k] = struct{}{}
		}
	}
	return out
}

func mapsKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
