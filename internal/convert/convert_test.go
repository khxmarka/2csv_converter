package convert

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"sql2csv/internal/csvout"
	"sql2csv/internal/logx"
	"sql2csv/internal/scan"
)

func runFile(t *testing.T, dir, name, sql string) (Result, string) {
	t.Helper()
	return runFileReg(t, csvout.NewRegistry(), dir, name, sql)
}

func runFileReg(t *testing.T, reg *csvout.Registry, dir, name, sql string) (Result, string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	res := File(logx.New(&buf), reg, scan.SQLFile{Path: path, TopFolder: "Alpha"})
	if res.OpenErr != nil {
		t.Fatalf("OpenErr: %v", res.OpenErr)
	}
	return res, buf.String()
}

func testfilesSQL(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "testfiles", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestTwoInsertsDifferentTables(t *testing.T) {
	dir := t.TempDir()
	sql := `
INSERT INTO users (id, name) VALUES (1, 'Ann');
INSERT INTO orders (id) VALUES (9);
`
	res, _ := runFile(t, dir, "dump.sql", sql)
	if res.Created != 2 || res.CSV != 2 || res.Skipped != 0 {
		t.Fatalf("created=%d csv=%d skipped=%d", res.Created, res.CSV, res.Skipped)
	}
	users := filepath.Join(dir, "users.csv")
	orders := filepath.Join(dir, "orders.csv")
	if _, err := os.Stat(users); err != nil {
		t.Fatalf("нет users.csv: %v", err)
	}
	if _, err := os.Stat(orders); err != nil {
		t.Fatalf("нет orders.csv: %v", err)
	}
}

func TestTwoInsertsSameTableMerged(t *testing.T) {
	dir := t.TempDir()
	sql := `
INSERT INTO t (id, item) VALUES (1, 'first');
INSERT INTO t (id, item) VALUES (2, 'second');
`
	res, log := runFile(t, dir, "dump.sql", sql)
	if res.Created != 2 || res.CSV != 1 || res.Skipped != 0 {
		t.Fatalf("created=%d csv=%d skipped=%d", res.Created, res.CSV, res.Skipped)
	}
	path := filepath.Join(dir, "t.csv")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "t(1).csv")); err == nil {
		t.Fatal("два INSERT в t не должны давать t(1).csv")
	}
	got := string(raw)
	want := "\"id\",\"item\"\n\"1\",\"first\"\n\"2\",\"second\"\n"
	if got != want {
		t.Fatalf("CSV:\n got %q\nwant %q", got, want)
	}
	if strings.Count(got, `"id","item"`) != 1 {
		t.Fatalf("вторая строка заголовка:\n%s", got)
	}
	if strings.Contains(log, "VALUES") {
		t.Fatal("тело VALUES не должно попадать в лог")
	}
}

func TestTwoSQLFilesSameTableMerged(t *testing.T) {
	dir := t.TempDir()
	reg := csvout.NewRegistry()
	res1, _ := runFileReg(t, reg, dir, "a.sql", `INSERT INTO t (id) VALUES (1);`)
	res2, _ := runFileReg(t, reg, dir, "b.sql", `INSERT INTO t (id) VALUES (2);`)
	if res1.Created != 1 || res1.CSV != 1 || res2.Created != 1 || res2.CSV != 0 {
		t.Fatalf("created/csv %d/%d и %d/%d", res1.Created, res1.CSV, res2.Created, res2.CSV)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "t(1).csv")); err == nil {
		t.Fatal("два .sql в одной папке не должны плодить t(1).csv")
	}
	got := string(raw)
	want := "\"id\"\n\"1\"\n\"2\"\n"
	if got != want {
		t.Fatalf("CSV:\n got %q\nwant %q", got, want)
	}
}

func TestSameTableDifferentDirs(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "Alpha")
	beta := filepath.Join(root, "Beta")
	if err := os.MkdirAll(alpha, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(beta, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := csvout.NewRegistry()
	runFileReg(t, reg, alpha, "a.sql", `INSERT INTO t (id) VALUES (1);`)
	runFileReg(t, reg, beta, "b.sql", `INSERT INTO t (id) VALUES (2);`)
	rawA, err := os.ReadFile(filepath.Join(alpha, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	rawB, err := os.ReadFile(filepath.Join(beta, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(rawA) != "\"id\"\n\"1\"\n" || string(rawB) != "\"id\"\n\"2\"\n" {
		t.Fatalf("Alpha=%q Beta=%q", rawA, rawB)
	}
}

func TestPreexistingCSVThenMergeIntoIndexed(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "t.csv")
	if err := os.WriteFile(old, []byte("KEEP"), 0o644); err != nil {
		t.Fatal(err)
	}
	sql := `
INSERT INTO t (id) VALUES (1);
INSERT INTO t (id) VALUES (2);
`
	res, _ := runFile(t, dir, "dump.sql", sql)
	if res.Created != 2 || res.CSV != 1 {
		t.Fatalf("created=%d csv=%d", res.Created, res.CSV)
	}
	keep, _ := os.ReadFile(old)
	if string(keep) != "KEEP" {
		t.Fatalf("чужой t.csv затёрт: %q", keep)
	}
	got, err := os.ReadFile(filepath.Join(dir, "t(1).csv"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "t(2).csv")); err == nil {
		t.Fatal("склейка этого запуска не должна плодить t(2).csv")
	}
	if string(got) != "\"id\"\n\"1\"\n\"2\"\n" {
		t.Fatalf("t(1).csv=%q", got)
	}
}

func TestSecondInsertTooManyDoesNotSpoilFirst(t *testing.T) {
	dir := t.TempDir()
	sql := `
INSERT INTO t (id, item) VALUES (1, 'ok');
INSERT INTO t (id, item, extra) VALUES (2, 'x', 'y');
`
	res, log := runFile(t, dir, "dump.sql", sql)
	if res.Created != 1 || res.CSV != 1 || res.Skipped != 1 {
		t.Fatalf("created=%d csv=%d skipped=%d log:\n%s", res.Created, res.CSV, res.Skipped, log)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "\"id\",\"item\"\n\"1\",\"ok\"\n" {
		t.Fatalf("первый CSV испорчен: %q", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "t(1).csv")); err == nil {
		t.Fatal("пропущенный INSERT не должен открывать новый файл")
	}
}

func TestSkipDoesNotStopNextInsert(t *testing.T) {
	dir := t.TempDir()
	sql := `
INSERT INTO t SELECT * FROM u;
INSERT INTO ok (id) VALUES (1);
INSERT INTO t SET a=1;
`
	res, log := runFile(t, dir, "dump.sql", sql)
	if res.Created != 1 || res.Skipped != 2 {
		t.Fatalf("created=%d skipped=%d log:\n%s", res.Created, res.Skipped, log)
	}
	if _, err := os.Stat(filepath.Join(dir, "ok.csv")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "t.csv")); err == nil {
		t.Fatal("для пропущенных INSERT CSV быть не должно")
	}
	if strings.Contains(log, "SELECT * FROM") {
		t.Fatal("в лог нельзя писать VALUES/тело INSERT")
	}
}

func TestOpenMissingFile(t *testing.T) {
	var buf bytes.Buffer
	res := File(logx.New(&buf), csvout.NewRegistry(), scan.SQLFile{
		Path: filepath.Join(t.TempDir(), "нет.sql"),
	})
	if res.OpenErr == nil {
		t.Fatal("ожидалась ошибка открытия")
	}
	if res.Created != 0 {
		t.Fatalf("created=%d", res.Created)
	}
}

func TestPaddedRowWritesWithoutLoggingValues(t *testing.T) {
	dir := t.TempDir()
	res, log := runFile(t, dir, "dump.sql", `INSERT INTO t (a, b, c) VALUES (1);`)
	if res.Created != 1 {
		t.Fatalf("created=%d", res.Created)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "\"a\",\"b\",\"c\"\n\"1\",\"\",\"\"\n" {
		t.Fatalf("CSV=%q", raw)
	}
	if strings.Contains(log, "VALUES (1)") {
		t.Fatal("тело VALUES не должно попадать в лог")
	}
}

func TestFixtureSameTableTwice(t *testing.T) {
	raw := testfilesSQL(t, "04_same_table_twice.sql")
	dir := t.TempDir()
	res, _ := runFile(t, dir, "04_same_table_twice.sql", string(raw))
	if res.Created != 2 || res.CSV != 1 || res.Skipped != 0 {
		t.Fatalf("created=%d csv=%d skipped=%d", res.Created, res.CSV, res.Skipped)
	}
	got, err := os.ReadFile(filepath.Join(dir, "orders.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "orders(1).csv")); err == nil {
		t.Fatal("ожидался один orders.csv")
	}
	want := "\"id\",\"item\"\n\"100\",\"first\"\n\"200\",\"INSERT INTO x (id) VALUES (9)\"\n"
	if string(got) != want {
		t.Fatalf("CSV:\n got %q\nwant %q", got, want)
	}
}

func TestFixtureInterleaveABA(t *testing.T) {
	raw := testfilesSQL(t, "05_interleave_a_b_a.sql")
	dir := t.TempDir()
	res, _ := runFile(t, dir, "05_interleave_a_b_a.sql", string(raw))
	if res.Created != 5 || res.CSV != 2 || res.Skipped != 0 {
		t.Fatalf("created=%d csv=%d skipped=%d", res.Created, res.CSV, res.Skipped)
	}
	gotA, err := os.ReadFile(filepath.Join(dir, "A.csv"))
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := os.ReadFile(filepath.Join(dir, "B.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "A(1).csv")); err == nil {
		t.Fatal("вклинивание B не должно плодить A(1).csv")
	}
	wantA := "\"id\",\"name\"\n\"1\",\"a1\"\n\"2\",\"a2\"\n\"3\",\"a3\"\n"
	wantB := "\"code\",\"qty\"\n\"b1\",\"10\"\n\"b2\",\"20\"\n"
	if string(gotA) != wantA {
		t.Fatalf("A.csv:\n got %q\nwant %q", gotA, wantA)
	}
	if string(gotB) != wantB {
		t.Fatalf("B.csv:\n got %q\nwant %q", gotB, wantB)
	}
}
