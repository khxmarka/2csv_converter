package app

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sql2csv/internal/converted"
	"sql2csv/internal/csvout"
	"sql2csv/internal/logx"
	"sql2csv/internal/scan"
	"sql2csv/internal/xlsconv"
)

type failingLogWriter struct{}

func (failingLogWriter) Write([]byte) (int, error) {
	return 0, errors.New("stderr unavailable")
}

func copySQLFixture(t *testing.T, fixture, dst string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testfiles", fixture))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunMissingRoot(t *testing.T) {
	var buf bytes.Buffer
	log := logx.New(&buf)
	missing := filepath.Join(t.TempDir(), "нет-такой-папки")

	_, err := Run(log, missing)
	if err == nil {
		t.Fatal("ожидалась ошибка для отсутствующего корня")
	}
	if _, statErr := os.Stat(converted.Path(missing)); statErr == nil {
		t.Fatal("без корня converted.txt создавать нельзя")
	}
}

func TestRootLockRejectsConcurrentRunAndReleases(t *testing.T) {
	root := t.TempDir()
	release, err := acquireRootLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireRootLock(root); err == nil {
		t.Fatal("второй запуск того же корня должен быть отклонён")
	}
	release()
	releaseAgain, err := acquireRootLock(root)
	if err != nil {
		t.Fatalf("lock не освобождён: %v", err)
	}
	releaseAgain()
}

func TestRunReportsLogWriteFailure(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dump.sql"), []byte(
		"INSERT INTO users (email) VALUES ('a@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(logx.New(failingLogWriter{}), root); err == nil ||
		!strings.Contains(err.Error(), "не удалось писать лог") {
		t.Fatalf("ожидалась ошибка stderr, получено %v", err)
	}
}

func TestConvertedReadAndAppendErrorsDoNotLoseCSV(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(converted.Path(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dump.sql"), []byte(
		"INSERT INTO users (email) VALUES ('a@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	res, err := Run(logx.New(&buf), root)
	if err != nil {
		t.Fatal(err)
	}
	if res.CSV != 1 || len(res.SuccessTops) != 0 {
		t.Fatalf("результат: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "users.csv")); err != nil {
		t.Fatalf("CSV должен сохраниться: %v", err)
	}
	log := buf.String()
	if !strings.Contains(log, "не удалось прочитать") || !strings.Contains(log, "не удалось дописать") {
		t.Fatalf("обе ошибки converted.txt должны логироваться:\n%s", log)
	}
}

func TestRunConvertedTxtTopFoldersOnly(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "Alpha", "x", "y")
	beta := filepath.Join(root, "Beta")
	gamma := filepath.Join(root, "Gamma")
	if err := os.MkdirAll(alpha, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(beta, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(gamma, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "converted.txt"), []byte("OLD\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	twoTables := `
INSERT INTO users (id, name) VALUES (1, 'Ann');
INSERT INTO orders (id) VALUES (9);
`
	sameTable := `
INSERT INTO t (email, phone) VALUES (1, 'a');
INSERT INTO t (email, phone) VALUES (2, 'b');
`
	if err := os.WriteFile(filepath.Join(alpha, "a.sql"), []byte(twoTables), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beta, "b.sql"), []byte(sameTable), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "root.sql"), []byte("INSERT INTO root_table (email, phone) VALUES (1, '555');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gamma, "skip.sql"), []byte("INSERT INTO t SELECT 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	res, err := Run(logx.New(&buf), root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, p := range []string{
		filepath.Join(alpha, "users.csv"),
		filepath.Join(beta, "t.csv"),
		filepath.Join(root, "root_table.csv"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("нет %s: %v\nлог:\n%s", p, err, buf.String())
		}
	}

	raw, err := os.ReadFile(converted.Path(root))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("BOM в converted.txt запрещён")
	}
	done, err := converted.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"OLD", "Alpha", "Beta"} {
		if _, ok := done[name]; !ok {
			t.Fatalf("в converted.txt нет %s: %q", name, raw)
		}
	}
	if _, ok := done["Gamma"]; ok {
		t.Fatalf("папка без CSV не должна быть в converted.txt: %q", raw)
	}
	if len(done) != 3 {
		t.Fatalf("converted.txt содержит лишние имена: %q", raw)
	}
	if strings.Join(res.SuccessTops, ",") != "Alpha,Beta" {
		t.Fatalf("верхние папки: %v", res.SuccessTops)
	}
	if res.CSV != 3 {
		t.Fatalf("CSV=%d, ожидалось 3 файла (orders отфильтрован, Beta.t склеен)", res.CSV)
	}
	if !strings.Contains(buf.String(), "error: папка Gamma: не создано ни одного CSV: нечего конвертировать") {
		t.Fatalf("Gamma без CSV должна дать причину:\n%s", buf.String())
	}
	if res.InsertOK != 4 {
		t.Fatalf("INSERT=%d", res.InsertOK)
	}
	if _, err := os.Stat(filepath.Join(alpha, "orders.csv")); !os.IsNotExist(err) {
		t.Fatalf("orders.csv не должен создаваться, err=%v", err)
	}
}

func TestSecondRunSkipsCompletedTopFolder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sqlPath := filepath.Join(dir, "dump.sql")
	if err := os.WriteFile(sqlPath, []byte("INSERT INTO users (name) VALUES ('Ann');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(logx.New(io.Discard), root); err != nil {
		t.Fatal(err)
	}
	csvPath := filepath.Join(dir, "users.csv")
	before, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(sqlPath, []byte("INSERT INTO users (name) VALUES ('Bob');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	staleTemp := filepath.Join(dir, ".2csv-stale.tmp")
	if err := os.WriteFile(staleTemp, []byte("do not enter completed folder"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) || strings.Contains(string(after), "Bob") {
		t.Fatalf("готовая папка обработана повторно: %q", after)
	}
	if second.CSV != 0 || second.InsertOK != 0 || len(second.SuccessTops) != 0 {
		t.Fatalf("второй запуск: %+v", second)
	}
	raw, err := os.ReadFile(converted.Path(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "Alpha\n" {
		t.Fatalf("имя не должно дублироваться: %q", raw)
	}
	if _, err := os.Stat(staleTemp); err != nil {
		t.Fatalf("готовая папка была затронута при очистке temp: %v", err)
	}
}

func TestSecondRunSkipsExcelInCompletedTopFolder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bookPath := filepath.Join(dir, "book.xlsx")
	if err := xlsconv.WriteXLSX(bookPath, []xlsconv.Sheet{{
		Name: "Data",
		Rows: [][]string{{"header"}, {"first"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(logx.New(io.Discard), root); err != nil {
		t.Fatal(err)
	}
	csvPath := filepath.Join(dir, "book_Data.csv")
	before, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := xlsconv.WriteXLSX(bookPath, []xlsconv.Sheet{{
		Name: "Data",
		Rows: [][]string{{"header"}, {"second"}},
	}}); err != nil {
		t.Fatal(err)
	}
	second, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || strings.Contains(string(after), "second") {
		t.Fatalf("Excel готовой папки обработан повторно: %q", after)
	}
	if second.CSV != 0 || len(second.Scan.Files) != 0 {
		t.Fatalf("второй запуск: %+v", second)
	}
}

func TestCompletedFolderMatchIsCaseInsensitiveOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows filesystem semantics")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dump.sql"), []byte(
		"INSERT INTO users (email) VALUES ('must-not-run@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(converted.Path(root), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	if res.CSV != 0 || len(res.Scan.Files) != 0 {
		t.Fatalf("готовая папка обработана повторно: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "users.csv")); !os.IsNotExist(err) {
		t.Fatalf("users.csv не должен создаваться, err=%v", err)
	}
	raw, err := os.ReadFile(converted.Path(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "alpha\n" {
		t.Fatalf("имя не должно дублироваться другим регистром: %q", raw)
	}
}

func TestUnfinishedFolderOverwritesStaleCSV(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Interrupted")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	csvPath := filepath.Join(dir, "users.csv")
	if err := os.WriteFile(csvPath, []byte("STALE PARTIAL DATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	staleTemp := filepath.Join(dir, ".2csv-interrupted.tmp")
	if err := os.WriteFile(staleTemp, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dump.sql"), []byte("INSERT INTO users (email) VALUES ('new@example.com');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(logx.New(io.Discard), root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "\"email\"\n\"new@example.com\"\n"
	if string(got) != want {
		t.Fatalf("CSV не перезаписан после незавершённого запуска:\n got %q\nwant %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "users(1).csv")); !os.IsNotExist(err) {
		t.Fatalf("users(1).csv не должен создаваться, err=%v", err)
	}
	if _, err := os.Stat(staleTemp); !os.IsNotExist(err) {
		t.Fatalf("temp прошлого запуска не удалён, err=%v", err)
	}
}

func TestFolderWithNoCSVIsNotInConverted(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Filtered")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dump.sql"), []byte("INSERT INTO settings VALUES (1, 'dark');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	res, err := Run(logx.New(&buf), root)
	if err != nil {
		t.Fatal(err)
	}
	if res.CSV != 0 || res.InsertOK != 0 || res.InsertSkip != 1 {
		t.Fatalf("результат: %+v", res)
	}
	if len(res.SuccessTops) != 0 {
		t.Fatalf("папка без CSV не завершена: %v", res.SuccessTops)
	}
	if _, err := os.Stat(converted.Path(root)); !os.IsNotExist(err) {
		t.Fatalf("converted.txt не должен создаваться, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.csv")); !os.IsNotExist(err) {
		t.Fatalf("settings.csv не должен создаваться, err=%v", err)
	}
	log := buf.String()
	if !strings.Contains(log, "error: папка Filtered: не создано ни одного CSV: всё отсеял фильтр") {
		t.Fatalf("нужна причина 0 CSV:\n%s", log)
	}
	if strings.Contains(log, "dark") || strings.Contains(log, "VALUES") {
		t.Fatal("тело VALUES не должно попадать в лог")
	}
}

func TestEmptyAndUnsupportedOnlyFoldersAreNotCompleted(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "Empty")
	unsupported := filepath.Join(root, "Unsupported")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(unsupported, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unsupported, "notes.txt"), []byte("ignore"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	first, err := Run(logx.New(&buf), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.SuccessTops) != 0 {
		t.Fatalf("пустые папки не завершены: %v", first.SuccessTops)
	}
	if _, err := os.Stat(converted.Path(root)); !os.IsNotExist(err) {
		t.Fatalf("converted.txt не должен создаваться, err=%v", err)
	}
	log := buf.String()
	if !strings.Contains(log, "папка Empty: нет файлов") || !strings.Contains(log, "папка Unsupported: нет файлов") {
		t.Fatalf("нужна строка «нет файлов»:\n%s", log)
	}

	if err := os.WriteFile(filepath.Join(empty, "later.sql"), []byte(
		"INSERT INTO users (email) VALUES ('later@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	if second.CSV != 1 {
		t.Fatalf("пустая папка без converted.txt должна обработаться: %+v", second)
	}
	if _, err := os.Stat(filepath.Join(empty, "users.csv")); err != nil {
		t.Fatalf("users.csv должен появиться: %v", err)
	}
}

func TestRootFileIsProcessedOnEveryRun(t *testing.T) {
	root := t.TempDir()
	sqlPath := filepath.Join(root, "dump.sql")
	if err := os.WriteFile(sqlPath, []byte("INSERT INTO users (name) VALUES ('First');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(logx.New(io.Discard), root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sqlPath, []byte("INSERT INTO users (name) VALUES ('Second');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	if second.InsertOK != 1 || second.CSV != 1 {
		t.Fatalf("второй запуск: %+v", second)
	}
	raw, err := os.ReadFile(filepath.Join(root, "users.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Second") || strings.Contains(string(raw), "First") {
		t.Fatalf("корневой CSV не перезаписан: %q", raw)
	}
	if _, err := os.Stat(converted.Path(root)); !os.IsNotExist(err) {
		t.Fatalf("корневой файл не должен попадать в converted.txt, err=%v", err)
	}
}

func TestTopFolderNamedLikeRootStatusIsTrackedSeparately(t *testing.T) {
	root := t.TempDir()
	top := filepath.Join(root, "корень")
	if err := os.MkdirAll(top, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "root.sql"), []byte(
		"INSERT INTO root_users (email) VALUES ('root@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(top, "nested.sql"), []byte(
		"INSERT INTO nested_users (email) VALUES ('nested@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	if res.CSV != 2 || strings.Join(res.SuccessTops, ",") != "корень" {
		t.Fatalf("результат: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "root_users.csv")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(top, "nested_users.csv")); err != nil {
		t.Fatal(err)
	}
	done, err := converted.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := done["корень"]; !ok {
		t.Fatalf("верхняя папка не записана: %v", done)
	}
}

func TestRunRootOnlyDoesNotCreateConverted(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "only.sql"), []byte("CREATE TABLE t (id INT);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(converted.Path(root)); !os.IsNotExist(err) {
		t.Fatalf("для файлов прямо в корне converted.txt не создаётся, err=%v", err)
	}
	if len(res.SuccessTops) != 0 {
		t.Fatalf("tops=%v", res.SuccessTops)
	}
}

func TestBrokenSQLDoesNotStopNextFile(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "A")
	b := filepath.Join(root, "B")
	if err := os.MkdirAll(a, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(b, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a, "broken.sql"), []byte("INSERT INTO t (email) VALUES (1, 'нет конца"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "ok.sql"), []byte("INSERT INTO ok (email, phone) VALUES (2, '555');\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	res, err := Run(logx.New(&buf), root)
	if err != nil {
		t.Fatalf("процесс не должен падать: %v", err)
	}
	if _, err := os.Stat(filepath.Join(b, "ok.csv")); err != nil {
		t.Fatalf("второй файл должен быть обработан: %v\nлог:\n%s", err, buf.String())
	}
	if _, err := os.Stat(filepath.Join(a, "t.csv")); err == nil {
		t.Fatal("битый INSERT не должен оставлять CSV")
	}
	if res.InsertOK != 1 || res.InsertSkip < 1 {
		t.Fatalf("ok=%d skip=%d", res.InsertOK, res.InsertSkip)
	}
	if strings.Join(res.SuccessTops, ",") != "B" {
		t.Fatalf("tops=%v", res.SuccessTops)
	}
	log := buf.String()
	if strings.Contains(log, "нет конца") {
		t.Fatal("в лог нельзя писать содержимое VALUES")
	}
	if strings.Count(log, "папка обработана: B") != 1 {
		t.Fatalf("ожидалось завершение B:\n%s", log)
	}
	if strings.Contains(log, "папка обработана: A") {
		t.Fatalf("A без CSV не обработана:\n%s", log)
	}
	if !strings.Contains(log, "error: папка A: не создано ни одного CSV:") {
		t.Fatalf("нужна причина 0 CSV у A:\n%s", log)
	}
	if strings.Contains(log, "воркеров:") || strings.Contains(log, "прогресс:") || strings.Contains(log, "сводка:") {
		t.Fatalf("служебный шум в логе:\n%s", log)
	}
}

func TestSecondInsertTooManyKeepsCSVAndLogsReason(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	copySQLFixture(t, "06_second_too_many.sql", filepath.Join(dir, "dump.sql"))

	var buf bytes.Buffer
	res, err := Run(logx.New(&buf), root)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	want := "\"id\",\"name\",\"email\"\n\"1\",\"ok\",\"a@example.test\"\n"
	if string(raw) != want {
		t.Fatalf("CSV первого INSERT испорчен: %q", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "t(1).csv")); !os.IsNotExist(err) {
		t.Fatalf("t(1).csv не должен создаваться, err=%v", err)
	}
	if strings.Join(res.SuccessTops, ",") != "Alpha" {
		t.Fatalf("папка с CSV должна быть в списке: %v", res.SuccessTops)
	}
	log := buf.String()
	if !strings.Contains(log, "таблица t") || !strings.Contains(log, "значений больше, чем колонок") {
		t.Fatalf("нужны таблица и причина:\n%s", log)
	}
	if strings.Contains(log, "VALUES") || strings.Contains(log, "'x'") {
		t.Fatal("тело VALUES не должно попадать в лог")
	}
	if strings.Count(log, "папка обработана: Alpha") != 1 {
		t.Fatalf("папка с CSV обработана:\n%s", log)
	}
}

func TestPoolSizeBounds(t *testing.T) {
	for _, procs := range []int{1, 4, maxWorkers, 64} {
		prev := runtime.GOMAXPROCS(procs)
		got := poolSize()
		runtime.GOMAXPROCS(prev)
		if want := min(procs, maxWorkers); got != want {
			t.Fatalf("GOMAXPROCS=%d: воркеров %d, ожидалось %d", procs, got, want)
		}
	}
}

func TestBlockedScanPreventsFolderCompletion(t *testing.T) {
	root := t.TempDir()
	file := scan.SQLFile{Path: filepath.Join(root, "Alpha", "ok.sql"), TopFolder: "Alpha"}
	var buf bytes.Buffer
	acc := newAccumulator(
		logx.New(&buf),
		root,
		[]scan.SQLFile{file},
		map[string]struct{}{"Alpha": {}},
	)
	acc.start(file)
	acc.add(file, fileOutcome{created: 1, csv: 1})

	if _, err := os.Stat(converted.Path(root)); !os.IsNotExist(err) {
		t.Fatalf("заблокированную папку нельзя записывать в converted.txt, err=%v", err)
	}
	if strings.Contains(buf.String(), "папка обработана: Alpha") {
		t.Fatalf("нельзя объявлять папку полностью завершённой:\n%s", buf.String())
	}
	_, _, _, _, tops := acc.snapshot()
	if len(tops) != 0 {
		t.Fatalf("завершённые папки: %v", tops)
	}
}

func TestAccumulatorCountsCriticalConversionFailure(t *testing.T) {
	root := t.TempDir()
	file := scan.SQLFile{Path: filepath.Join(root, "Alpha", "bad.sql"), TopFolder: "Alpha"}
	acc := newAccumulator(logx.New(io.Discard), root, []scan.SQLFile{file}, nil)
	acc.start(file)
	acc.add(file, fileOutcome{skipped: 1, failed: true})
	_, skipped, _, filesFail, tops := acc.snapshot()
	if skipped != 1 || filesFail != 1 {
		t.Fatalf("skipped=%d filesFail=%d", skipped, filesFail)
	}
	if _, ok := tops["Alpha"]; ok {
		t.Fatalf("папка без CSV не должна попадать в converted.txt: %v", tops)
	}
}

func TestFolderStatusOncePerTopFolder(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(alpha, 0o755); err != nil {
		t.Fatal(err)
	}
	sql := "INSERT INTO t (email, phone) VALUES ('a@example.test', '555');\n"
	if err := os.WriteFile(filepath.Join(alpha, "a.sql"), []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alpha, "b.sql"), []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if _, err := Run(logx.New(&buf), root); err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	if strings.Count(log, "папка в обработке: Alpha (0 с)") != 1 {
		t.Fatalf("статус должен висеть один раз:\n%s", log)
	}
	if strings.Count(log, "папка обработана: Alpha") != 1 {
		t.Fatalf("завершение один раз:\n%s", log)
	}
	if strings.Contains(log, "записан") || strings.Contains(log, "дописаны") || strings.Contains(log, "INSERT успешно") {
		t.Fatalf("каждое действие не должно логироваться:\n%s", log)
	}
}

func TestFolderStatusesNeverCombineDifferentTopFolders(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Alpha", "Beta"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "dump.sql"), []byte(
			"INSERT INTO users (email) VALUES ('a@example.test');\n",
		), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var buf bytes.Buffer
	if _, err := Run(logx.New(&buf), root); err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	for _, name := range []string{"Alpha", "Beta"} {
		if !strings.Contains(log, name+" (") {
			t.Fatalf("в списке активных нет %s:\n%s", name, log)
		}
		if strings.Count(log, "папка обработана: "+name) != 1 {
			t.Fatalf("завершение %s один раз:\n%s", name, log)
		}
	}
}

func TestHangLineHasNameAndChangingSeconds(t *testing.T) {
	var buf bytes.Buffer
	log := logx.New(&buf)
	file := scan.SQLFile{Path: "a.sql", TopFolder: "Alpha"}
	acc := newAccumulator(log, t.TempDir(), []scan.SQLFile{file}, nil)
	acc.start(file)
	got := buf.String()
	if !strings.Contains(got, "папка в обработке: Alpha (0 с)") {
		t.Fatalf("старт: %q", got)
	}

	acc.mu.Lock()
	af := acc.active[folderKey(file)]
	af.start = time.Now().Add(-2 * time.Second)
	acc.active[folderKey(file)] = af
	acc.mu.Unlock()
	acc.tickHang()
	got = buf.String()
	if !strings.Contains(got, "папка в обработке: Alpha (2 с)") {
		t.Fatalf("тик: %q", got)
	}

	acc.mu.Lock()
	delete(acc.active, "Alpha")
	acc.refreshHang()
	acc.mu.Unlock()
	log.Linef("after")
	got = buf.String()
	tail := got[strings.LastIndex(got, "after"):]
	if strings.Contains(tail, "папка в обработке") {
		t.Fatalf("Hang(\"\") должен снять строку: %q", got)
	}
}

func TestHangListsEachActiveTopWithOwnSeconds(t *testing.T) {
	var buf bytes.Buffer
	log := logx.New(&buf)
	alpha := scan.SQLFile{Path: "a.sql", TopFolder: "Alpha"}
	beta := scan.SQLFile{Path: "b.sql", TopFolder: "Beta"}
	acc := newAccumulator(log, t.TempDir(), []scan.SQLFile{alpha, beta}, nil)
	acc.start(beta)
	acc.start(alpha)
	acc.mu.Lock()
	aa := acc.active[folderKey(alpha)]
	aa.start = time.Now().Add(-3 * time.Second)
	acc.active[folderKey(alpha)] = aa
	bb := acc.active[folderKey(beta)]
	bb.start = time.Now().Add(-1 * time.Second)
	acc.active[folderKey(beta)] = bb
	acc.refreshHang()
	acc.mu.Unlock()
	got := buf.String()
	if !strings.Contains(got, "папка в обработке: Alpha (3 с), Beta (1 с)") {
		t.Fatalf("список: %q", got)
	}
}

func TestTwoTopsSeveralDirsKeepSeparateCSV(t *testing.T) {
	root := t.TempDir()
	for _, top := range []string{"Alpha", "Beta"} {
		for _, sub := range []string{"one", "two"} {
			dir := filepath.Join(root, top, sub)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			body := "INSERT INTO users (email) VALUES ('" + top + "-" + sub + "@example.test');\n"
			if err := os.WriteFile(filepath.Join(dir, "a.sql"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	res, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	if res.CSV != 4 || res.InsertOK != 4 {
		t.Fatalf("результат: %+v", res)
	}
	for _, top := range []string{"Alpha", "Beta"} {
		for _, sub := range []string{"one", "two"} {
			raw, err := os.ReadFile(filepath.Join(root, top, sub, "users.csv"))
			if err != nil {
				t.Fatal(err)
			}
			want := top + "-" + sub + "@example.test"
			if !strings.Contains(string(raw), want) {
				t.Fatalf("%s/%s: %q", top, sub, raw)
			}
		}
	}
	if strings.Join(res.SuccessTops, ",") != "Alpha,Beta" {
		t.Fatalf("папки: %v", res.SuccessTops)
	}
}

func TestSecondTopStartsBeforeFirstFinishes(t *testing.T) {
	if poolSize() < 2 {
		t.Skip("при одном воркере вторую папку нечем открыть")
	}
	root := t.TempDir()
	for _, top := range []string{"Alpha", "Beta"} {
		dir := filepath.Join(root, top)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "INSERT INTO users (email) VALUES ('" + top + "@example.test');\n"
		if err := os.WriteFile(filepath.Join(dir, "a.sql"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var alphaState atomic.Int32
	var betaWhileHeld atomic.Bool
	releaseCh := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCh) }) }
	defer release()
	betaEntered := make(chan struct{})
	var betaOnce sync.Once

	testDirEnter = func(f scan.SQLFile) {
		switch f.TopFolder {
		case "Alpha":
			alphaState.Store(1)
			<-releaseCh
			alphaState.Store(2)
		case "Beta":
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if alphaState.Load() == 1 {
					betaWhileHeld.Store(true)
					break
				}
				runtime.Gosched()
			}
			betaOnce.Do(func() { close(betaEntered) })
		}
	}
	t.Cleanup(func() { testDirEnter = nil })

	errc := make(chan error, 1)
	go func() {
		_, err := Run(logx.New(io.Discard), root)
		errc <- err
	}()

	select {
	case <-betaEntered:
	case err := <-errc:
		t.Fatalf("прогон закончился до старта Beta: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Beta не стартовала, пока Alpha ещё идёт")
	}
	if !betaWhileHeld.Load() {
		t.Fatal("Beta стартовала не во время незавершённой Alpha")
	}
	release()
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	for _, top := range []string{"Alpha", "Beta"} {
		raw, err := os.ReadFile(filepath.Join(root, top, "users.csv"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), top+"@example.test") {
			t.Fatalf("%s: %q", top, raw)
		}
	}
}

func TestHangTickerStopsWhenTopFolderEnds(t *testing.T) {
	file := scan.SQLFile{Path: "a.sql", TopFolder: "Alpha"}
	acc := newAccumulator(logx.New(io.Discard), t.TempDir(), []scan.SQLFile{file}, nil)
	acc.start(file)
	stop := acc.startHangTicker()
	stop()
	acc.mu.Lock()
	delete(acc.active, "Alpha")
	acc.refreshHang()
	acc.mu.Unlock()
}

func TestGroupByDirSerializesSameFolder(t *testing.T) {
	d1 := filepath.Join("Alpha", "a.sql")
	d2 := filepath.Join("Alpha", "z.sql")
	d3 := filepath.Join("Beta", "b.sql")
	groups := groupByDir([]scan.SQLFile{
		{Path: d2},
		{Path: d3},
		{Path: d1},
	})
	if len(groups) != 2 {
		t.Fatalf("групп: %d", len(groups))
	}
	var alpha, beta []scan.SQLFile
	for _, g := range groups {
		switch filepath.Dir(g[0].Path) {
		case filepath.Dir(d1):
			alpha = g
		case filepath.Dir(d3):
			beta = g
		}
	}
	if len(alpha) != 2 || alpha[0].Path != d1 || alpha[1].Path != d2 {
		t.Fatalf("Alpha должна идти по возрастанию пути: %v", alpha)
	}
	if len(beta) != 1 || beta[0].Path != d3 {
		t.Fatalf("Beta: %v", beta)
	}
}

func TestGroupByDirKeepsSQLStreamBeforeExcel(t *testing.T) {
	dir := filepath.Join("Alpha")
	groups := groupByDir([]scan.SQLFile{
		{Path: filepath.Join(dir, "m.xlsx"), Kind: scan.KindXLSX},
		{Path: filepath.Join(dir, "z.sql"), Kind: scan.KindSQL},
		{Path: filepath.Join(dir, "c.csv"), Kind: scan.KindCSV},
		{Path: filepath.Join(dir, "a.sql"), Kind: scan.KindSQL},
		{Path: filepath.Join(dir, "a.csv"), Kind: scan.KindCSV},
		{Path: filepath.Join(dir, "b.xls"), Kind: scan.KindXLS},
	})
	if len(groups) != 1 {
		t.Fatalf("групп=%d", len(groups))
	}
	got := groups[0]
	want := []string{"a.sql", "z.sql", "b.xls", "m.xlsx", "a.csv", "c.csv"}
	if len(got) != len(want) {
		t.Fatalf("файлы=%v", got)
	}
	for i, name := range want {
		if filepath.Base(got[i].Path) != name {
			t.Fatalf("позиция %d: got=%s want=%s", i, got[i].Path, name)
		}
	}
}

func TestMultiFileTreeFixturesThroughApp(t *testing.T) {
	root := t.TempDir()
	same := filepath.Join(root, "Same")
	copySQLFixture(t, filepath.Join("07_same_folder", "a.sql"), filepath.Join(same, "a.sql"))
	copySQLFixture(t, filepath.Join("07_same_folder", "z.sql"), filepath.Join(same, "z.sql"))
	for _, top := range []string{"Alpha", "Beta"} {
		copySQLFixture(
			t,
			filepath.Join("08_two_dirs", top, "dump.sql"),
			filepath.Join(root, top, "dump.sql"),
		)
	}

	res, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	if res.InsertOK != 4 || res.CSV != 3 {
		t.Fatalf("результат: %+v", res)
	}
	sameCSV, err := os.ReadFile(filepath.Join(same, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	wantSame := "\"id\",\"name\",\"email\"\n\"1\",\"from-a\",\"a@example.test\"\n\"2\",\"from-z\",\"555\"\n"
	if string(sameCSV) != wantSame {
		t.Fatalf("Same/t.csv:\n got %q\nwant %q", sameCSV, wantSame)
	}
	for _, top := range []string{"Alpha", "Beta"} {
		raw, err := os.ReadFile(filepath.Join(root, top, "t.csv"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "@example.test") {
			t.Fatalf("%s/t.csv=%q", top, raw)
		}
	}
	if strings.Join(res.SuccessTops, ",") != "Alpha,Beta,Same" {
		t.Fatalf("готовые папки: %v", res.SuccessTops)
	}
}

func TestHeaderFromFirstFileByPath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "D")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Пишем z.sql раньше a.sql, чтобы порядок создания на диске не совпал с путём.
	if err := os.WriteFile(filepath.Join(dir, "z.sql"), []byte("INSERT INTO t (x, email, phone) VALUES (2, 'later', '555');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.sql"), []byte("INSERT INTO t (id, name, email) VALUES (1, 'first', 'a@example.test');\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	res, err := Run(logx.New(&buf), root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.InsertOK != 2 || res.CSV != 1 {
		t.Fatalf("ok=%d csv=%d log:\n%s", res.InsertOK, res.CSV, buf.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	want := "\"id\",\"name\",\"email\"\n\"1\",\"first\",\"a@example.test\"\n\"2\",\"later\",\"555\"\n"
	if string(got) != want {
		t.Fatalf("заголовок должен быть от a.sql, не от того кто добежал:\n got %q\nwant %q\nлог:\n%s", got, want, buf.String())
	}
}

func TestRunExcelTreeConvertedTxt(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "Alpha")
	beta := filepath.Join(root, "Beta")
	if err := os.MkdirAll(alpha, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(beta, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := xlsconv.WriteXLSX(filepath.Join(alpha, "a.xlsx"), []xlsconv.Sheet{
		{Name: "One", Rows: [][]string{{"h1"}, {"v1"}}},
		{Name: "Two", Rows: [][]string{{"h2"}, {"v2"}}},
	}); err != nil {
		t.Fatal(err)
	}
	six := make([]xlsconv.Sheet, 6)
	for i := range six {
		six[i] = xlsconv.Sheet{Name: string(rune('A' + i)), Rows: [][]string{{"x"}}}
	}
	if err := xlsconv.WriteXLS(filepath.Join(beta, "six.xls"), six); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alpha, "dump.sql"), []byte("INSERT INTO t (email, phone) VALUES (1, '555');\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	res, err := Run(logx.New(&buf), root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	log := buf.String()

	for _, p := range []string{
		filepath.Join(alpha, "a_One.csv"),
		filepath.Join(alpha, "a_Two.csv"),
		filepath.Join(alpha, "t.csv"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("нет %s: %v\nлог:\n%s", p, err, log)
		}
	}
	entries, err := os.ReadDir(beta)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.EqualFold(filepath.Ext(e.Name()), ".csv") {
			t.Fatalf("Beta не должна иметь CSV при 6 листах: %s", e.Name())
		}
	}

	raw, err := os.ReadFile(converted.Path(root))
	if err != nil {
		t.Fatal(err)
	}
	done, err := converted.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 1 {
		t.Fatalf("converted.txt=%q, ожидалась только Alpha", raw)
	}
	if _, ok := done["Alpha"]; !ok {
		t.Fatalf("converted.txt=%q, нет Alpha", raw)
	}
	if _, ok := done["Beta"]; ok {
		t.Fatalf("Beta без CSV не должна быть в converted.txt: %q", raw)
	}
	if strings.Join(res.SuccessTops, ",") != "Alpha" {
		t.Fatalf("tops=%v", res.SuccessTops)
	}
	if strings.Contains(log, "six.xls") {
		t.Fatalf("пропуск >5 листов не логировать по файлу:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(beta, "six.xls")); err != nil {
		t.Fatalf("книга >5 листов должна остаться: %v", err)
	}
	for _, name := range []string{"a.xlsx", "dump.sql"} {
		if _, err := os.Stat(filepath.Join(alpha, name)); !os.IsNotExist(err) {
			t.Fatalf("исходник %s должен быть удалён, err=%v", name, err)
		}
	}
	if strings.Count(log, "папка обработана: Alpha") != 1 {
		t.Fatalf("завершение Alpha:\n%s", log)
	}
	if strings.Contains(log, "папка обработана: Beta") {
		t.Fatalf("Beta без CSV не обработана:\n%s", log)
	}
	if !strings.Contains(log, "error: папка Beta: не создано ни одного CSV: нечего конвертировать") {
		t.Fatalf("нужна причина 0 CSV у Beta:\n%s", log)
	}
}

func TestDeleteSourceOnlyAfterCSV(t *testing.T) {
	root := t.TempDir()
	okDir := filepath.Join(root, "Ok")
	piiDir := filepath.Join(root, "PII")
	colDir := filepath.Join(root, "Col")
	if err := os.MkdirAll(okDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(piiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(colDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(okDir, "a.sql"), []byte(
		"INSERT INTO users (email) VALUES ('a@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(okDir, "z.sql"), []byte(
		"INSERT INTO users (email) VALUES ('z@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := xlsconv.WriteXLSX(filepath.Join(okDir, "book.xlsx"), []xlsconv.Sheet{{
		Name: "Data",
		Rows: [][]string{{"email"}, {"a@example.test"}},
	}}); err != nil {
		t.Fatal(err)
	}
	copySQLFixture(t, "11_no_pii.sql", filepath.Join(piiDir, "11_no_pii.sql"))
	if err := os.WriteFile(filepath.Join(colDir, "one.sql"), []byte(
		"INSERT INTO t (email) VALUES ('a@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(okDir, "notes.csv")
	if err := os.WriteFile(kept, []byte("\"h\"\n\"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(logx.New(io.Discard), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(okDir, "users.csv")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(okDir, "book_Data.csv")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.sql", "z.sql", "book.xlsx"} {
		if _, err := os.Stat(filepath.Join(okDir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s должен быть удалён, err=%v", name, err)
		}
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("чужой csv удалять нельзя: %v", err)
	}
	for _, path := range []string{
		filepath.Join(piiDir, "11_no_pii.sql"),
		filepath.Join(colDir, "one.sql"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("файл без CSV должен остаться %s: %v", path, err)
		}
	}
}

func TestSQLExcelSQLSameTargetNeverMixesStreams(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.sql"), []byte(
		"INSERT INTO m_users (email) VALUES ('first@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := xlsconv.WriteXLSX(filepath.Join(dir, "m.xlsx"), []xlsconv.Sheet{{
		Name: "users",
		Rows: [][]string{{"excel_header"}, {"excel"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "z.sql"), []byte(
		"INSERT INTO m_users (email) VALUES ('second@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	// SQL закрывает ключ раньше Excel (§9). Лист Excel не затирает CSV этого
	// запуска: иначе оба .sql удалились бы без своих данных на диске.
	if res.InsertOK != 2 || res.CSV != 1 || res.FilesFail != 1 {
		t.Fatalf("результат: %+v", res)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "m_users.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "\"email\"\n\"first@example.test\"\n\"second@example.test\"\n" {
		t.Fatalf("SQL и Excel не должны смешиваться: %q", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "m_users(1).csv")); !os.IsNotExist(err) {
		t.Fatalf("индексный CSV не должен создаваться, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "m.xlsx")); err != nil {
		t.Fatalf("книга без своего CSV должна остаться: %v", err)
	}
}

func TestTechnicalIDsDoNotPassColumnThresholdThroughApp(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sql := `
INSERT INTO t (user_id, email) VALUES (1, 'skip@example.test');
INSERT INTO t (national_id, phone) VALUES ('X', '555');
INSERT INTO users (id, status) VALUES (2, 'ok');
`
	if err := os.WriteFile(filepath.Join(dir, "dump.sql"), []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	if res.InsertOK != 2 || res.InsertSkip != 1 || res.CSV != 2 {
		t.Fatalf("результат: %+v", res)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "\"national_id\",\"phone\"\n\"X\",\"555\"\n" {
		t.Fatalf("t.csv=%q", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "users.csv")); err != nil {
		t.Fatal(err)
	}
}

func TestUnicodePIIFlowsThroughAppToExactCSV(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Unicode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sql := `
INSERT INTO архив (имя, email) VALUES ('Анна "Тест"', 'anna@example.test');
INSERT INTO orders (id, status) VALUES (2, 'skip');
`
	if err := os.WriteFile(filepath.Join(dir, "dump.sql"), []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	if res.InsertOK != 1 || res.InsertSkip != 1 || res.CSV != 1 {
		t.Fatalf("результат: %+v", res)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "архив.csv"))
	if err != nil {
		t.Fatal(err)
	}
	want := "\"имя\",\"email\"\n\"Анна \"\"Тест\"\"\",\"anna@example.test\"\n"
	if string(raw) != want {
		t.Fatalf("CSV:\n got %q\nwant %q", raw, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "orders.csv")); !os.IsNotExist(err) {
		t.Fatalf("orders.csv не должен создаваться, err=%v", err)
	}
}

func TestRunBrokenExcelLogsAndContinues(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "A")
	b := filepath.Join(root, "B")
	if err := os.MkdirAll(a, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(b, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a, "bad.xlsx"), []byte("not excel"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "ok.sql"), []byte("INSERT INTO ok (email, phone) VALUES (1, '555');\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	res, err := Run(logx.New(&buf), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b, "ok.csv")); err != nil {
		t.Fatalf("SQL после битого Excel: %v", err)
	}
	if strings.Join(res.SuccessTops, ",") != "B" {
		t.Fatalf("tops=%v", res.SuccessTops)
	}
	log := buf.String()
	if !strings.Contains(log, "не удалось открыть") {
		t.Fatalf("ожидалась критическая ошибка открытия xlsx:\n%s", log)
	}
	if !strings.Contains(log, "error: папка A: не создано ни одного CSV:") {
		t.Fatalf("нужна причина 0 CSV у A:\n%s", log)
	}
}

func TestTabularSQLWithInsertAndExcelSameRun(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	copySQLFixture(t, "17_table_csv.sql", filepath.Join(dir, "GameSalad.sql"))
	if err := os.WriteFile(filepath.Join(dir, "dump.sql"), []byte(
		"INSERT INTO t (email, phone) VALUES (1, '555');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := xlsconv.WriteXLSX(filepath.Join(dir, "a.xlsx"), []xlsconv.Sheet{{
		Name: "One",
		Rows: [][]string{{"h1"}, {"v1"}},
	}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "GameSalad.sql"))
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	res, err := Run(logx.New(&buf), root)
	if err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	for _, p := range []string{
		filepath.Join(dir, "GameSalad.csv"),
		filepath.Join(dir, "t.csv"),
		filepath.Join(dir, "a_One.csv"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("нет %s: %v\nлог:\n%s", p, err, log)
		}
	}
	got, err := os.ReadFile(filepath.Join(dir, "GameSalad.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "\"email\",\"phone\"\n\"a@example.test\",\"555\"\n\"b@example.test\",\"777\"\n" {
		t.Fatalf("табличный CSV: %q", got)
	}
	for _, name := range []string{"GameSalad.sql", "dump.sql", "a.xlsx"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("исходник %s должен быть удалён, err=%v", name, err)
		}
	}
	if string(before) == "" {
		t.Fatal("фикстура табличного .sql пустая")
	}
	if strings.Count(log, "не INSERT, а таблица") != 1 {
		t.Fatalf("warn табличного .sql:\n%s", log)
	}
	if res.CSV != 3 || strings.Join(res.SuccessTops, ",") != "Alpha" {
		t.Fatalf("результат: %+v", res)
	}
}

func TestForeignCSVSplitLeavesSmallFileAndMarksFolder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.csv")
	writeRepeatedRows(t, big, "\"h\"\n", "\"a\"\n", csvout.SplitThreshold+1)
	small := filepath.Join(dir, "small.csv")
	const smallBody = "\"h\"\n\"b\"\n"
	if err := os.WriteFile(small, []byte(smallBody), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	res, err := Run(logx.New(&buf), root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.SuccessTops, ",") != "Alpha" {
		t.Fatalf("вершины: %+v\n%s", res.SuccessTops, buf.String())
	}
	raw, err := os.ReadFile(converted.Path(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "Alpha\n" {
		t.Fatalf("converted.txt: %q", raw)
	}
	for _, name := range []string{"big.csv", "big_2.csv", "big_3.csv"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("нет %s: %v", name, err)
		}
	}
	got, err := os.ReadFile(small)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != smallBody {
		t.Fatalf("маленький CSV изменён: %q", got)
	}
	if strings.Contains(buf.String(), "не удалось нарезать") {
		t.Fatalf("успешная нарезка попала в лог:\n%s", buf.String())
	}
}

func TestFolderWithOnlySmallCSVHasNoFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("\"h\"\n\"a\"\n")
	path := filepath.Join(dir, "small.csv")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	res, err := Run(logx.New(&buf), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SuccessTops) != 0 {
		t.Fatalf("папка попала в список: %v", res.SuccessTops)
	}
	if _, err := os.Stat(converted.Path(root)); !os.IsNotExist(err) {
		t.Fatalf("converted.txt: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("CSV изменён: %q", got)
	}
	if !strings.Contains(buf.String(), "папка Alpha: нет файлов") {
		t.Fatalf("лог:\n%s", buf.String())
	}
}

func TestSQLKeyIsMergedBeforeSplit(t *testing.T) {
	restore := csvout.SetSplitLimits(2, 2)
	defer restore()

	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sql := "" +
		"INSERT INTO users (email) VALUES ('a'),('a'),('a');\n" +
		"INSERT INTO users (email) VALUES ('b');\n"
	if err := os.WriteFile(filepath.Join(dir, "a.sql"), []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(logx.New(io.Discard), root); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(dir, "users.csv"), "\"email\"\n\"a\"\n\"a\"\n")
	assertFile(t, filepath.Join(dir, "users_2.csv"), "\"email\"\n\"a\"\n\"b\"\n")
}

// Прошлый запуск оборвался после публикации части: users_2.csv лежит,
// папки нет в converted.txt. Повторная нарезка занимает тот же слот, а не _3.
func TestRerunAfterInterruptedSplitDoesNotDuplicateParts(t *testing.T) {
	restore := csvout.SetSplitLimits(2, 2)
	defer restore()

	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "users_2.csv"), []byte("\"email\"\n\"b\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sql := "INSERT INTO users (email) VALUES ('a'),('a'),('b');\n"
	if err := os.WriteFile(filepath.Join(dir, "a.sql"), []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(logx.New(io.Discard), root); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(dir, "users.csv"), "\"email\"\n\"a\"\n\"a\"\n")
	assertFile(t, filepath.Join(dir, "users_2.csv"), "\"email\"\n\"b\"\n")
	if _, err := os.Stat(filepath.Join(dir, "users_3.csv")); !os.IsNotExist(err) {
		t.Fatalf("дубль части users_3.csv: %v", err)
	}
}

func TestHeaderlessSQLSplitDoesNotInventHeader(t *testing.T) {
	restore := csvout.SetSplitLimits(2, 2)
	defer restore()

	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sql := "INSERT INTO users VALUES ('a'),('a'),('b');\n"
	if err := os.WriteFile(filepath.Join(dir, "a.sql"), []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(logx.New(io.Discard), root); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(dir, "users.csv"), "\"a\"\n\"a\"\n")
	assertFile(t, filepath.Join(dir, "users_2.csv"), "\"b\"\n")
}

func TestCompletedTopIsNotSplit(t *testing.T) {
	restore := csvout.SetSplitLimits(1, 1)
	defer restore()

	root := t.TempDir()
	alpha := filepath.Join(root, "Alpha")
	beta := filepath.Join(root, "Beta")
	if err := os.MkdirAll(alpha, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(beta, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("\"h\"\n\"a\"\n\"b\"\n")
	if err := os.WriteFile(filepath.Join(alpha, "big.csv"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(converted.Path(root), []byte("Alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beta, "dump.sql"), []byte(
		"INSERT INTO users (email) VALUES ('a@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if _, err := Run(logx.New(&buf), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(alpha, "big_2.csv")); !os.IsNotExist(err) {
		t.Fatalf("завершённая папка нарезана: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(alpha, "big.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("CSV завершённой папки изменён: %q", got)
	}
	if strings.Contains(buf.String(), "папка Alpha:") || strings.Contains(buf.String(), "папка обработана: Alpha") {
		t.Fatalf("Alpha снова в логе:\n%s", buf.String())
	}
}

func TestQuotedNewlineAndSpecialCellsConvertAndSplit(t *testing.T) {
	restore := csvout.SetSplitLimits(2, 2)
	defer restore()

	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sql := "" +
		"INSERT INTO users (email, note) VALUES ('a@example.test', 'line1\nline2');\n" +
		"INSERT INTO users (email, note) VALUES ('b@example.test', 'say \"hi\"');\n" +
		"INSERT INTO users (email, note) VALUES ('c@example.test', 'comma, here');\n"
	if err := os.WriteFile(filepath.Join(dir, "a.sql"), []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(logx.New(io.Discard), root); err != nil {
		t.Fatal(err)
	}
	want1 := "\"email\",\"note\"\n\"a@example.test\",\"line1\nline2\"\n\"b@example.test\",\"say \"\"hi\"\"\"\n"
	want2 := "\"email\",\"note\"\n\"c@example.test\",\"comma, here\"\n"
	assertFile(t, filepath.Join(dir, "users.csv"), want1)
	assertFile(t, filepath.Join(dir, "users_2.csv"), want2)
	if _, err := os.Stat(filepath.Join(dir, "a.sql")); !os.IsNotExist(err) {
		t.Fatalf("исходник должен быть удалён: %v", err)
	}
}

func TestTwoTablesStayCorrectWhenLaterFinishesFirst(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sql := "" +
		"INSERT INTO users (email) VALUES ('first@example.test');\n" +
		"INSERT INTO user_profiles (email) VALUES ('arch@example.test');\n" +
		"INSERT INTO users (email) VALUES ('second@example.test');\n"
	if err := os.WriteFile(filepath.Join(dir, "a.sql"), []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(logx.New(io.Discard), root); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(dir, "users.csv"), "\"email\"\n\"first@example.test\"\n\"second@example.test\"\n")
	assertFile(t, filepath.Join(dir, "user_profiles.csv"), "\"email\"\n\"arch@example.test\"\n")
}

func TestXLSDeletedAfterOneSheetCSV(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	book := filepath.Join(dir, "book.xls")
	if err := xlsconv.WriteXLS(book, []xlsconv.Sheet{{
		Name: "Data",
		Rows: [][]string{{"email"}, {"a@example.test"}},
	}}); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(logx.New(io.Discard), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "book_Data.csv")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(book); !os.IsNotExist(err) {
		t.Fatalf(".xls должен быть удалён: %v", err)
	}
}

// report.xls и report.xlsx претендуют на report_Sheet1.csv. Второй не затирает
// CSV первого: его лист — ошибка записи, а книга остаётся на диске.
func TestSameTargetNameDoesNotOverwriteCSVOfThisRun(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	xls := filepath.Join(dir, "report.xls")
	xlsx := filepath.Join(dir, "report.xlsx")
	if err := xlsconv.WriteXLS(xls, []xlsconv.Sheet{{
		Name: "Sheet1",
		Rows: [][]string{{"email"}, {"from-xls@example.test"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := xlsconv.WriteXLSX(xlsx, []xlsconv.Sheet{{
		Name: "Sheet1",
		Rows: [][]string{{"email"}, {"from-xlsx@example.test"}},
	}}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if _, err := Run(logx.New(&buf), root); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(dir, "report_Sheet1.csv"), "\"email\"\n\"from-xls@example.test\"\n")
	if _, err := os.Stat(xls); !os.IsNotExist(err) {
		t.Fatalf("report.xls дал CSV и должен быть удалён: %v", err)
	}
	if _, err := os.Stat(xlsx); err != nil {
		t.Fatalf("report.xlsx без своего CSV должен остаться: %v", err)
	}
	if !strings.Contains(buf.String(), "report.xlsx") {
		t.Fatalf("нужна ошибка столкновения имён:\n%s", buf.String())
	}
}

// Нарезка users.csv не занимает users_2.csv, который этот запуск записал
// для таблицы users_2: нарезка падает, оба CSV целы, исходник остаётся.
func TestSplitDoesNotOverwriteCSVOfThisRun(t *testing.T) {
	restore := csvout.SetSplitLimits(2, 2)
	defer restore()

	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sql := "" +
		"INSERT INTO users_2 (email) VALUES ('x');\n" +
		"INSERT INTO users (email) VALUES ('a'),('b'),('c');\n"
	src := filepath.Join(dir, "a.sql")
	if err := os.WriteFile(src, []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if _, err := Run(logx.New(&buf), root); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(dir, "users_2.csv"), "\"email\"\n\"x\"\n")
	assertFile(t, filepath.Join(dir, "users.csv"), "\"email\"\n\"a\"\n\"b\"\n\"c\"\n")
	if !strings.Contains(buf.String(), "не удалось нарезать") {
		t.Fatalf("нужна ошибка нарезки:\n%s", buf.String())
	}
}

// Исходник удаляется только при чистом успехе: INSERT, не попавший в CSV
// из-за ошибки, иначе пропал бы безвозвратно.
func TestPartialFailureKeepsSource(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "dump.sql")
	copySQLFixture(t, "06_second_too_many.sql", src)

	if _, err := Run(logx.New(io.Discard), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "t.csv")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("исходник с проваленным INSERT должен остаться: %v", err)
	}
}

func TestPIISkipKeepsSourceWhileNeighborIsDeleted(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.sql"), []byte(
		"INSERT INTO users (email) VALUES ('a@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	copySQLFixture(t, "11_no_pii.sql", filepath.Join(dir, "11_no_pii.sql"))

	if _, err := Run(logx.New(io.Discard), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ok.sql")); !os.IsNotExist(err) {
		t.Fatalf("успешный .sql должен быть удалён: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "11_no_pii.sql")); err != nil {
		t.Fatalf("PII-файл должен остаться: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "users.csv")); err != nil {
		t.Fatal(err)
	}
}

func TestSameTableTwoDirsIndependent(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "Alpha", "one")
	b := filepath.Join(root, "Alpha", "two")
	if err := os.MkdirAll(a, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(b, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a, "a.sql"), []byte(
		"INSERT INTO users (email) VALUES ('one@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "a.sql"), []byte(
		"INSERT INTO users (email) VALUES ('two@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(logx.New(io.Discard), root); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(a, "users.csv"), "\"email\"\n\"one@example.test\"\n")
	assertFile(t, filepath.Join(b, "users.csv"), "\"email\"\n\"two@example.test\"\n")
}

func TestRootSQLDeletedAndNotListed(t *testing.T) {
	root := t.TempDir()
	sqlPath := filepath.Join(root, "dump.sql")
	if err := os.WriteFile(sqlPath, []byte(
		"INSERT INTO users (email) VALUES ('root@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sqlPath); !os.IsNotExist(err) {
		t.Fatalf("корневой .sql должен быть удалён: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "users.csv")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(converted.Path(root)); !os.IsNotExist(err) {
		t.Fatalf("корень не пишется в converted.txt: %v", err)
	}
	if len(res.SuccessTops) != 0 {
		t.Fatalf("tops=%v", res.SuccessTops)
	}
}

func writeRepeatedRows(t *testing.T, path, header, line string, rows int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	if _, err := w.WriteString(header); err != nil {
		t.Fatal(err)
	}
	b := []byte(line)
	for i := 0; i < rows; i++ {
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s:\n got %q\nwant %q", path, got, want)
	}
}
