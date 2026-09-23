package convert

import (
	"sync"
	"testing"
	"time"
)

func TestCommitQueueKeepsReserveOrder(t *testing.T) {
	q := newCommitQ()
	done := q.start()
	fill1 := q.reserve()
	fill2 := q.reserve()

	var mu sync.Mutex
	var got []int
	go func() {
		fill2(func() {
			mu.Lock()
			got = append(got, 2)
			mu.Unlock()
		})
	}()
	time.Sleep(20 * time.Millisecond)
	fill1(func() {
		mu.Lock()
		got = append(got, 1)
		mu.Unlock()
	})
	q.close()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("порядок: %v", got)
	}
}

func TestCommitQueueSurvivesApplyPanic(t *testing.T) {
	q := newCommitQ()
	done := q.start()
	fill1 := q.reserve()
	fill2 := q.reserve()
	var got int
	fill1(func() { panic("boom") })
	fill2(func() { got = 2 })
	q.close()
	<-done
	if got != 2 {
		t.Fatal("после паники в одном INSERT остальные должны записаться")
	}
}

func TestCommitQueueBoundsInFlight(t *testing.T) {
	q := newCommitQ()
	q.limit = 1
	done := q.start()
	fill1 := q.reserve()

	second := make(chan struct{})
	go func() {
		fill2 := q.reserve()
		close(second)
		fill2(func() {})
	}()

	select {
	case <-second:
		t.Fatal("второй INSERT разобрали, пока первый не записан")
	case <-time.After(30 * time.Millisecond):
	}
	fill1(func() {})
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("очередь не сдвинулась после записи")
	}
	q.close()
	<-done
}
