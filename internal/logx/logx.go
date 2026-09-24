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
	err  error
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
	l.print(msg)
	l.hang = msg
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
	l.println(s)
	if saved != "" {
		l.print(saved)
		l.hang = saved
	}
}

func (l *Logger) clearHang() {
	if l.hang == "" {
		return
	}
	n := utf8.RuneCountInString(l.hang)
	l.print("\r" + strings.Repeat(" ", n) + "\r")
	l.hang = ""
}

func (l *Logger) print(s string) {
	if l.err != nil {
		return
	}
	_, l.err = fmt.Fprint(l.w, s)
}

func (l *Logger) println(s string) {
	if l.err != nil {
		return
	}
	_, l.err = fmt.Fprintln(l.w, s)
}

// Err возвращает первую ошибку записи в лог.
func (l *Logger) Err() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}
