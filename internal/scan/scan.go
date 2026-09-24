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

	"sql2csv/internal/marks"
)

// Kind — тип рабочего файла.
type Kind int

const (
	KindSQL Kind = iota
	KindXLSX
	KindXLS
	KindCSV
	// KindTXT — .txt только для построчной нарезки (readme.txt и служебные
	// списки программы исключены).
	KindTXT
)

// SQLFile — найденный .sql / .xlsx / .xls / .csv / .txt и его место в дереве относительно корня.
type SQLFile struct {
	Path string
	Kind Kind

	// TopFolder — первый сегмент пути относительно корня.
	// Пусто для файлов, лежащих прямо в корне: они обрабатываются при
	// каждом запуске и в списки состояний не попадают.
	TopFolder string
}

// IsExcel — книга .xlsx или .xls.
func (f SQLFile) IsExcel() bool { return f.Kind == KindXLSX || f.Kind == KindXLS }

// IsCSV — файл только для нарезки: .csv или .txt, конвертировать нечего.
func (f SQLFile) IsCSV() bool { return f.Kind == KindCSV || f.Kind == KindTXT }

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

// Find рекурсивно обходит root и собирает пути *.sql, *.xlsx, *.xls, *.csv и *.txt без учёта регистра.
// Symlink-и не раскрываются: и ссылки на каталоги, и ссылки на файлы попадают в Skips.
// Ошибки чтения каталогов возвращаются как блокирующие Skips и не роняют обход.
func Find(root string) (Result, error) {
	return FindSkipping(root, nil)
}

// FindSkipping работает как Find, но целиком исключает уже завершённые верхние
// папки до открытия находящихся в них файлов.
func FindSkipping(root string, completed map[string]struct{}) (Result, error) {
	var res Result
	completed = foldTopNames(completed)

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
			return nil //nolint:nilerr // ошибка файла — пропуск в Skips, обход продолжается (§8)
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

// isCompletedTop: completed уже приведён foldTopNames, поиск — O(1).
func isCompletedTop(root, path string, completed map[string]struct{}) bool {
	if len(completed) == 0 {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || filepath.Dir(rel) != "." {
		return false
	}
	_, ok := completed[foldTopName(rel)]
	return ok
}

// foldTopName — ключ сравнения имени верхней папки: на Windows без учёта
// регистра (§7), как canonicalPath в csvout.
func foldTopName(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(name)
	}
	return name
}

// foldTopNames строит множество ключей один раз на обход: иначе на Windows
// каждая верхняя папка сравнивалась бы со всем списком (O(n²)).
func foldTopNames(names map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for name := range names {
		out[foldTopName(name)] = struct{}{}
	}
	return out
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

// isLink отсекает symlink-и и прочие reparse point-ы Windows (junction, mount point).
func isLink(mode fs.FileMode) bool {
	return mode&(fs.ModeSymlink|fs.ModeIrregular) != 0
}

// IsIgnored — файл не обрабатывается ни на какой глубине: readme.txt и
// служебные файлы программы (_log.txt, _*_done_.txt, _*_passed_.txt).
func IsIgnored(base string) bool {
	return strings.EqualFold(base, "readme.txt") || marks.IsService(base)
}

func workKind(path string) (Kind, bool) {
	base := filepath.Base(path)
	if IsIgnored(base) {
		return 0, false
	}
	lower := strings.ToLower(base)
	if strings.HasPrefix(lower, ".2csv-") && strings.HasSuffix(lower, ".tmp") {
		return 0, false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".sql":
		return KindSQL, true
	case ".xlsx":
		return KindXLSX, true
	case ".xls":
		return KindXLS, true
	case ".csv":
		return KindCSV, true
	case ".txt":
		return KindTXT, true
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
