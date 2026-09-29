package app

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"sql2csv/internal/logx"
	"sql2csv/internal/marks"
	"sql2csv/internal/scan"
)

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
	// convSkip/splitSkip — верхние папки (ключи marks.Fold), уже записанные
	// в списки конверта/нарезки: этот этап для них не выполняется.
	convSkip  map[string]struct{}
	splitSkip map[string]struct{}
}

// phases — какие этапы идут для верхней папки. Файлы прямо в корне
// в списки не пишутся и обрабатываются каждый запуск.
func (a *accumulator) phases(topFolder string) (convert, split bool) {
	if topFolder == "" {
		return true, true
	}
	key := marks.Fold(topFolder)
	_, convDone := a.convSkip[key]
	_, splitDone := a.splitSkip[key]
	return !convDone, !splitDone
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
	// splitDone — нарезан хотя бы один файл (свежий CSV или лежавший .csv/.txt).
	splitDone bool
	sources   []string
}

func (st *topAcc) convertFailed() bool {
	return st.openErr > 0 || st.writeErr > 0 || st.failed > 0 || st.unitFail > 0
}

func newAccumulator(log *logx.Logger, root string, files []scan.SQLFile, blocked map[string]struct{}) *accumulator {
	left := make(map[string]int)
	for _, f := range files {
		left[folderKey(f)]++
	}
	return &accumulator{
		convSkip:  make(map[string]struct{}),
		splitSkip: make(map[string]struct{}),
		tops:      make(map[string]struct{}),
		left:      left,
		active:    make(map[string]activeFolder),
		blocked:   blocked,
		log:       log,
		root:      root,
		byTop:     make(map[string]*topAcc),
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
	// Папка уже в одном из списков — в строку прогресса не выводится.
	if convert, split := a.phases(file.TopFolder); !convert || !split {
		return
	}
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

// markSplit учитывает нарезку CSV, записанных этим запуском в директории.
func (a *accumulator) markSplit(file scan.SQLFile, failed, split bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.ensureTop(folderKey(file))
	if split {
		st.splitDone = true
	}
	if failed {
		st.splitFail = true
		a.filesFail++
	}
}

func (a *accumulator) markGroupFailure(file scan.SQLFile) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.ensureTop(folderKey(file))
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
	if out.split {
		st.splitDone = true
		a.csv++
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
	if producedCSV(out) {
		st.sources = append(st.sources, file.Path)
	}
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
	convert, split := a.phases(topFolder)
	stagesOK := !st.convertFailed() && !st.splitFail
	stateOK := stagesOK && a.recordListsLocked(topFolder, convert, split, st.csv > 0, st.splitDone)
	if stateOK {
		for _, path := range st.sources {
			if err := removeSource(path); err != nil {
				a.filesFail++
				a.log.Errorf("%s: не удалось удалить: %v", path, err)
			}
		}
	}
	say, sayErr := a.log.Linef, a.log.Errorf
	if !convert || !split {
		// Папка уже в одном из списков: итог только в _log.txt.
		say, sayErr = a.log.FileLinef, a.log.FileErrorf
	}
	if st.splitFail {
		sayErr("папка %s: не нарезать", name)
		return
	}
	if st.convertFailed() {
		sayErr("папка %s: обработана с ошибками", name)
		return
	}
	if st.csv > 0 || st.splitDone {
		say("папка обработана: %s", name)
		return
	}
	if st.sqlN == 0 && st.excelN == 0 {
		say("папка %s: нет файлов", name)
		return
	}
	sayErr("папка %s: не создано ни одного CSV: %s", name, st.failReason())
}

// recordListsLocked persists only stages that completed without errors.
// A done list has output; a passed list means that the stage had no work.
func (a *accumulator) recordListsLocked(topFolder string, convert, split, converted, splitDone bool) bool {
	if topFolder == "" {
		return true
	}
	add := func(l marks.List) bool {
		if err := appendState(a.root, l, topFolder); err != nil {
			a.log.Errorf("не удалось дописать %s: %v", marks.Path(a.root, l), err)
			a.filesFail++
			return false
		}
		if l == marks.ConvertDone || l == marks.SplitDone {
			a.tops[topFolder] = struct{}{}
		}
		return true
	}
	if split {
		list := marks.SplitPassed
		if splitDone {
			list = marks.SplitDone
		}
		if !add(list) {
			return false
		}
	}
	if convert {
		list := marks.ConvertPassed
		if converted {
			list = marks.ConvertDone
		}
		if !add(list) {
			return false
		}
	}
	return true
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
	label := func(f activeFolder) string {
		return fmt.Sprintf("%s (%d с)", f.name, max(int(now.Sub(f.start)/time.Second), 0))
	}
	parts := make([]string, len(folders))
	for i, folder := range folders {
		parts[i] = label(folder)
	}
	const prefix = "папка в обработке: "
	line := prefix + strings.Join(parts, ", ")
	if logx.DisplayWidth(line) > logx.HangWidth {
		// §8: все не влезают — самая давняя. Строка длиннее экрана переносится,
		// и \r затирает только её хвост: каждую секунду в консоли мусор.
		oldest := slices.MinFunc(folders, func(x, y activeFolder) int { return x.start.Compare(y.start) })
		line = truncateWidth(prefix+label(oldest), logx.HangWidth)
	}
	a.log.Hang(line)
}

// truncateWidth обрезает s до width колонок, заменяя хвост на «…».
func truncateWidth(s string, width int) string {
	if logx.DisplayWidth(s) <= width {
		return s
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := logx.DisplayWidth(string(r))
		if w+rw > width-1 {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "…"
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
	return a.insertOK, a.insertSkip, a.csv, a.filesFail, maps.Clone(a.tops)
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
		convert, split := a.phases(name)
		a.recordListsLocked(name, convert, split, false, false)
		if convert && split {
			a.log.Linef("папка %s: нет файлов", name)
		} else {
			a.log.FileLinef("папка %s: нет файлов", name)
		}
		a.mu.Unlock()
	}
}

var appendState = marks.Append
