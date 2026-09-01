// Package scan проверяет корень и находит рабочие файлы (§3 политики).
package scan

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Kind — тип рабочего файла.
type Kind int

const (
	KindSQL Kind = iota
	KindXLSX
	KindXLS
)

// SQLFile — найденный .sql / .xlsx / .xls и его место в дереве относительно корня.
type SQLFile struct {
	Path string
	Kind Kind

	// TopFolder — первый сегмент пути относительно корня.
	// Пусто для файлов, лежащих прямо в корне: они конвертируются,
	// но в converted.txt не отражаются (§3, §7).
	TopFolder string
}

// InRoot сообщает, что файл лежит прямо в корне, а не в подпапке.
func (f SQLFile) InRoot() bool { return f.TopFolder == "" }

// IsExcel — книга .xlsx или .xls.
func (f SQLFile) IsExcel() bool { return f.Kind == KindXLSX || f.Kind == KindXLS }

// Skip — единица, пропущенная при обходе: symlink или недоступный каталог.
type Skip struct {
	Path   string
	Reason string
}

// Result — итог обхода: найденные файлы (отсортированы по пути) и пропуски.
type Result struct {
	Files []SQLFile
	Skips []Skip
}

// ValidateRoot проверяет, что корень существует и является директорией.
func ValidateRoot(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("корень %s не найден", path)
		}
		return fmt.Errorf("не удалось прочитать корень %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("корень %s существует, но не является директорией", path)
	}
	return nil
}

// Find рекурсивно обходит root и собирает пути *.sql, *.xlsx и *.xls без учёта регистра.
// Symlink-и не раскрываются: и ссылки на каталоги, и ссылки на файлы попадают в Skips.
// Ошибка чтения отдельного каталога не прерывает обход, ошибка чтения самого корня — прерывает.
func Find(root string) (Result, error) {
	var res Result

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			res.Skips = append(res.Skips, Skip{Path: path, Reason: walkErr.Error()})
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		if isLink(entry.Type()) {
			res.Skips = append(res.Skips, Skip{Path: path, Reason: "symlink или reparse point, не следуем"})
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		kind, ok := workKind(path)
		if !ok {
			return nil
		}
		if err := probeOpen(path); err != nil {
			res.Skips = append(res.Skips, Skip{Path: path, Reason: err.Error()})
			return nil
		}
		top, err := topFolder(root, path)
		if err != nil {
			res.Skips = append(res.Skips, Skip{Path: path, Reason: err.Error()})
			return nil
		}
		res.Files = append(res.Files, SQLFile{Path: path, Kind: kind, TopFolder: top})
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("обход %s: %w", root, err)
	}

	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].Path < res.Files[j].Path })
	sort.Slice(res.Skips, func(i, j int) bool { return res.Skips[i].Path < res.Skips[j].Path })
	return res, nil
}

// TopFolders возвращает отсортированный список верхних папок, в которых нашлись рабочие файлы.
func (r Result) TopFolders() []string {
	seen := make(map[string]struct{})
	for _, f := range r.Files {
		if !f.InRoot() {
			seen[f.TopFolder] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// InRootCount — сколько .sql лежит прямо в корне.
func (r Result) InRootCount() int {
	n := 0
	for _, f := range r.Files {
		if f.InRoot() {
			n++
		}
	}
	return n
}

// isLink отсекает symlink-и и прочие reparse point-ы Windows (junction, mount point).
func isLink(mode fs.FileMode) bool {
	return mode&(fs.ModeSymlink|fs.ModeIrregular) != 0
}

func workKind(path string) (Kind, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".sql":
		return KindSQL, true
	case ".xlsx":
		return KindXLSX, true
	case ".xls":
		return KindXLS, true
	default:
		return 0, false
	}
}

// probeOpen проверяет, что файл можно открыть, и сразу закрывает его.
// Содержимое не читается: разбор INSERT — отдельный слой.
func probeOpen(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("не удалось открыть: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("не удалось закрыть после проверки: %w", err)
	}
	return nil
}

func topFolder(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 1 {
		return "", nil
	}
	return parts[0], nil
}
