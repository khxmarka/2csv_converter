package logx

import (
	"bytes"
	"errors"
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
