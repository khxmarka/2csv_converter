// Package logx writes synchronized console output and an optional cumulative log.
package logx

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// HangWidth limits status text to one terminal line so carriage-return updates remain readable.
const HangWidth = 79

// Logger is safe for concurrent use by workers. Hang maintains one replaceable
// status line without writing it to the cumulative log.
type Logger struct {
	mu      sync.Mutex
	w       io.Writer
	hang    string
	hangOff bool
	err     error
	// file receives persistent log lines but not transient progress. File write
	// failures are reported without stopping the conversion.
	file    io.Writer
	fileErr error
}

// SetFile duplicates subsequent non-progress log lines to w.
func (l *Logger) SetFile(w io.Writer) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.file = w
}

// FileErr returns the first persistent-log write error.
func (l *Logger) FileErr() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fileErr
}

// New writes console output to w. Replaceable status output is disabled when w
// is not a terminal to avoid accumulating carriage-return updates in files and pipes.
func New(w io.Writer) *Logger {
	l := &Logger{w: w}
	if f, ok := w.(*os.File); ok {
		if info, err := f.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
			l.hangOff = true
		}
	}
	return l
}

// Hang displays msg without a newline. Repeated text is suppressed, and an empty
// message clears the current status line.
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

// FileLinef writes only to the persistent log. It keeps already completed
// folder summaries out of the console.
func (l *Logger) FileLinef(format string, args ...any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.toFile(fmt.Sprintf(format, args...))
}

// FileErrorf writes a formatted error only to the persistent log.
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

// DisplayWidth returns terminal column width, counting CJK and full-width runes as two columns.
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
	return (r >= 0x1100 && r <= 0x115F) || // Hangul Jamo.
		(r >= 0x2E80 && r <= 0xA4CF) || // CJK, kana, and radicals.
		(r >= 0xAC00 && r <= 0xD7A3) || // Hangul syllables.
		(r >= 0xF900 && r <= 0xFAFF) || // CJK compatibility ideographs.
		(r >= 0xFE30 && r <= 0xFE4F) ||
		(r >= 0xFF00 && r <= 0xFF60) || (r >= 0xFFE0 && r <= 0xFFE6) || // Full-width forms.
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

// Err returns the first persistent-log write error.
func (l *Logger) Err() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}
