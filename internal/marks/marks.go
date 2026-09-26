// Package marks ведёт накопительные списки верхних папок в корне обхода:
// что уже сконвертировано и что уже нарезано. Конверт и нарезка — разные
// состояния: папку можно убрать из одного списка и повторить только этот этап.
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

// List — файл-список верхних папок в корне.
type List string

const (
	// ConvertDone — из .sql/Excel папки получен хотя бы один CSV.
	ConvertDone List = "_convert_done_.txt"
	// ConvertPassed — конверт папку прошёл без результата: конвертировать
	// нечего, всё отсеял фильтр или не разобралось. Повторно не открывается.
	ConvertPassed List = "_convert_passed_.txt"
	// SplitDone — в папке нарезан хотя бы один .csv/.txt.
	SplitDone List = "_splitter_done_.txt"
	// SplitPassed — нарезка папку проверила, резать нечего или не вышло.
	SplitPassed List = "_splitter_passed_.txt"
)

// LogName — подробный лог запусков в корне.
const LogName = "_log.txt"

// Lists — все списки состояний.
var Lists = []List{ConvertDone, ConvertPassed, SplitDone, SplitPassed}

// IsService сообщает, что имя файла — служебный файл программы в корне:
// такие файлы не конвертируются и не режутся.
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

// Fold — ключ сравнения имени папки: на Windows без учёта регистра.
func Fold(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(name)
	}
	return name
}

// FoldSet приводит набор имён к ключам Fold.
func FoldSet(names map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for name := range names {
		out[Fold(name)] = struct{}{}
	}
	return out
}

var fileMu sync.Mutex

// Path возвращает путь списка l в корне root.
func Path(root string, l List) string {
	return filepath.Join(root, string(l))
}

// Read читает имена папок из списка l. Отсутствующий файл — пустой набор.
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

// Append дописывает имя верхней папки в список l, не перезаписывая уже
// накопленное. Повторное имя не добавляется.
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
