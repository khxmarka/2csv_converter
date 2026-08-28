package logx

import (
	"bytes"
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
