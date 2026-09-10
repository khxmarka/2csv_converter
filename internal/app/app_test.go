package app

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"sql2csv/internal/converted"
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
	for _, name := range []string{"OLD", "Alpha", "Beta", "Gamma"} {
		if _, ok := done[name]; !ok {
			t.Fatalf("в converted.txt нет %s: %q", name, raw)
		}
	}
	if len(done) != 4 {
		t.Fatalf("converted.txt содержит лишние имена: %q", raw)
	}
	if strings.Join(res.SuccessTops, ",") != "Alpha,Beta,Gamma" {
		t.Fatalf("верхние папки: %v", res.SuccessTops)
	}
	if res.CSV != 3 {
		t.Fatalf("CSV=%d, ожидалось 3 файла (orders отфильтрован, Beta.t склеен)", res.CSV)
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

func TestFolderWithNoCSVIsStillCompleted(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Filtered")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dump.sql"), []byte("INSERT INTO settings VALUES (1, 'dark');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	if res.CSV != 0 || res.InsertOK != 0 || res.InsertSkip != 1 {
		t.Fatalf("результат: %+v", res)
	}
	if strings.Join(res.SuccessTops, ",") != "Filtered" {
		t.Fatalf("завершённые папки: %v", res.SuccessTops)
	}
	raw, err := os.ReadFile(converted.Path(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "Filtered\n" {
		t.Fatalf("converted.txt=%q", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.csv")); !os.IsNotExist(err) {
		t.Fatalf("settings.csv не должен создаваться, err=%v", err)
	}
}

func TestEmptyAndUnsupportedOnlyFoldersAreCompleted(t *testing.T) {
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

	first, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(first.SuccessTops, ",") != "Empty,Unsupported" {
		t.Fatalf("завершённые папки: %v", first.SuccessTops)
	}
	done, err := converted.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 2 {
		t.Fatalf("converted.txt: %v", done)
	}

	// После фиксации папка считается завершённой целиком; новые файлы требуют
	// ручного сброса converted.txt и не должны открываться автоматически.
	if err := os.WriteFile(filepath.Join(empty, "later.sql"), []byte(
		"INSERT INTO users (email) VALUES ('later@example.test');\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	if second.CSV != 0 {
		t.Fatalf("готовая пустая папка была обработана повторно: %+v", second)
	}
	if _, err := os.Stat(filepath.Join(empty, "users.csv")); !os.IsNotExist(err) {
		t.Fatalf("users.csv не должен создаваться, err=%v", err)
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
	if strings.Join(res.SuccessTops, ",") != "A,B" {
		t.Fatalf("tops=%v", res.SuccessTops)
	}
	log := buf.String()
	if strings.Contains(log, "нет конца") {
		t.Fatal("в лог нельзя писать содержимое VALUES")
	}
	if strings.Count(log, "папка полностью завершена: A") != 1 || strings.Count(log, "папка полностью завершена: B") != 1 {
		t.Fatalf("ожидалось по одному завершению A и B:\n%s", log)
	}
	if strings.Contains(log, "воркеров:") || strings.Contains(log, "прогресс:") || strings.Contains(log, "сводка:") {
		t.Fatalf("служебный шум в логе:\n%s", log)
	}
}

func TestWorkerCount(t *testing.T) {
	if workerCount(0) != 0 {
		t.Fatalf("0 файлов → 0 воркеров")
	}
	if n := workerCount(1); n != 1 {
		t.Fatalf("1 файл → 1 воркер, получено %d", n)
	}
	if n := workerCount(1000); n < 1 || n > maxWorkers {
		t.Fatalf("потолок: %d", n)
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
	if strings.Contains(buf.String(), "папка полностью завершена: Alpha") {
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
	if _, ok := tops["Alpha"]; !ok {
		t.Fatalf("обработанная с ошибкой папка должна завершиться: %v", tops)
	}
}

func TestFolderStatusOncePerTopFolder(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "Alpha")
	if err := os.MkdirAll(alpha, 0o755); err != nil {
		t.Fatal(err)
	}
	sql := "INSERT INTO t (id) VALUES (1);\n"
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
	if strings.Count(log, "папка в обработке: Alpha") != 1 {
		t.Fatalf("статус должен висеть один раз:\n%s", log)
	}
	if strings.Count(log, "папка полностью завершена: Alpha") != 1 {
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
		if strings.Count(log, "папка в обработке: "+name) != 1 {
			t.Fatalf("статус %s должен появиться один раз:\n%s", name, log)
		}
	}
	if strings.Contains(log, "Alpha, Beta") || strings.Contains(log, "Beta, Alpha") {
		t.Fatalf("статусы разных верхних папок нельзя объединять:\n%s", log)
	}
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
		{Path: filepath.Join(dir, "a.sql"), Kind: scan.KindSQL},
		{Path: filepath.Join(dir, "b.xls"), Kind: scan.KindXLS},
	})
	if len(groups) != 1 {
		t.Fatalf("групп=%d", len(groups))
	}
	got := groups[0]
	want := []string{"a.sql", "z.sql", "b.xls", "m.xlsx"}
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
	if len(done) != 2 {
		t.Fatalf("converted.txt=%q, ожидались Alpha и Beta", raw)
	}
	for _, name := range []string{"Alpha", "Beta"} {
		if _, ok := done[name]; !ok {
			t.Fatalf("converted.txt=%q, нет %s", raw, name)
		}
	}
	if strings.Join(res.SuccessTops, ",") != "Alpha,Beta" {
		t.Fatalf("tops=%v", res.SuccessTops)
	}
	if strings.Contains(log, "six.xls") || strings.Contains(strings.ToLower(log), "лист") {
		t.Fatalf("пропуск >5 листов не логировать:\n%s", log)
	}
	if strings.Count(log, "папка полностью завершена: Alpha") != 1 || strings.Count(log, "папка полностью завершена: Beta") != 1 {
		t.Fatalf("завершение папок:\n%s", log)
	}
	if strings.Contains(log, "error:") {
		t.Fatalf("критических ошибок не ожидалось:\n%s", log)
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
	if res.InsertOK != 2 || res.CSV != 2 {
		t.Fatalf("результат: %+v", res)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "m_users.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "\"excel_header\"\n\"excel\"\n" {
		t.Fatalf("SQL и Excel не должны смешиваться: %q", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "m_users(1).csv")); !os.IsNotExist(err) {
		t.Fatalf("индексный CSV не должен создаваться, err=%v", err)
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
	if strings.Join(res.SuccessTops, ",") != "A,B" {
		t.Fatalf("tops=%v", res.SuccessTops)
	}
	log := buf.String()
	if !strings.Contains(log, "не удалось открыть") {
		t.Fatalf("ожидалась критическая ошибка открытия xlsx:\n%s", log)
	}
}
