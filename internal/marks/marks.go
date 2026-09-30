// Package marks maintains persistent conversion and splitting state for
// top-level input folders. Each stage can be reset independently.
package marks

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// List identifies a persistent top-level folder state file.
type List string

const (
	// ConvertDone records folders where SQL or Excel conversion produced CSV.
	ConvertDone List = "_convert_done_.txt"
	// ConvertPassed records folders that completed conversion without output.
	ConvertPassed List = "_convert_passed_.txt"
	// SplitDone records folders where at least one CSV or text file was split.
	SplitDone List = "_splitter_done_.txt"
	// SplitPassed records folders that completed splitting without output.
	SplitPassed List = "_splitter_passed_.txt"
)

// LogName is the cumulative run log stored in each input root.
const LogName = "_log.txt"

// Lists contains every persistent stage list.
var Lists = []List{ConvertDone, ConvertPassed, SplitDone, SplitPassed}

// IsService reports whether name belongs to application state and must not be processed.
func IsService(base string) bool {
	if strings.EqualFold(base, LogName) {
		return true
	}
	for _, l := range Lists {
		if strings.EqualFold(base, string(l)) {
			return true
		}
	}
	return false
}

// Fold returns the case-insensitive comparison key used for Windows folder names.
func Fold(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(name)
	}
	return name
}

// FoldSet converts folder names to Fold comparison keys.
func FoldSet(names map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for name := range names {
		out[Fold(name)] = struct{}{}
	}
	return out
}

var fileMu sync.Mutex

// Path returns the path of list l within root.
func Path(root string, l List) string {
	return filepath.Join(root, string(l))
}

// Read loads folder names from list l. A missing list is treated as empty.
func Read(root string, l List) (map[string]struct{}, error) {
	fileMu.Lock()
	defer fileMu.Unlock()
	raw, err := os.ReadFile(Path(root, l))
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]struct{}), nil
		}
		return nil, err
	}
	return decode(raw), nil
}

func decode(raw []byte) map[string]struct{} {
	out := make(map[string]struct{})
	complete := bytes.LastIndexByte(raw, '\n') + 1
	raw = raw[:complete]
	text := strings.TrimPrefix(string(raw), "\uFEFF")
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line != "" {
			out[line] = struct{}{}
		}
	}
	return out
}

// Append adds a top-level folder to list l without duplicating existing entries.
func Append(root string, l List, name string) error {
	if name == "" || strings.ContainsAny(name, "\r\n") {
		return fmt.Errorf("некорректное имя верхней папки %q", name)
	}

	fileMu.Lock()
	defer fileMu.Unlock()

	path := Path(root, l)
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if containsName(decode(raw), name) {
		return nil
	}
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		complete := bytes.LastIndexByte(raw, '\n') + 1
		if err := os.Truncate(path, int64(complete)); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(name + "\n")
	syncErr := f.Sync()
	closeErr := f.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}

func containsName(names map[string]struct{}, name string) bool {
	if runtime.GOOS != "windows" {
		_, ok := names[name]
		return ok
	}
	for existing := range names {
		if strings.EqualFold(existing, name) {
			return true
		}
	}
	return false
}
