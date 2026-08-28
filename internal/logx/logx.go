// Package logx — лог в консоль. Файл лога не создаётся (§8).
package logx

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode/utf8"
)

// Logger безопасен для одновременного использования из пула воркеров.
// Hang держит одну строку статуса без перевода строки, пока её не сменят.
type Logger struct {
	mu   sync.Mutex
	w    io.Writer
	hang string
}

func New(w io.Writer) *Logger {
	return &Logger{w: w}
}

// Hang показывает msg без '\n'. Повтор с тем же текстом ничего не пишет.
// Пустая строка снимает статус.
func (l *Logger) Hang(msg string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hang == msg {
		return
	}
	l.clearHang()
	if msg == "" {
		return
	}
	fmt.Fprint(l.w, msg)
	l.hang = msg
}

func (l *Logger) Infof(format string, args ...any) {
	l.line("info: " + fmt.Sprintf(format, args...))
}

func (l *Logger) Warnf(format string, args ...any) {
	l.line("warn: " + fmt.Sprintf(format, args...))
}

func (l *Logger) Errorf(format string, args ...any) {
	l.line("error: " + fmt.Sprintf(format, args...))
}

func (l *Logger) Linef(format string, args ...any) {
	l.line(fmt.Sprintf(format, args...))
}

func (l *Logger) line(s string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	saved := l.hang
	l.clearHang()
	fmt.Fprintln(l.w, s)
	if saved != "" {
		fmt.Fprint(l.w, saved)
		l.hang = saved
	}
}

func (l *Logger) clearHang() {
	if l.hang == "" {
		return
	}
	n := utf8.RuneCountInString(l.hang)
	fmt.Fprint(l.w, "\r"+strings.Repeat(" ", n)+"\r")
	l.hang = ""
}
