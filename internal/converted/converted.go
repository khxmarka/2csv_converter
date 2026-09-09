// Package converted ведёт накопительный converted.txt в корне обхода.
package converted

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

const FileName = "converted.txt"

var fileMu sync.Mutex

// Path возвращает converted.txt в выбранном корне обхода (или тестовом корне).
func Path(root string) string {
	return filepath.Join(root, FileName)
}

// Read читает уже завершённые верхние папки. Отсутствующий файл означает
// пустой набор.
func Read(root string) (map[string]struct{}, error) {
	fileMu.Lock()
	defer fileMu.Unlock()
	return readUnlocked(root)
}

func readUnlocked(root string) (map[string]struct{}, error) {
	raw, err := os.ReadFile(Path(root))
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
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line != "" {
			out[line] = struct{}{}
		}
	}
	return out
}

// Append дописывает имя завершённой верхней папки, не перезаписывая уже
// накопленный список. Повторное имя не добавляется.
func Append(root, name string) error {
	if name == "" || strings.ContainsAny(name, "\r\n") {
		return fmt.Errorf("некорректное имя верхней папки %q", name)
	}

	fileMu.Lock()
	defer fileMu.Unlock()

	path := Path(root)
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
