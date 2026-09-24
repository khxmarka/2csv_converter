// Package logx — лог в консоль. Файл лога не создаётся (§8).
package logx

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// HangWidth — предел ширины строки статуса в колонках. Длиннее — строка
// переносится, и \r затирает только её последний кусок.
const HangWidth = 79

// Logger безопасен для одновременного использования из пула воркеров.
// Hang держит одну строку статуса без перевода строки, пока её не сменят.
type Logger struct {
	mu      sync.Mutex
	w       io.Writer
	hang    string
	hangOff bool
	err     error
	// file — копия лога (_log.txt). Строка прогресса туда не пишется.
	// Ошибка записи в файл не останавливает работу: консоль важнее.
	file    io.Writer
	fileErr error
}

// SetFile дублирует все строки лога (кроме строки прогресса) в w.
func (l *Logger) SetFile(w io.Writer) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.file = w
}

// FileErr — первая ошибка записи в файл лога.
func (l *Logger) FileErr() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fileErr
}

// New пишет лог в w. Если w — файл, но не терминал (перенаправление в файл
// или pipe), строка статуса отключается: там она копила бы \r и пробелы.
func New(w io.Writer) *Logger {
	l := &Logger{w: w}
	if f, ok := w.(*os.File); ok {
		if info, err := f.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
			l.hangOff = true
		}
	}
	return l
}

// Hang показывает msg без '\n'. Повтор с тем же текстом ничего не пишет.
// Пустая строка снимает статус.
func (l *Logger) Hang(msg string) {
	if l == nil || l.hangOff {
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

// FileLinef пишет строку только в файл лога, не в консоль: итоги папок,
// уже записанных в списки состояний, консоль не засоряют.
func (l *Logger) FileLinef(format string, args ...any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.toFile(fmt.Sprintf(format, args...))
}

// FileErrorf — FileLinef для ошибки.
func (l *Logger) FileErrorf(format string, args ...any) {
	l.FileLinef("error: "+format, args...)
}

func (l *Logger) toFile(s string) {
	if l.file == nil || l.fileErr != nil {
		return
	}
	_, l.fileErr = fmt.Fprintln(l.file, s)
}

func (l *Logger) line(s string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.toFile(s)
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
	l.print("\r" + strings.Repeat(" ", DisplayWidth(l.hang)) + "\r")
	l.hang = ""
}

// DisplayWidth — ширина строки в колонках консоли: иероглифы, хангыль, кана
// и полноширинные формы занимают две колонки, остальное — одну.
func DisplayWidth(s string) int {
	n := 0
	for _, r := range s {
		n++
		if isWide(r) {
			n++
		}
	}
	return n
}

func isWide(r rune) bool {
	return (r >= 0x1100 && r <= 0x115F) || // хангыль чамо
		(r >= 0x2E80 && r <= 0xA4CF) || // CJK, кана, радикалы
		(r >= 0xAC00 && r <= 0xD7A3) || // хангыль слоги
		(r >= 0xF900 && r <= 0xFAFF) || // CJK совместимые
		(r >= 0xFE30 && r <= 0xFE4F) ||
		(r >= 0xFF00 && r <= 0xFF60) || (r >= 0xFFE0 && r <= 0xFFE6) || // полноширинные
		(r >= 0x20000 && r <= 0x3FFFD)
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
