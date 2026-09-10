// Package scan проверяет корень и находит рабочие файлы (§3 политики).
package scan

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
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
	Path             string
	Reason           string
	TopFolder        string
	BlocksCompletion bool
}

// Result — итог обхода: найденные файлы (отсортированы по пути) и пропуски.
type Result struct {
	Files   []SQLFile
	Skips   []Skip
	TopDirs []string
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
// Ошибки чтения каталогов возвращаются как блокирующие Skips и не роняют обход.
func Find(root string) (Result, error) {
	return FindSkipping(root, nil)
}

// FindSkipping работает как Find, но целиком исключает уже завершённые верхние
// папки до открытия находящихся в них файлов.
func FindSkipping(root string, completed map[string]struct{}) (Result, error) {
	var res Result

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root {
				res.Skips = append(res.Skips, Skip{
					Path:             path,
					Reason:           walkErr.Error(),
					BlocksCompletion: true,
				})
				return nil
			}
			res.Skips = append(res.Skips, Skip{
				Path:             path,
				Reason:           walkErr.Error(),
				TopFolder:        skipTopFolder(root, path, entry != nil && entry.IsDir()),
				BlocksCompletion: true,
			})
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		if entry.IsDir() && isCompletedTop(root, path, completed) {
			return fs.SkipDir
		}
		if isLink(entry.Type()) {
			res.Skips = append(res.Skips, Skip{Path: path, Reason: "symlink или reparse point, не следуем"})
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if top, direct := directTopDir(root, path); direct {
				res.TopDirs = append(res.TopDirs, top)
			}
			return nil
		}
		kind, ok := workKind(path)
		if !ok {
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
	sort.Strings(res.TopDirs)
	return res, nil
}

func isCompletedTop(root, path string, completed map[string]struct{}) bool {
	if len(completed) == 0 {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || filepath.Dir(rel) != "." {
		return false
	}
	if runtime.GOOS != "windows" {
		_, ok := completed[rel]
		return ok
	}
	for name := range completed {
		if strings.EqualFold(name, rel) {
			return true
		}
	}
	return false
}

func directTopDir(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 1 || rel == "." {
		return "", false
	}
	return parts[0], true
}

func skipTopFolder(root, path string, isDir bool) string {
	if isDir {
		if top, direct := directTopDir(root, path); direct {
			return top
		}
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 2 {
		return ""
	}
	return parts[0]
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

// InRootCount — сколько рабочих файлов лежит прямо в корне.
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
