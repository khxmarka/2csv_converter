package app

import (
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"sql2csv/internal/convert"
	"sql2csv/internal/csvout"
	"sql2csv/internal/logx"
	"sql2csv/internal/scan"
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

type accumulator struct {
	mu         sync.Mutex
	insertOK   int
	insertSkip int
	csv        int
	filesFail  int
	tops       map[string]struct{}
	left       map[string]int
	active     map[string]struct{}
	log        *logx.Logger
}

func newAccumulator(log *logx.Logger, files []scan.SQLFile) *accumulator {
	left := make(map[string]int)
	for _, f := range files {
		left[folderName(f)]++
	}
	return &accumulator{
		tops:   make(map[string]struct{}),
		left:   left,
		active: make(map[string]struct{}),
		log:    log,
	}
}

func (a *accumulator) start(file scan.SQLFile) {
	a.mu.Lock()
	defer a.mu.Unlock()
	name := folderName(file)
	if _, ok := a.active[name]; ok {
		return
	}
	a.active[name] = struct{}{}
	a.refreshHang()
}

func (a *accumulator) add(file scan.SQLFile, fr convert.Result) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if fr.OpenErr != nil {
		a.filesFail++
		a.log.Errorf("%s: не удалось открыть: %v", file.Path, fr.OpenErr)
	} else {
		a.insertOK += fr.Created
		a.insertSkip += fr.Skipped
		a.csv += fr.CSV
		if fr.Created > 0 && file.TopFolder != "" {
			a.tops[file.TopFolder] = struct{}{}
		}
	}
	name := folderName(file)
	a.left[name]--
	if a.left[name] == 0 {
		delete(a.active, name)
		a.refreshHang()
		a.log.Linef("папка полностью завершена: %s", name)
	}
}

func (a *accumulator) refreshHang() {
	if len(a.active) == 0 {
		a.log.Hang("")
		return
	}
	a.log.Hang("папка в обработке: " + strings.Join(mapsKeys(a.active), ", "))
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

func processFiles(log *logx.Logger, files []scan.SQLFile) *accumulator {
	acc := newAccumulator(log, files)
	if len(files) == 0 {
		return acc
	}

	groups := groupByDir(files)
	n := workerCount(len(groups))

	reg := csvout.NewRegistry()
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
	return acc
}

// groupByDir собирает файлы одной директории в группу и сортирует их по пути.
// Группы разных папок идут в разные воркеры; внутри папки — строго по возрастанию пути,
// чтобы заголовок CSV был от верхнего успешного INSERT, а не от того, кто добежал первым.
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
		sort.Slice(g, func(i, j int) bool { return g[i].Path < g[j].Path })
		groups = append(groups, g)
	}
	return groups
}

func convertOne(log *logx.Logger, reg *csvout.Registry, file scan.SQLFile) (fr convert.Result) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Errorf("%s: сбой обработки (%v), файл пропущен", file.Path, rec)
			fr = convert.Result{Skipped: 1}
		}
	}()
	return convert.File(log, reg, file)
}
