package app

import (
	"path/filepath"
	"runtime"
	"sort"

	"sql2csv/internal/csvout"
	"sql2csv/internal/logx"
	"sql2csv/internal/scan"
)

const maxWorkers = 16

// maxConcurrentDirs bounds concurrent workbook and splitter memory without reducing INSERT workers.
const maxConcurrentDirs = 4

// poolSize — N воркеров на весь запуск: min(GOMAXPROCS, 16), не меньше 1 (§9).
// Числом файлов не ограничивается: INSERT одного файла тоже идут в этот пул.
func poolSize() int {
	return min(max(runtime.GOMAXPROCS(0), 1), maxWorkers)
}

// processFiles обрабатывает файлы запуска. convSkip/splitSkip — верхние
// папки (ключи marks.Fold), для которых этап конверта/нарезки уже закрыт.
func processFiles(log *logx.Logger, root string, files []scan.SQLFile, blocked, convSkip, splitSkip map[string]struct{}) *accumulator {
	acc := newAccumulator(log, root, files, blocked)
	if convSkip != nil {
		acc.convSkip = convSkip
	}
	if splitSkip != nil {
		acc.splitSkip = splitSkip
	}
	if len(files) == 0 {
		return acc
	}

	reg := csvout.NewRegistry()
	stopTick := acc.startHangTicker()
	defer stopTick()
	runDirs(acc, log, reg, files)
	return acc
}

func groupByTop(files []scan.SQLFile) [][]scan.SQLFile {
	order := make([]string, 0)
	byTop := make(map[string][]scan.SQLFile)
	for _, file := range files {
		key := folderKey(file)
		if _, ok := byTop[key]; !ok {
			order = append(order, key)
		}
		byTop[key] = append(byTop[key], file)
	}
	groups := make([][]scan.SQLFile, 0, len(order))
	for _, key := range order {
		groups = append(groups, byTop[key])
	}
	return groups
}

// groupByDir собирает файлы одной директории в группу. SQL-файлы идут единым
// непрерывным потоком раньше Excel и сортируются по пути: так SQL-ключ не может
// быть вытеснен Excel-файлом между двумя INSERT и ошибочно дописаться в Excel CSV.
// Excel идёт после SQL, заранее лежавшие CSV — после Excel. Внутри ранга файлы
// сортируются по пути.
func groupByDir(files []scan.SQLFile) [][]scan.SQLFile {
	order := make([]string, 0)
	byDir := make(map[string][]scan.SQLFile)
	for _, f := range files {
		d := filepath.Dir(f.Path)
		if _, ok := byDir[d]; !ok {
			order = append(order, d)
		}
		byDir[d] = append(byDir[d], f)
	}
	groups := make([][]scan.SQLFile, 0, len(order))
	for _, d := range order {
		g := byDir[d]
		sort.Slice(g, func(i, j int) bool {
			if ri, rj := fileRank(g[i]), fileRank(g[j]); ri != rj {
				return ri < rj
			}
			return g[i].Path < g[j].Path
		})
		groups = append(groups, g)
	}
	return groups
}

// testDirEnter — крюк теста. В бою nil. Вызывается до файлов директории.
var testDirEnter func(scan.SQLFile)

func processDirGroup(acc *accumulator, log *logx.Logger, reg *csvout.Registry, submit func(func()), group []scan.SQLFile) {
	if len(group) == 0 {
		return
	}
	dir := filepath.Dir(group[0].Path)
	defer func() {
		if err := csvout.CleanupTemps(dir); err != nil {
			log.Errorf("%s: не удалось удалить временные CSV: %v", dir, err)
			acc.markGroupFailure(group[0])
		}
		if rec := recover(); rec != nil {
			log.Errorf("%s: сбой обработки (%v), папка пропущена", dir, rec)
			acc.markGroupFailure(group[0])
		}
		acc.finishFiles(group)
	}()
	if testDirEnter != nil {
		testDirEnter(group[0])
	}
	if err := csvout.CleanupTemps(dir); err != nil {
		log.Errorf("%s: не удалось удалить временные CSV: %v", dir, err)
	}
	var sqls, excels, csvs []scan.SQLFile
	for _, file := range group {
		switch {
		case file.IsCSV():
			csvs = append(csvs, file)
		case file.IsExcel():
			excels = append(excels, file)
		default:
			sqls = append(sqls, file)
		}
	}
	doConvert, doSplit := acc.phases(group[0].TopFolder)
	if !doConvert {
		sqls, excels = nil, nil
	}
	if !doSplit {
		csvs = nil
	}
	convertDirFiles(acc, log, reg, submit, sqls, excels)
	// CSV этого запуска режутся всегда: это часть их создания (§5).
	splitDirFiles(acc, log, reg, dir, group[0], csvs)
}

func fileRank(f scan.SQLFile) int {
	switch {
	case f.IsCSV():
		return 2
	case f.IsExcel():
		return 1
	default:
		return 0
	}
}
