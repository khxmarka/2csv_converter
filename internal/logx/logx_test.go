package logx

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestHangDoesNotReprint(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf)
	log.Hang("папка в обработке: Alpha")
	log.Hang("папка в обработке: Alpha")
	if strings.Count(buf.String(), "папка в обработке: Alpha") != 1 {
		t.Fatalf("статус не должен печататься повторно: %q", buf.String())
	}
}

func TestHangUpdatesWhenSecondsChange(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf)
	log.Hang("папка в обработке: Alpha (0 с)")
	log.Hang("папка в обработке: Alpha (1 с)")
	got := buf.String()
	if !strings.Contains(got, "папка в обработке: Alpha (0 с)") {
		t.Fatalf("нет 0 с: %q", got)
	}
	if !strings.Contains(got, "папка в обработке: Alpha (1 с)") {
		t.Fatalf("нет 1 с: %q", got)
	}
	log.Hang("")
	log.Linef("done")
	got = buf.String()
	tail := got[strings.LastIndex(got, "done"):]
	if strings.Contains(tail, "папка в обработке") {
		t.Fatalf("Hang(\"\") должен снять строку: %q", got)
	}
}

func TestHangThenLineKeepsOneStatus(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf)
	log.Hang("папка в обработке: Alpha")
	log.Hang("")
	log.Linef("папка полностью завершена: Alpha")
	got := buf.String()
	if strings.Count(got, "папка полностью завершена: Alpha") != 1 {
		t.Fatalf("%q", got)
	}
	if strings.Contains(got, "info:") || strings.Contains(got, "warn:") {
		t.Fatalf("лишние уровни: %q", got)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestLoggerCapturesFirstWriteError(t *testing.T) {
	log := New(failingWriter{})
	log.Hang("status")
	log.Errorf("later error")
	if err := log.Err(); err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("Err=%v", err)
	}
}

// В файл или pipe статусная строка не пишется: там она копилась бы \r и
// пробелами раз в секунду. Обычные строки лога идут как раньше.
func TestHangOffWhenNotTerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	log := New(w)
	log.Hang("папка в обработке: Alpha (0 с)")
	log.Linef("папка обработана: Alpha")
	log.Hang("")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "папка обработана: Alpha\n" {
		t.Fatalf("вывод: %q", got)
	}
}

// Иероглиф занимает две колонки: затирание должно покрыть всю строку.
func TestClearHangCoversWideRunes(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf)
	log.Hang("папка: 数据")
	log.Hang("")
	want := "\r" + strings.Repeat(" ", DisplayWidth("папка: 数据")) + "\r"
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("затирание: %q", buf.String())
	}
	if DisplayWidth("папка: 数据") != 11 {
		t.Fatalf("ширина: %d", DisplayWidth("папка: 数据"))
	}
}
