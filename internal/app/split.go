package app

import (
	"path/filepath"
	"runtime"
	"strings"

	"sql2csv/internal/csvout"
	"sql2csv/internal/logx"
)

func splitWritten(log *logx.Logger, reg *csvout.Registry, dir string) bool {
	failed := false
	for _, out := range reg.OutputsIn(dir) {
		mode := csvout.SplitNoHeader
		if out.HasHeader {
			mode = csvout.SplitWithHeader
		}
		if _, err := reg.SplitIfNeeded(out.Path, mode); err != nil {
			log.Errorf("%s: не удалось нарезать: %v", out.Path, err)
			failed = true
		}
	}
	return failed
}

func splitForeignCSV(log *logx.Logger, reg *csvout.Registry, path string) fileOutcome {
	res, err := reg.SplitIfNeeded(path, csvout.SplitWithHeader)
	if err != nil {
		log.Errorf("%s: не удалось нарезать: %v", path, err)
		return fileOutcome{failed: true, splitFail: true}
	}
	if res.Split {
		return fileOutcome{csv: 1}
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
