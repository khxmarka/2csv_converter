// Package app orchestrates discovery, conversion, splitting, and persistent state.
package app

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"sql2csv/internal/logx"
	"sql2csv/internal/marks"
	"sql2csv/internal/scan"
)

// Result summarizes a run after persistent stage lists have been updated.
type Result struct {
	Scan        scan.Result
	InsertOK    int
	InsertSkip  int
	CSV         int
	FilesFail   int
	SuccessTops []string // Top-level folders added to a done list.
}

// Run processes supported files below root and writes results beside their sources.
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
	// A folder is skipped entirely only after both independent stages are complete.
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
		SuccessTops: slices.Sorted(maps.Keys(tops)),
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

// openLogFile appends a timestamped run to root\_log.txt. Failure leaves
// console logging available and does not stop processing.
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

// readLists loads persistent stage state. An unreadable list is returned as empty with its error.
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
	maps.Copy(out, a)
	maps.Copy(out, b)
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
