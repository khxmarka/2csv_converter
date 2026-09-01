package app

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sql2csv/internal/converted"
	"sql2csv/internal/logx"
	"sql2csv/internal/scan"
	"sql2csv/internal/xlsconv"
)

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
INSERT INTO t (id) VALUES (1);
INSERT INTO t (id) VALUES (2);
`
	if err := os.WriteFile(filepath.Join(alpha, "a.sql"), []byte(twoTables), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beta, "b.sql"), []byte(sameTable), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "root.sql"), []byte("INSERT INTO root_table (id) VALUES (1);\n"), 0o644); err != nil {
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
		filepath.Join(alpha, "orders.csv"),
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
	if string(raw) != "Alpha\nBeta\n" {
		t.Fatalf("converted.txt=%q, ожидалось Alpha/Beta без Gamma и без вложенного пути", raw)
	}
	if strings.Join(res.SuccessTops, ",") != "Alpha,Beta" {
		t.Fatalf("верхние папки: %v", res.SuccessTops)
	}
	if res.CSV != 4 {
		t.Fatalf("CSV=%d, ожидалось 4 файла (два INSERT в Beta.t склеены)", res.CSV)
	}
	if res.InsertOK != 5 {
		t.Fatalf("INSERT=%d", res.InsertOK)
	}
}

func TestRunEmptyConvertedWhenNoSuccess(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "only.sql"), []byte("CREATE TABLE t (id INT);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Run(logx.New(io.Discard), root)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(converted.Path(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 0 {
		t.Fatalf("без успехов файл должен быть пустым: %q", raw)
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
	if err := os.WriteFile(filepath.Join(a, "broken.sql"), []byte("INSERT INTO t (id) VALUES (1, 'нет конца"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "ok.sql"), []byte("INSERT INTO ok (id) VALUES (2);\n"), 0o644); err != nil {
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

func TestHeaderFromFirstFileByPath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "D")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Пишем z.sql раньше a.sql, чтобы порядок создания на диске не совпал с путём.
	if err := os.WriteFile(filepath.Join(dir, "z.sql"), []byte("INSERT INTO t (x, y) VALUES (2, 'later');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.sql"), []byte("INSERT INTO t (id, name) VALUES (1, 'first');\n"), 0o644); err != nil {
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
	want := "\"id\",\"name\"\n\"1\",\"first\"\n\"2\",\"later\"\n"
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
	if err := os.WriteFile(filepath.Join(alpha, "dump.sql"), []byte("INSERT INTO t (id) VALUES (1);\n"), 0o644); err != nil {
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
	if string(raw) != "Alpha\n" {
		t.Fatalf("converted.txt=%q, ожидалось только Alpha", raw)
	}
	if strings.Join(res.SuccessTops, ",") != "Alpha" {
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
	if err := os.WriteFile(filepath.Join(b, "ok.sql"), []byte("INSERT INTO ok (id) VALUES (1);\n"), 0o644); err != nil {
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
}
