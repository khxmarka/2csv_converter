// Package scan validates an input root and discovers supported files.
package scan

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sql2csv/internal/marks"
)

// Kind identifies a supported input file type.
type Kind int

const (
	KindSQL Kind = iota
	KindXLSX
	KindXLS
	KindCSV
	// KindTXT marks text files for line-based splitting only.
	KindTXT
)

// SQLFile describes a discovered input and its location relative to the root.
type SQLFile struct {
	Path string
	Kind Kind

	// TopFolder is the first path segment below the root. It is empty for files
	// stored directly in the root, which are processed on every run.
	TopFolder string
}

// IsExcel reports whether the input is an XLSX or XLS workbook.
func (f SQLFile) IsExcel() bool { return f.Kind == KindXLSX || f.Kind == KindXLS }

// IsCSV reports whether the input is handled only by the splitting stage.
func (f SQLFile) IsCSV() bool { return f.Kind == KindCSV || f.Kind == KindTXT }

// Skip describes an inaccessible entry or a link rejected during discovery.
type Skip struct {
	Path             string
	Reason           string
	TopFolder        string
	BlocksCompletion bool
}

// Result contains path-sorted inputs and blocking discovery skips.
type Result struct {
	Files   []SQLFile
	Skips   []Skip
	TopDirs []string
}

// ValidateRoot verifies that root exists and is a directory.
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

// FindSkipping recursively scans root while excluding completed top-level directories.
// It reports unreadable entries and links as blocking skips instead of following them.
func FindSkipping(root string, completed map[string]struct{}) (Result, error) {
	var res Result
	completed = marks.FoldSet(completed)

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
			return nil //nolint:nilerr // The error is recorded in Skips so discovery can continue.
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

// isCompletedTop expects completed to contain FoldSet keys for constant-time lookup.
func isCompletedTop(root, path string, completed map[string]struct{}) bool {
	if len(completed) == 0 {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || filepath.Dir(rel) != "." {
		return false
	}
	_, ok := completed[marks.Fold(rel)]
	return ok
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

// isLink rejects symbolic links and Windows reparse points such as junctions and mount points.
func isLink(mode fs.FileMode) bool {
	return mode&(fs.ModeSymlink|fs.ModeIrregular) != 0
}

// IsIgnored reports whether a file is metadata or application state at any depth.
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
