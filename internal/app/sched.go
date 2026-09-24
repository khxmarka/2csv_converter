package app

import (
	"container/heap"
	"sync"

	"sql2csv/internal/csvout"
	"sql2csv/internal/logx"
	"sql2csv/internal/scan"
)

// dirWork — одна директория. top меньше у той верхней папки, которая встретилась раньше:
// её INSERT забирают раньше, и её директории открываются раньше других папок.
type dirWork struct {
	top   int
	group []scan.SQLFile
}

type schedJob struct {
	top int
	seq uint64
	fn  func()
}

type jobHeap []schedJob

func (h jobHeap) Len() int { return len(h) }

func (h jobHeap) Less(i, j int) bool {
	if h[i].top != h[j].top {
		return h[i].top < h[j].top
	}
	return h[i].seq < h[j].seq
}

func (h jobHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *jobHeap) Push(x any) { *h = append(*h, x.(schedJob)) } //nolint:forcetypeassert // container/heap: в кучу кладём только schedJob

func (h *jobHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// runner — один пул на весь запуск.
// Свободный воркер открывает следующую директорию только когда в очереди нет готового INSERT.
// В одной директории по-прежнему один .sql за раз.
type runner struct {
	mu       sync.Mutex
	cond     *sync.Cond
	jobs     jobHeap
	seq      uint64
	dirs     []dirWork
	next     int
	scanners int
	limit    int
	done     bool
	wg       sync.WaitGroup

	acc *accumulator
	log *logx.Logger
	reg *csvout.Registry
}

func orderedDirs(files []scan.SQLFile) []dirWork {
	var out []dirWork
	for top, group := range groupByTop(files) {
		for _, g := range groupByDir(group) {
			if len(g) == 0 {
				continue
			}
			out = append(out, dirWork{top: top, group: g})
		}
	}
	return out
}

func runDirs(acc *accumulator, log *logx.Logger, reg *csvout.Registry, files []scan.SQLFile) {
	dirs := orderedDirs(files)
	if len(dirs) == 0 {
		return
	}
	n := poolSize()
	r := &runner{
		dirs:  dirs,
		limit: n,
		acc:   acc,
		log:   log,
		reg:   reg,
	}
	r.cond = sync.NewCond(&r.mu)
	for range n {
		r.wg.Add(1)
		go r.worker()
	}
	r.wg.Wait()
}

func (r *runner) submit(top int, fn func()) {
	r.mu.Lock()
	for len(r.jobs) >= r.limit && !r.done {
		r.cond.Wait()
	}
	if r.done {
		r.mu.Unlock()
		fn()
		return
	}
	r.seq++
	heap.Push(&r.jobs, schedJob{top: top, seq: r.seq, fn: fn})
	r.cond.Broadcast()
	r.mu.Unlock()
}

func (r *runner) fillLocked() {
	for !r.done && len(r.jobs) == 0 && r.next < len(r.dirs) && r.scanners < r.limit {
		dw := r.dirs[r.next]
		r.next++
		r.scanners++
		go r.scan(dw)
	}
}

func (r *runner) scan(dw dirWork) {
	defer func() {
		r.mu.Lock()
		r.scanners--
		r.fillLocked()
		if r.scanners == 0 && r.next >= len(r.dirs) && len(r.jobs) == 0 {
			r.done = true
		}
		r.cond.Broadcast()
		r.mu.Unlock()
	}()
	processDirGroup(r.acc, r.log, r.reg, func(fn func()) { r.submit(dw.top, fn) }, dw.group)
}

func (r *runner) worker() {
	defer r.wg.Done()
	for {
		r.mu.Lock()
		for len(r.jobs) == 0 && !r.done {
			r.fillLocked()
			if len(r.jobs) > 0 {
				break
			}
			if r.scanners == 0 && r.next >= len(r.dirs) {
				r.done = true
				r.cond.Broadcast()
				break
			}
			r.cond.Wait()
		}
		if len(r.jobs) == 0 {
			r.mu.Unlock()
			return
		}
		item := heap.Pop(&r.jobs).(schedJob) //nolint:forcetypeassert // container/heap: в куче только schedJob
		if len(r.jobs) == 0 {
			r.fillLocked()
		}
		r.cond.Broadcast()
		r.mu.Unlock()
		item.fn()
	}
}
