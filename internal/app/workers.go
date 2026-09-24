package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"sql2csv/internal/convert"
	"sql2csv/internal/converted"
	"sql2csv/internal/csvout"
	"sql2csv/internal/logx"
	"sql2csv/internal/scan"
	"sql2csv/internal/xlsconv"
)

const maxWorkers = 16

// poolSize — N воркеров на весь запуск: min(GOMAXPROCS, 16), не меньше 1 (§9).
// Числом файлов не ограничивается: INSERT одного файла тоже идут в этот пул.
func poolSize() int {
	return min(max(runtime.GOMAXPROCS(0), 1), maxWorkers)
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
	active     map[string]activeFolder
	blocked    map[string]struct{}
	log        *logx.Logger
	root       string
	byTop      map[string]*topAcc
}

type activeFolder struct {
	name  string
	start time.Time
}

type topAcc struct {
	csv         int
	created     int
	openErr     int
	writeErr    int
	failed      int
	skipTooMany int
	piiSkip     int
	unitFail    int
	sqlN        int
	excelN      int
	splitFail   bool
}

func newAccumulator(log *logx.Logger, root string, files []scan.SQLFile, blocked map[string]struct{}) *accumulator {
	left := make(map[string]int)
	for _, f := range files {
		left[folderKey(f)]++
	}
	return &accumulator{
		tops:    make(map[string]struct{}),
		left:    left,
		active:  make(map[string]activeFolder),
		blocked: blocked,
		log:     log,
		root:    root,
		byTop:   make(map[string]*topAcc),
	}
}

func (a *accumulator) ensureTop(key string) *topAcc {
	st := a.byTop[key]
	if st == nil {
		st = &topAcc{}
		a.byTop[key] = st
	}
	return st
}

func (st *topAcc) failReason() string {
	var parts []string
	if st.piiSkip > 0 {
		parts = append(parts, "всё отсеял фильтр")
	}
	if st.openErr > 0 || st.failed > 0 || st.unitFail > 0 {
		parts = append(parts, "не разобрать")
	}
	if st.writeErr > 0 {
		parts = append(parts, "не записать")
	}
	if len(parts) == 0 {
		return "нечего конвертировать"
	}
	return strings.Join(parts, "; ")
}

func (a *accumulator) start(file scan.SQLFile) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.setActiveLocked(folderKey(file), folderName(file))
}

func (a *accumulator) setActiveLocked(key, name string) {
	if _, ok := a.active[key]; ok {
		return
	}
	a.active[key] = activeFolder{name: name, start: time.Now()}
	a.refreshHang()
}

func (a *accumulator) add(file scan.SQLFile, out fileOutcome) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.recordLocked(file, out)
	a.finishFileLocked(file)
}

func (a *accumulator) record(file scan.SQLFile, out fileOutcome) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.recordLocked(file, out)
}

func (a *accumulator) finishFiles(files []scan.SQLFile) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, file := range files {
		a.finishFileLocked(file)
	}
}

func (a *accumulator) markSplitFail(file scan.SQLFile) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.ensureTop(folderKey(file))
	st.splitFail = true
	st.unitFail++
	a.filesFail++
}

func (a *accumulator) recordLocked(file scan.SQLFile, out fileOutcome) {
	st := a.ensureTop(folderKey(file))
	switch {
	case file.IsCSV():
	case file.IsExcel():
		st.excelN++
	default:
		st.sqlN++
	}
	if out.splitFail {
		st.splitFail = true
	}
	if out.openErr != nil {
		a.filesFail++
		st.openErr++
		a.log.Errorf("%s: не удалось открыть: %v", file.Path, out.openErr)
		return
	}
	if out.skipTooMany {
		st.skipTooMany++
		return
	}
	if out.failed {
		a.filesFail++
		st.failed++
	}
	if out.writeErr != nil {
		a.filesFail++
		st.writeErr++
		a.log.Errorf("%s: не удалось создать CSV: %v", file.Path, out.writeErr)
	}
	a.insertOK += out.created
	a.insertSkip += out.skipped
	a.csv += out.csv
	st.csv += out.csv
	st.created += out.created
	st.piiSkip += out.piiSkip
	st.unitFail += out.unitFail
}

func (a *accumulator) finishFileLocked(file scan.SQLFile) {
	key := folderKey(file)
	a.left[key]--
	if a.left[key] == 0 {
		a.finishTopLocked(key, folderName(file), file.TopFolder)
	}
}

func (a *accumulator) finishTopLocked(key, name, topFolder string) {
	_, cannotComplete := a.blocked[topFolder]
	st := a.ensureTop(key)
	delete(a.active, key)
	a.refreshHang()
	if cannotComplete {
		return
	}
	if st.splitFail {
		a.log.Errorf("папка %s: не нарезать", name)
		return
	}
	if st.csv > 0 {
		if topFolder != "" {
			if err := converted.Append(a.root, topFolder); err != nil {
				a.log.Errorf("не удалось дописать %s: %v", converted.Path(a.root), err)
			} else {
				a.tops[topFolder] = struct{}{}
			}
		}
		a.log.Linef("папка обработана: %s", name)
		return
	}
	if st.sqlN == 0 && st.excelN == 0 {
		a.log.Linef("папка %s: нет файлов", name)
		return
	}
	a.log.Errorf("папка %s: не создано ни одного CSV: %s", name, st.failReason())
}

func (a *accumulator) refreshHang() {
	if len(a.active) == 0 {
		a.log.Hang("")
		return
	}
	folders := make([]activeFolder, 0, len(a.active))
	for _, folder := range a.active {
		folders = append(folders, folder)
	}
	sort.Slice(folders, func(i, j int) bool { return folders[i].name < folders[j].name })
	now := time.Now()
	parts := make([]string, len(folders))
	for i, folder := range folders {
		sec := int(now.Sub(folder.start) / time.Second)
		if sec < 0 {
			sec = 0
		}
		parts[i] = fmt.Sprintf("%s (%d с)", folder.name, sec)
	}
	a.log.Hang("папка в обработке: " + strings.Join(parts, ", "))
}

func (a *accumulator) tickHang() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.active) == 0 {
		return
	}
	a.refreshHang()
}

func (a *accumulator) startHangTicker() func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				a.tickHang()
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
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

		a.setActiveLocked(name, name)
		delete(a.active, name)
		a.refreshHang()
		a.log.Linef("папка %s: нет файлов", name)
		a.mu.Unlock()
	}
}

func processFiles(log *logx.Logger, root string, files []scan.SQLFile, blocked map[string]struct{}) *accumulator {
	acc := newAccumulator(log, root, files, blocked)
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

type fileOutcome struct {
	openErr     error
	writeErr    error
	created     int
	skipped     int
	csv         int
	skipTooMany bool
	failed      bool
	piiSkip     int
	unitFail    int
	splitFail   bool
}

func outcomeFromConvert(fr convert.Result) fileOutcome {
	return fileOutcome{
		openErr:  fr.OpenErr,
		created:  fr.Created,
		skipped:  fr.Skipped,
		csv:      fr.CSV,
		failed:   fr.Failed,
		piiSkip:  fr.PIISkip,
		unitFail: fr.UnitFail,
	}
}

func convertExcel(log *logx.Logger, reg *csvout.Registry, file scan.SQLFile) (out fileOutcome) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Errorf("%s: сбой обработки (%v), файл пропущен", file.Path, rec)
			out = fileOutcome{skipped: 1, failed: true}
		}
	}()
	xr := xlsconv.File(reg, file.Path)
	return fileOutcome{
		openErr:     xr.OpenErr,
		writeErr:    xr.WriteErr,
		csv:         xr.CSV,
		skipTooMany: xr.SkipTooMany,
	}
}

// testDirEnter — крюк теста. В бою nil. Вызывается до файлов директории.
var testDirEnter func(scan.SQLFile)

func processDirGroup(acc *accumulator, log *logx.Logger, reg *csvout.Registry, submit func(func()), group []scan.SQLFile) {
	if len(group) == 0 {
		return
	}
	dir := filepath.Dir(group[0].Path)
	defer func() {
		if rec := recover(); rec != nil {
			log.Errorf("%s: сбой обработки (%v), папка пропущена", dir, rec)
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
	var produced []scan.SQLFile
	for _, file := range sqls {
		acc.start(file)
		out := outcomeFromConvert(convert.Schedule(log, reg, file, submit))
		acc.record(file, out)
		if producedCSV(out) {
			produced = append(produced, file)
		}
	}
	for _, file := range excels {
		acc.start(file)
		out := convertExcel(log, reg, file)
		acc.record(file, out)
		if producedCSV(out) {
			produced = append(produced, file)
		}
	}
	if splitWritten(log, reg, dir) {
		acc.markSplitFail(group[0])
	}
	ours := outputPaths(reg, dir)
	for _, file := range csvs {
		acc.start(file)
		if _, ok := ours[foldPath(file.Path)]; ok {
			acc.record(file, fileOutcome{})
			continue
		}
		acc.record(file, splitForeignCSV(log, reg, file.Path))
	}
	for _, file := range produced {
		removeSource(log, file.Path)
	}
}

// producedCSV — исходник можно удалить (§15): CSV получен и ни одна единица
// файла не провалилась. Иначе удаление унесло бы INSERT или лист, которые
// в CSV не попали (ошибка записи, лишние значения, занятое имя).
func producedCSV(out fileOutcome) bool {
	if out.writeErr != nil || out.failed || out.unitFail > 0 {
		return false
	}
	return out.created > 0 || out.csv > 0
}

func removeSource(log *logx.Logger, path string) {
	if err := os.Remove(path); err != nil {
		log.Errorf("%s: не удалось удалить: %v", path, err)
	}
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
