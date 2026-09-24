package scan

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestValidateRootMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "нет-такой-папки")
	if err := ValidateRoot(missing); err == nil {
		t.Fatalf("ожидалась ошибка для отсутствующего пути %s", missing)
	}
}

func TestFindMissingRootReturnsBlockingSkip(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "нет-такой-папки")
	result, err := Find(missing)
	if err != nil {
		t.Fatalf("ошибка обхода должна быть частью Result: %v", err)
	}
	if len(result.Skips) != 1 || !result.Skips[0].BlocksCompletion || result.Skips[0].Path != missing {
		t.Fatalf("Skips=%+v", result.Skips)
	}
}

func TestValidateRootIsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "db")
	if err := os.WriteFile(file, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRoot(file); err == nil {
		t.Fatalf("ожидалась ошибка: %s не директория", file)
	}
}

func TestValidateRootDir(t *testing.T) {
	if err := ValidateRoot(t.TempDir()); err != nil {
		t.Fatalf("для существующей директории ошибки быть не должно: %v", err)
	}
}

func TestFindOnFixtureTree(t *testing.T) {
	root := filepath.Join("testdata", "tree")

	result, err := Find(root)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}

	want := []SQLFile{
		{Path: filepath.Join(root, "Alpha", "alpha.SQL"), TopFolder: "Alpha"},
		{Path: filepath.Join(root, "Alpha", "x", "y", "deep.sql"), TopFolder: "Alpha"},
		{Path: filepath.Join(root, "Beta", "beta.Sql"), TopFolder: "Beta"},
		{Path: filepath.Join(root, "root_level.sql"), TopFolder: ""},
	}
	if !reflect.DeepEqual(result.Files, want) {
		t.Fatalf("список файлов не совпал\nполучено: %#v\nожидалось: %#v", result.Files, want)
	}
	if len(result.Skips) != 0 {
		t.Fatalf("пропусков на чистой фикстуре быть не должно: %#v", result.Skips)
	}
	if !reflect.DeepEqual(result.TopDirs, []string{"Alpha", "Beta", "Gamma"}) {
		t.Fatalf("верхние каталоги: %v", result.TopDirs)
	}
}

func TestFindSkippingExcludesCompletedTopFolder(t *testing.T) {
	root := filepath.Join("testdata", "tree")
	result, err := FindSkipping(root, map[string]struct{}{"Alpha": {}})
	if err != nil {
		t.Fatal(err)
	}
	want := []SQLFile{
		{Path: filepath.Join(root, "Beta", "beta.Sql"), TopFolder: "Beta"},
		{Path: filepath.Join(root, "root_level.sql"), TopFolder: ""},
	}
	if !reflect.DeepEqual(result.Files, want) {
		t.Fatalf("получено: %#v\nожидалось: %#v", result.Files, want)
	}
	if !reflect.DeepEqual(result.TopDirs, []string{"Beta", "Gamma"}) {
		t.Fatalf("верхние каталоги: %v", result.TopDirs)
	}
}

func TestSkipTopFolderForWalkError(t *testing.T) {
	root := "root"
	if got := skipTopFolder(root, filepath.Join(root, "Alpha", "locked", "file.sql"), false); got != "Alpha" {
		t.Fatalf("TopFolder=%q", got)
	}
	if got := skipTopFolder(root, filepath.Join(root, "Alpha"), true); got != "Alpha" {
		t.Fatalf("прямой верхний каталог: %q", got)
	}
	if got := skipTopFolder(root, filepath.Join(root, "root.sql"), false); got != "" {
		t.Fatalf("корневой файл не должен иметь TopFolder: %q", got)
	}
}

func TestFindDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "outside.sql"), []byte("INSERT INTO t (id) VALUES (1);"), 0o644); err != nil {
		t.Fatal(err)
	}

	real := filepath.Join(root, "Real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(real, "inside.sql")
	if err := os.WriteFile(inside, []byte("INSERT INTO t (id) VALUES (1);"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(root, "LinkedDir")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("создание symlink недоступно (нужны права): %v", err)
		}
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "outside.sql"), filepath.Join(root, "linked.sql")); err != nil {
		t.Fatal(err)
	}

	result, err := Find(root)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}

	want := []SQLFile{{Path: inside, TopFolder: "Real"}}
	if !reflect.DeepEqual(result.Files, want) {
		t.Fatalf("по symlink-ам ходить нельзя\nполучено: %#v\nожидалось: %#v", result.Files, want)
	}
	if len(result.Skips) != 2 {
		t.Fatalf("оба symlink-а должны попасть в пропуски, получено: %#v", result.Skips)
	}
}

// На Windows symlink требует прав, а junction — нет, поэтому проверяем и его:
// именно junction-ы встречаются в реальных деревьях каталогов.
func TestFindDoesNotFollowJunctions(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junction есть только на Windows")
	}

	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "outside.sql"), []byte("INSERT INTO t (id) VALUES (1);"), 0o644); err != nil {
		t.Fatal(err)
	}

	real := filepath.Join(root, "Real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(real, "inside.sql")
	if err := os.WriteFile(inside, []byte("INSERT INTO t (id) VALUES (1);"), 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(root, "Junction")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
		t.Skipf("не удалось создать junction: %v (%s)", err, out)
	}

	result, err := Find(root)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}

	want := []SQLFile{{Path: inside, TopFolder: "Real"}}
	if !reflect.DeepEqual(result.Files, want) {
		t.Fatalf("по junction ходить нельзя\nполучено: %#v\nожидалось: %#v", result.Files, want)
	}
	if len(result.Skips) != 1 || result.Skips[0].Path != link {
		t.Fatalf("junction должен попасть в пропуски, получено: %#v", result.Skips)
	}
}

func TestFindIgnoresNonSQL(t *testing.T) {
	root := filepath.Join("testdata", "tree")
	result, err := Find(root)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	for _, f := range result.Files {
		if strings.EqualFold(filepath.Ext(f.Path), ".sqlx") || strings.HasSuffix(f.Path, "notes.txt") {
			t.Fatalf("не-.sql попал в список: %s", f.Path)
		}
	}
}

func TestFindCollectsExcelIgnoresOtherTables(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(alpha, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]Kind{
		filepath.Join(alpha, "a.xlsx"):   KindXLSX,
		filepath.Join(alpha, "B.XLS"):    KindXLS,
		filepath.Join(alpha, "dump.sql"): KindSQL,
	}
	for path := range files {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"skip.xlsm", "old.xlsb", "calc.ods", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(alpha, name), []byte("no"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := Find(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 3 {
		t.Fatalf("файлов: %#v", result.Files)
	}
	byPath := map[string]Kind{}
	for _, f := range result.Files {
		byPath[f.Path] = f.Kind
		if f.TopFolder != "Alpha" {
			t.Fatalf("TopFolder=%q для %s", f.TopFolder, f.Path)
		}
	}
	for path, want := range files {
		if byPath[path] != want {
			t.Fatalf("%s: kind=%v want=%v", path, byPath[path], want)
		}
	}
}

func TestFindLeavesUnreadableSQLForConverterToReport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("на Windows chmod не отбирает право чтения у владельца")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "Locked")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "secret.sql")
	if err := os.WriteFile(path, []byte("INSERT INTO t (id) VALUES (1);"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	result, err := Find(root)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(result.Files) != 1 || result.Files[0].Path != path {
		t.Fatalf("нечитаемый .sql должен дойти до слоя конвертации: %#v", result.Files)
	}
	if len(result.Skips) != 0 {
		t.Fatalf("scan не должен заранее открывать рабочий файл: %#v", result.Skips)
	}
}

func TestFindCollectsCSVSkipsTempsAndConverted(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(alpha, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := []string{"rows.csv", "DATA.CSV"}
	for _, name := range keep {
		if err := os.WriteFile(filepath.Join(alpha, name), []byte("h\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"converted.txt", ".2csv-left.tmp", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(alpha, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "converted.txt"), []byte("Old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Find(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 2 {
		t.Fatalf("файлы: %#v", result.Files)
	}
	for _, f := range result.Files {
		if f.Kind != KindCSV || f.TopFolder != "Alpha" {
			t.Fatalf("csv: %+v", f)
		}
		base := filepath.Base(f.Path)
		if base == "converted.txt" || strings.Contains(base, ".2csv-") {
			t.Fatalf("служебный файл попал в обход: %s", f.Path)
		}
	}
}

func TestFindOnEmptyDir(t *testing.T) {
	result, err := Find(t.TempDir())
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(result.Files) != 0 || len(result.Skips) != 0 {
		t.Fatalf("пустая директория должна давать пустой результат: %#v", result)
	}
}
