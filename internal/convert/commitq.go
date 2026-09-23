package convert

import "sync"

// commitInFlight — сколько INSERT одного файла можно разобрать, пока первый
// ещё не записан. Без потолка один медленный Commit оставляет на диске
// десятки тысяч .2csv-*.tmp и процесс упирается в каталог/память.
const commitInFlight = 16

// commitQ применяет функции строго в порядке Reserve/Enqueue.
// Поздний слот может стать готовым раньше раннего и ждёт.
type commitQ struct {
	mu     sync.Mutex
	cond   *sync.Cond
	slots  []func()
	ready  []bool
	next   int
	limit  int
	closed bool
}

func newCommitQ() *commitQ {
	q := &commitQ{limit: commitInFlight}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *commitQ) start() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			fn, ok := q.pop()
			if !ok {
				return
			}
			if fn == nil {
				continue
			}
			func() {
				defer func() { _ = recover() }()
				fn()
			}()
		}
	}()
	return done
}

func (q *commitQ) pop() (func(), bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for {
		if q.next < len(q.slots) && q.ready[q.next] {
			fn := q.slots[q.next]
			q.slots[q.next] = nil
			q.next++
			q.cond.Broadcast()
			return fn, true
		}
		if q.closed && q.next >= len(q.slots) {
			return nil, false
		}
		q.cond.Wait()
	}
}

func (q *commitQ) reserve() func(func()) {
	q.mu.Lock()
	for q.limit > 0 && len(q.slots)-q.next >= q.limit && !q.closed {
		q.cond.Wait()
	}
	i := len(q.slots)
	q.slots = append(q.slots, nil)
	q.ready = append(q.ready, false)
	q.mu.Unlock()
	return func(fn func()) {
		q.mu.Lock()
		q.slots[i] = fn
		q.ready[i] = true
		q.cond.Broadcast()
		q.mu.Unlock()
	}
}

func (q *commitQ) enqueue(fn func()) {
	fill := q.reserve()
	fill(fn)
}

func (q *commitQ) close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
}
