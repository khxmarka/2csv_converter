package app

import (
	"path/filepath"
	"runtime"
	"strings"

	"sql2csv/internal/csvout"
	"sql2csv/internal/logx"
	"sql2csv/internal/scan"
)

// splitWritten splits CSV files produced in dir during this run.
func splitWritten(log *logx.Logger, reg *csvout.Registry, dir string) (failed, split bool) {
	for _, out := range reg.OutputsIn(dir) {
		mode := csvout.SplitNoHeader
		if out.HasHeader {
			mode = csvout.SplitWithHeader
		}
		res, err := reg.SplitIfNeeded(out.Path, mode)
		if err != nil {
			log.Errorf("%s: не удалось нарезать: %v", out.Path, err)
			failed = true
			continue
		}
		split = split || res.Split
	}
	return failed, split
}

func splitDirFiles(
	acc *accumulator,
	log *logx.Logger,
	reg *csvout.Registry,
	dir string,
	groupFile scan.SQLFile,
	csvs []scan.SQLFile,
) {
	failed, splitAny := splitWritten(log, reg, dir)
	acc.markSplit(groupFile, failed, splitAny)
	ours := outputPaths(reg, dir)
	for _, file := range csvs {
		acc.start(file)
		if _, ok := ours[foldPath(file.Path)]; ok {
			acc.record(file, fileOutcome{})
			continue
		}
		acc.record(file, splitForeignCSV(log, reg, file))
	}
}

// splitForeignCSV splits a pre-existing CSV using its first non-empty record as
// the header. Text files are split by line without CSV quote semantics.
func splitForeignCSV(log *logx.Logger, reg *csvout.Registry, file scan.SQLFile) fileOutcome {
	mode := csvout.SplitWithHeader
	if file.Kind == scan.KindTXT {
		mode = csvout.SplitLines
	}
	path := file.Path
	res, err := reg.SplitIfNeeded(path, mode)
	if err != nil {
		log.Errorf("%s: не удалось нарезать: %v", path, err)
		return fileOutcome{failed: true, splitFail: true}
	}
	if res.Split {
		return fileOutcome{split: true}
	}
	return fileOutcome{}
}

func outputPaths(reg *csvout.Registry, dir string) map[string]struct{} {
	paths := reg.OutputsIn(dir)
	out := make(map[string]struct{}, len(paths))
	for _, item := range paths {
		out[foldPath(item.Path)] = struct{}{}
	}
	return out
}

func foldPath(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}
