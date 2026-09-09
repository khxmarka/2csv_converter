package app

import (
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"sql2csv/internal/convert"
	"sql2csv/internal/converted"
	"sql2csv/internal/csvout"
	"sql2csv/internal/logx"
	"sql2csv/internal/scan"
	"sql2csv/internal/xlsconv"
)

const maxWorkers = 16

func workerCount(files int) int {
	if files <= 0 {
		return 0
	}
	n := runtime.GOMAXPROCS(0)
	if n < 1 {
		n = 1
	}
	if n > maxWorkers {
		n = maxWorkers
	}
	if n > files {
		n = files
	}
	return n
}

func folderName(f scan.SQLFile) string {
	if f.TopFolder == "" {
		return "корень"
	}
	return f.TopFolder
}

func folderKey(f scan.SQLFile) string {
	if f.TopFolder == "" {
		return "\x00root"
	}
	return f.TopFolder
}

type accumulator struct {
	mu         sync.Mutex
	insertOK   int
	insertSkip int
	csv        int
	filesFail  int
	tops       map[string]struct{}
	left       map[string]int
	active     map[string]string
	blocked    map[string]struct{}
	log        *logx.Logger
	root       string
}

func newAccumulator(log *logx.Logger, root string, files []scan.SQLFile, blocked map[string]struct{}) *accumulator {
	left := make(map[string]int)
	for _, f := range files {
		left[folderKey(f)]++
	}
	return &accumulator{
		tops:    make(map[string]struct{}),
		left:    left,
		active:  make(map[string]string),
		blocked: blocked,
		log:     log,
		root:    root,
	}
}

func (a *accumulator) start(file scan.SQLFile) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := folderKey(file)
	name := folderName(file)
	if _, ok := a.active[key]; ok {
		return
	}
	a.active[key] = name
	a.refreshHang()
}

func (a *accumulator) add(file scan.SQLFile, out fileOutcome) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if out.openErr != nil {
		a.filesFail++
		a.log.Errorf("%s: не удалось открыть: %v", file.Path, out.openErr)
	} else if !out.skipTooMany {
		if out.failed {
			a.filesFail++
		}
		if out.writeErr != nil {
			a.filesFail++
			a.log.Errorf("%s: не удалось создать CSV: %v", file.Path, out.writeErr)
		}
		a.insertOK += out.created
		a.insertSkip += out.skipped
		a.csv += out.csv
	}
	key := folderKey(file)
	name := folderName(file)
	a.left[key]--
	if a.left[key] == 0 {
		_, cannotComplete := a.blocked[file.TopFolder]
		if file.TopFolder != "" && !cannotComplete {
			if err := converted.Append(a.root, file.TopFolder); err != nil {
				a.log.Errorf("не удалось дописать %s: %v", converted.Path(a.root), err)
			} else {
				a.tops[file.TopFolder] = struct{}{}
			}
		}
		delete(a.active, key)
		a.refreshHang()
		if !cannotComplete {
			a.log.Linef("папка полностью завершена: %s", name)
		}
	}
}

func (a *accumulator) refreshHang() {
	if len(a.active) == 0 {
		a.log.Hang("")
		return
	}
	names := make([]string, 0, len(a.active))
	for _, name := range a.active {
		names = append(names, name)
	}
	sort.Strings(names)
	a.log.Hang("папка в обработке: " + strings.Join(names, ", "))
}

func (a *accumulator) snapshot() (insertOK, insertSkip, csv, filesFail int, tops map[string]struct{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cloned := make(map[string]struct{}, len(a.tops))
	for k := range a.tops {
		cloned[k] = struct{}{}
	}
	return a.insertOK, a.insertSkip, a.csv, a.filesFail, cloned
}

func (a *accumulator) completeEmptyTops(topDirs []string) {
	for _, name := range topDirs {
		a.mu.Lock()
		_, hadWork := a.left[name]
		_, cannotComplete := a.blocked[name]
		if hadWork || cannotComplete {
			a.mu.Unlock()
			continue
		}

		a.active[name] = name
		a.refreshHang()
		err := converted.Append(a.root, name)
		if err != nil {
			a.log.Errorf("не удалось дописать %s: %v", converted.Path(a.root), err)
		} else {
			a.tops[name] = struct{}{}
		}
		delete(a.active, name)
		a.refreshHang()
		if err == nil {
			a.log.Linef("папка полностью завершена: %s", name)
		}
		a.mu.Unlock()
	}
}

func processFiles(log *logx.Logger, root string, files []scan.SQLFile, blocked map[string]struct{}) *accumulator {
	acc := newAccumulator(log, root, files, blocked)
	if len(files) == 0 {
		return acc
	}

	reg := csvout.NewRegistry()
	for _, topFiles := range groupByTop(files) {
		processTopFiles(acc, log, reg, topFiles)
	}
	return acc
}

func processTopFiles(acc *accumulator, log *logx.Logger, reg *csvout.Registry, files []scan.SQLFile) {
	groups := groupByDir(files)
	for _, group := range groups {
		if len(group) == 0 {
			continue
		}
		dir := filepath.Dir(group[0].Path)
		if err := csvout.CleanupTemps(dir); err != nil {
			log.Errorf("%s: не удалось удалить временные CSV: %v", dir, err)
		}
	}
	n := workerCount(len(groups))
	jobs := make(chan []scan.SQLFile)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			for group := range jobs {
				for _, file := range group {
					acc.start(file)
					acc.add(file, convertOne(log, reg, file))
				}
			}
		}()
	}
	for _, group := range groups {
		jobs <- group
	}
	close(jobs)
	wg.Wait()
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
// Excel-файлы после SQL также сортируются по пути.
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
			iExcel := g[i].IsExcel()
			jExcel := g[j].IsExcel()
			if iExcel != jExcel {
				return !iExcel
			}
			return g[i].Path < g[j].Path
		})
		groups = append(groups, g)
	}
	return groups
}

type fileOutcome struct {
	openErr     error
	writeErr    error
	created     int
	skipped     int
	csv         int
	skipTooMany bool
	failed      bool
}

func convertOne(log *logx.Logger, reg *csvout.Registry, file scan.SQLFile) (out fileOutcome) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Errorf("%s: сбой обработки (%v), файл пропущен", file.Path, rec)
			out = fileOutcome{skipped: 1, failed: true}
		}
	}()
	if file.IsExcel() {
		xr := xlsconv.File(reg, file.Path)
		return fileOutcome{
			openErr:     xr.OpenErr,
			writeErr:    xr.WriteErr,
			csv:         xr.CSV,
			skipTooMany: xr.SkipTooMany,
		}
	}
	fr := convert.File(log, reg, file)
	return fileOutcome{
		openErr: fr.OpenErr,
		created: fr.Created,
		skipped: fr.Skipped,
		csv:     fr.CSV,
		failed:  fr.Failed,
	}
}
