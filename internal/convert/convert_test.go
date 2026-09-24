package convert

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	res := Schedule(logx.New(&buf), reg, scan.SQLFile{Path: path, TopFolder: "Alpha"}, nil)
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
	if res.Created != 1 || res.CSV != 1 || res.Skipped != 1 {
		t.Fatalf("created=%d csv=%d skipped=%d", res.Created, res.CSV, res.Skipped)
	}
	users := filepath.Join(dir, "users.csv")
	orders := filepath.Join(dir, "orders.csv")
	if _, err := os.Stat(users); err != nil {
		t.Fatalf("нет users.csv: %v", err)
	}
	if _, err := os.Stat(orders); !os.IsNotExist(err) {
		t.Fatalf("orders.csv не должен создаваться, err=%v", err)
	}
}

func TestPIIFilterMatchesColumnAndStaysSilent(t *testing.T) {
	dir := t.TempDir()
	sql := `
INSERT INTO settings (id, email) VALUES (1, 'a@example.com');
INSERT INTO settings (email, phone) VALUES ('a@example.com', '555');
INSERT INTO audit_log (id, status) VALUES (2, 'ok');
`
	res, log := runFile(t, dir, "dump.sql", sql)
	if res.Created != 1 || res.CSV != 1 || res.Skipped != 2 {
		t.Fatalf("created=%d csv=%d skipped=%d", res.Created, res.CSV, res.Skipped)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.csv")); err != nil {
		t.Fatalf("нет settings.csv: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "audit_log.csv")); !os.IsNotExist(err) {
		t.Fatalf("audit_log.csv не должен создаваться, err=%v", err)
	}
	if log != "" {
		t.Fatalf("PII-фильтр должен пропускать INSERT без лога: %q", log)
	}
}

func TestPIIFilterRequiresTwoColumnsWithoutTableMatch(t *testing.T) {
	dir := t.TempDir()
	sql := `
INSERT INTO t (email) VALUES ('a@example.test');
INSERT INTO t (phone) VALUES ('555');
INSERT INTO t (email, phone) VALUES ('a@example.test', '555');
INSERT INTO t (user_id, email) VALUES (1, 'a@example.test');
INSERT INTO t (national_id, email) VALUES ('X', 'a@example.test');
INSERT INTO t (passport_id, ssn_id) VALUES ('P', 'S');
INSERT INTO users (id, status) VALUES (1, 'active');
`
	res, log := runFile(t, dir, "dump.sql", sql)
	if res.Created != 4 || res.CSV != 2 || res.Skipped != 3 {
		t.Fatalf("created=%d csv=%d skipped=%d log=%q", res.Created, res.CSV, res.Skipped, log)
	}
	if log != "" {
		t.Fatalf("фильтр должен молчать: %q", log)
	}
	tCSV, err := os.ReadFile(filepath.Join(dir, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tCSV), `"email","phone"`) {
		t.Fatalf("t.csv должен начаться с двух PII-колонок: %q", tCSV)
	}
	if _, err := os.Stat(filepath.Join(dir, "users.csv")); err != nil {
		t.Fatal(err)
	}
}

func TestPIIRejectDoesNotDoubleCountBrokenTail(t *testing.T) {
	dir := t.TempDir()
	sql := `
INSERT INTO t (email) VALUES ('a@example.test'), ('b', 'c');
INSERT INTO t SELECT 1;
`
	res, log := runFile(t, dir, "dump.sql", sql)
	if res.Created != 0 || res.CSV != 0 || res.Skipped != 2 {
		t.Fatalf("created=%d csv=%d skipped=%d log=%q", res.Created, res.CSV, res.Skipped, log)
	}
}

func TestPIIFilterWithoutColumnsUsesOnlyTableName(t *testing.T) {
	dir := t.TempDir()
	sql := `
INSERT INTO users VALUES (1, 'Ann');
INSERT INTO settings VALUES (2, 'dark');
`
	res, _ := runFile(t, dir, "dump.sql", sql)
	if res.Created != 1 || res.CSV != 1 || res.Skipped != 1 {
		t.Fatalf("created=%d csv=%d skipped=%d", res.Created, res.CSV, res.Skipped)
	}
	if _, err := os.Stat(filepath.Join(dir, "users.csv")); err != nil {
		t.Fatalf("нет users.csv: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.csv")); !os.IsNotExist(err) {
		t.Fatalf("settings.csv не должен создаваться, err=%v", err)
	}
}

func TestTwoInsertsSameTableMerged(t *testing.T) {
	dir := t.TempDir()
	sql := `
INSERT INTO t (id, name, email) VALUES (1, 'first', 'a@example.test');
INSERT INTO t (id, name, email) VALUES (2, 'second', 'b@example.test');
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
	want := "\"id\",\"name\",\"email\"\n\"1\",\"first\",\"a@example.test\"\n\"2\",\"second\",\"b@example.test\"\n"
	if got != want {
		t.Fatalf("CSV:\n got %q\nwant %q", got, want)
	}
	if strings.Count(got, `"id","name","email"`) != 1 {
		t.Fatalf("вторая строка заголовка:\n%s", got)
	}
	if strings.Contains(log, "VALUES") {
		t.Fatal("тело VALUES не должно попадать в лог")
	}
}

func TestTwoSQLFilesSameTableMerged(t *testing.T) {
	dir := t.TempDir()
	reg := csvout.NewRegistry()
	res1, _ := runFileReg(t, reg, dir, "a.sql", `INSERT INTO t (email, phone) VALUES (1, 'a');`)
	res2, _ := runFileReg(t, reg, dir, "b.sql", `INSERT INTO t (email, phone) VALUES (2, 'b');`)
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
	want := "\"email\",\"phone\"\n\"1\",\"a\"\n\"2\",\"b\"\n"
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
	runFileReg(t, reg, alpha, "a.sql", `INSERT INTO t (email, phone) VALUES (1, 'a');`)
	runFileReg(t, reg, beta, "b.sql", `INSERT INTO t (email, phone) VALUES (2, 'b');`)
	rawA, err := os.ReadFile(filepath.Join(alpha, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	rawB, err := os.ReadFile(filepath.Join(beta, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(rawA) != "\"email\",\"phone\"\n\"1\",\"a\"\n" || string(rawB) != "\"email\",\"phone\"\n\"2\",\"b\"\n" {
		t.Fatalf("Alpha=%q Beta=%q", rawA, rawB)
	}
}

func TestPreexistingCSVOverwrittenThenMerged(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "t.csv")
	if err := os.WriteFile(old, []byte("KEEP"), 0o644); err != nil {
		t.Fatal(err)
	}
	sql := `
INSERT INTO t (email, phone) VALUES (1, 'a');
INSERT INTO t (email, phone) VALUES (2, 'b');
`
	res, _ := runFile(t, dir, "dump.sql", sql)
	if res.Created != 2 || res.CSV != 1 {
		t.Fatalf("created=%d csv=%d", res.Created, res.CSV)
	}
	got, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "t(1).csv")); !os.IsNotExist(err) {
		t.Fatalf("склейка этого запуска не должна плодить t(1).csv, err=%v", err)
	}
	if string(got) != "\"email\",\"phone\"\n\"1\",\"a\"\n\"2\",\"b\"\n" {
		t.Fatalf("t.csv=%q", got)
	}
}

func TestRejectedInsertDoesNotOverwriteExistingCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.csv")
	if err := os.WriteFile(path, []byte("KEEP"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := runFile(t, dir, "dump.sql", `
INSERT INTO users (email) VALUES ('a@example.test', 'extra');
INSERT INTO users (email) VALUES ('valid@example.test'), ('a' 'no comma');
`)
	if res.Created != 0 || res.CSV != 0 || res.Skipped != 2 {
		t.Fatalf("created=%d csv=%d skipped=%d", res.Created, res.CSV, res.Skipped)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "KEEP" {
		t.Fatalf("отклонённый INSERT изменил существующий CSV: %q", raw)
	}
}

func TestSecondInsertTooManyDoesNotSpoilFirst(t *testing.T) {
	dir := t.TempDir()
	sql := `
INSERT INTO t (id, name, email) VALUES (1, 'ok', 'a@example.test');
INSERT INTO t (id, name, email, extra) VALUES (2, 'x', 'y', 'z');
`
	res, log := runFile(t, dir, "dump.sql", sql)
	if res.Created != 1 || res.CSV != 1 || res.Skipped != 1 {
		t.Fatalf("created=%d csv=%d skipped=%d log:\n%s", res.Created, res.CSV, res.Skipped, log)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "\"id\",\"name\",\"email\"\n\"1\",\"ok\",\"a@example.test\"\n" {
		t.Fatalf("первый CSV испорчен: %q", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "t(1).csv")); err == nil {
		t.Fatal("пропущенный INSERT не должен открывать новый файл")
	}
	if !strings.Contains(log, "таблица t") || !strings.Contains(log, "значений больше, чем колонок") {
		t.Fatalf("нужна причина провала INSERT:\n%s", log)
	}
	if strings.Contains(log, "VALUES") || strings.Contains(log, "'x'") {
		t.Fatal("тело VALUES не должно попадать в лог")
	}
}

func TestSurplusRowKeepsValidRowsInSameInsert(t *testing.T) {
	dir := t.TempDir()
	sql := `INSERT INTO t (email, phone) VALUES ('a@example.test', '1'), ('b', '2', 'extra'), ('c@example.test', '3');`
	res, log := runFile(t, dir, "dump.sql", sql)
	if res.Created != 1 || res.CSV != 1 {
		t.Fatalf("created=%d csv=%d log:\n%s", res.Created, res.CSV, log)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	want := "\"email\",\"phone\"\n\"a@example.test\",\"1\"\n\"c@example.test\",\"3\"\n"
	if string(raw) != want {
		t.Fatalf("CSV:\n got %q\nwant %q", raw, want)
	}
	if !strings.Contains(log, "1 строк пропущено") {
		t.Fatalf("нужен счётчик surplus:\n%s", log)
	}
	if strings.Contains(log, "extra") || strings.Contains(log, "VALUES") {
		t.Fatal("тело VALUES не должно попадать в лог")
	}
}

func TestLargeCellStreamsWithoutFullBuffer(t *testing.T) {
	dir := t.TempDir()
	const n = 2_000_000
	path := filepath.Join(dir, "big.sql")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(f, "INSERT INTO t (email, phone) VALUES ('"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64*1024)
	for i := range buf {
		buf[i] = 'x'
	}
	left := n
	for left > 0 {
		chunk := buf
		if left < len(chunk) {
			chunk = buf[:left]
		}
		if _, err := f.Write(chunk); err != nil {
			t.Fatal(err)
		}
		left -= len(chunk)
	}
	if _, err := io.WriteString(f, "', '555');"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	var logBuf bytes.Buffer
	res := Schedule(logx.New(&logBuf), csvout.NewRegistry(), scan.SQLFile{Path: path, TopFolder: "Alpha"}, nil)
	if res.Created != 1 || res.CSV != 1 {
		t.Fatalf("created=%d csv=%d log:\n%s", res.Created, res.CSV, logBuf.String())
	}
	raw, err := os.ReadFile(filepath.Join(dir, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	wantPrefix := "\"email\",\"phone\"\n\""
	wantSuffix := "\",\"555\"\n"
	if !strings.HasPrefix(string(raw), wantPrefix) || !strings.HasSuffix(string(raw), wantSuffix) {
		t.Fatalf("обёртка CSV сломана, len=%d", len(raw))
	}
	body := string(raw[len(wantPrefix) : len(raw)-len(wantSuffix)])
	if len(body) != n {
		t.Fatalf("тело ячейки: len=%d want %d", len(body), n)
	}
}

func TestSkipDoesNotStopNextInsert(t *testing.T) {
	dir := t.TempDir()
	sql := `
INSERT INTO t SELECT * FROM u;
INSERT INTO ok (email, phone) VALUES (1, '555');
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
	res := Schedule(logx.New(&buf), csvout.NewRegistry(), scan.SQLFile{
		Path: filepath.Join(t.TempDir(), "нет.sql"),
	}, nil)
	if res.OpenErr == nil {
		t.Fatal("ожидалась ошибка открытия")
	}
	if res.Created != 0 {
		t.Fatalf("created=%d", res.Created)
	}
}

func TestPaddedRowWritesWithoutLoggingValues(t *testing.T) {
	dir := t.TempDir()
	res, log := runFile(t, dir, "dump.sql", `INSERT INTO t (a, email, phone) VALUES (1);`)
	if res.Created != 1 {
		t.Fatalf("created=%d", res.Created)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "\"a\",\"email\",\"phone\"\n\"1\",\"\",\"\"\n" {
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
	want := "\"id\",\"name\",\"email\"\n\"100\",\"first\",\"a@example.test\"\n\"200\",\"INSERT INTO x (id) VALUES (9)\",\"b@example.test\"\n"
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
	wantA := "\"id\",\"name\",\"email\"\n\"1\",\"a1\",\"a1@example.test\"\n\"2\",\"a2\",\"a2@example.test\"\n\"3\",\"a3\",\"557\"\n"
	wantB := "\"code\",\"email\",\"phone\"\n\"b1\",\"10\",\"555\"\n\"b2\",\"20\",\"556\"\n"
	if string(gotA) != wantA {
		t.Fatalf("A.csv:\n got %q\nwant %q", gotA, wantA)
	}
	if string(gotB) != wantB {
		t.Fatalf("B.csv:\n got %q\nwant %q", gotB, wantB)
	}
}

func TestPIIFixtures(t *testing.T) {
	tests := []struct {
		name    string
		created int
		skipped int
		files   []string
	}{
		{name: "10_pii.sql", created: 2, files: []string{"archive.csv", "user_profiles.csv"}},
		{name: "11_no_pii.sql", skipped: 3},
		{name: "12_mixed_pii.sql", created: 2, skipped: 2, files: []string{"audit_log.csv", "users.csv"}},
		{name: "13_one_column_rejected.sql", skipped: 2},
		{name: "14_two_columns_ok.sql", created: 1, files: []string{"t.csv"}},
		{name: "15_technical_id_ignored.sql", skipped: 2},
		{name: "16_document_id_counts.sql", created: 2, files: []string{"docs.csv", "ids.csv"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			res, log := runFile(t, dir, tt.name, testfilesSQL(t, tt.name))
			if res.Created != tt.created || res.Skipped != tt.skipped || res.CSV != len(tt.files) {
				t.Fatalf("created=%d skipped=%d csv=%d log=%q", res.Created, res.Skipped, res.CSV, log)
			}
			for _, name := range tt.files {
				if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
					t.Fatalf("нет %s: %v", name, err)
				}
			}
		})
	}
}

func TestValuesWithoutColumnsNoHeader(t *testing.T) {
	dir := t.TempDir()
	sql := `INSERT INTO users VALUES (334,10,'genre','Action'),(335,10,'genre','Adventure'),(336,10,'genre','Fantastique'),(337,10,'genre','Mystère')`
	res, _ := runFile(t, dir, "dump.sql", sql)
	if res.Created != 1 || res.CSV != 1 || res.Skipped != 0 {
		t.Fatalf("created=%d csv=%d skipped=%d", res.Created, res.CSV, res.Skipped)
	}
	got, err := os.ReadFile(filepath.Join(dir, "users.csv"))
	if err != nil {
		t.Fatal(err)
	}
	want := "\"334\",\"10\",\"genre\",\"Action\"\n\"335\",\"10\",\"genre\",\"Adventure\"\n\"336\",\"10\",\"genre\",\"Fantastique\"\n\"337\",\"10\",\"genre\",\"Mystère\"\n"
	if string(got) != want {
		t.Fatalf("CSV:\n got %q\nwant %q", got, want)
	}
}

func TestLegacyFixturesRemainCompatibleWithPIIFilter(t *testing.T) {
	t.Run("skips then ok", func(t *testing.T) {
		dir := t.TempDir()
		res, _ := runFile(t, dir, "03.sql", testfilesSQL(t, "03_skips_then_ok.sql"))
		if res.Created != 1 || res.CSV != 1 || res.Skipped != 2 {
			t.Fatalf("результат: %+v", res)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "ok.csv"))
		if err != nil {
			t.Fatal(err)
		}
		want := "\"email\",\"phone\"\n\"survived@example.test\",\"555\"\n"
		if string(raw) != want {
			t.Fatalf("ok.csv:\n got %q\nwant %q", raw, want)
		}
	})

	t.Run("second row wider than established header", func(t *testing.T) {
		dir := t.TempDir()
		res, log := runFile(t, dir, "06.sql", testfilesSQL(t, "06_second_too_many.sql"))
		if res.Created != 1 || res.CSV != 1 || res.Skipped != 1 {
			t.Fatalf("результат: %+v", res)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "t.csv"))
		if err != nil {
			t.Fatal(err)
		}
		want := "\"id\",\"name\",\"email\"\n\"1\",\"ok\",\"a@example.test\"\n"
		if string(raw) != want {
			t.Fatalf("t.csv:\n got %q\nwant %q", raw, want)
		}
		if !strings.Contains(log, "таблица t") || !strings.Contains(log, "значений больше, чем колонок") {
			t.Fatalf("нужна причина провала INSERT:\n%s", log)
		}
	})
}

func TestValuesWithoutColumnsThenWithColumnsAppends(t *testing.T) {
	dir := t.TempDir()
	reg := csvout.NewRegistry()
	res1, _ := runFileReg(t, reg, dir, "a.sql", `INSERT INTO users VALUES (1, 'a');`)
	res2, _ := runFileReg(t, reg, dir, "b.sql", `INSERT INTO users (id, name) VALUES (2, 'b');`)
	if res1.Created != 1 || res1.CSV != 1 || res2.Created != 1 || res2.CSV != 0 {
		t.Fatalf("created/csv %d/%d и %d/%d", res1.Created, res1.CSV, res2.Created, res2.CSV)
	}
	got, err := os.ReadFile(filepath.Join(dir, "users.csv"))
	if err != nil {
		t.Fatal(err)
	}
	want := "\"1\",\"a\"\n\"2\",\"b\"\n"
	if string(got) != want {
		t.Fatalf("CSV:\n got %q\nwant %q", got, want)
	}
}

func TestLargeInsertThroughFilePipeline(t *testing.T) {
	const rows = 10_000
	var sql strings.Builder
	sql.WriteString("INSERT INTO users (id, email) VALUES\n")
	for i := range rows {
		if i > 0 {
			sql.WriteString(",\n")
		}
		sql.WriteByte('(')
		sql.WriteString(strconv.Itoa(i))
		sql.WriteString(",'user")
		sql.WriteString(strconv.Itoa(i))
		sql.WriteString("@example.test')")
	}
	sql.WriteString(";\n")

	dir := t.TempDir()
	res, log := runFile(t, dir, "large.sql", sql.String())
	if res.Created != 1 || res.CSV != 1 || res.Skipped != 0 || res.Failed {
		t.Fatalf("результат=%+v log=%q", res, log)
	}
	f, err := os.Open(filepath.Join(dir, "users.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	lines := 0
	for scanner.Scan() {
		lines++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if lines != rows+1 {
		t.Fatalf("строк CSV=%d, ожидалось %d", lines, rows+1)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".2csv-") {
			t.Fatalf("после Commit остался temp: %s", entry.Name())
		}
	}
}

func TestTabularCSVSQLWritesOurFormatAndKeepsSource(t *testing.T) {
	raw := testfilesSQL(t, "17_table_csv.sql")
	dir := t.TempDir()
	path := filepath.Join(dir, "GameSalad.sql")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	res := Schedule(logx.New(&buf), csvout.NewRegistry(), scan.SQLFile{Path: path, TopFolder: "Alpha"}, nil)
	if res.OpenErr != nil || res.Failed || res.CSV != 1 || res.PIISkip != 0 {
		t.Fatalf("результат=%+v log=%q", res, buf.String())
	}
	log := buf.String()
	if strings.Count(log, "warn:") != 1 || !strings.Contains(log, "не INSERT, а таблица") {
		t.Fatalf("нужен один warn:\n%s", log)
	}
	if strings.Contains(log, "a@example.test") || strings.Contains(log, "VALUES") {
		t.Fatal("строки таблицы нельзя дампить в лог")
	}
	got, err := os.ReadFile(filepath.Join(dir, "GameSalad.csv"))
	if err != nil {
		t.Fatal(err)
	}
	want := "\"email\",\"phone\"\n\"a@example.test\",\"555\"\n\"b@example.test\",\"777\"\n"
	if string(got) != want {
		t.Fatalf("CSV:\n got %q\nwant %q", got, want)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != raw {
		t.Fatalf("исходник изменён")
	}
}

func TestTabularTSVSQLWritesOurFormat(t *testing.T) {
	dir := t.TempDir()
	res, log := runFile(t, dir, "18_table_tsv.sql", testfilesSQL(t, "18_table_tsv.sql"))
	if res.CSV != 1 {
		t.Fatalf("результат=%+v", res)
	}
	if strings.Count(log, "warn:") != 1 {
		t.Fatalf("warn:\n%s", log)
	}
	got, err := os.ReadFile(filepath.Join(dir, "18_table_tsv.csv"))
	if err != nil {
		t.Fatal(err)
	}
	want := "\"email\",\"phone\"\n\"a@example.test\",\"555\"\n\"b@example.test\",\"777\"\n"
	if string(got) != want {
		t.Fatalf("CSV:\n got %q\nwant %q", got, want)
	}
}

func TestTabularSQLPIIRejectsWithoutCSV(t *testing.T) {
	dir := t.TempDir()
	raw := testfilesSQL(t, "19_table_no_pii.sql")
	path := filepath.Join(dir, "19_table_no_pii.sql")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	res := Schedule(logx.New(&buf), csvout.NewRegistry(), scan.SQLFile{Path: path, TopFolder: "Alpha"}, nil)
	if res.CSV != 0 || res.PIISkip != 1 {
		t.Fatalf("результат=%+v log=%q", res, buf.String())
	}
	if strings.Count(buf.String(), "warn:") != 1 {
		t.Fatalf("warn после PII всё равно нужен:\n%s", buf.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "19_table_no_pii.csv")); !os.IsNotExist(err) {
		t.Fatalf("CSV не должен создаваться, err=%v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != raw {
		t.Fatal("исходник изменён")
	}
}

func TestTabularSQLStemPIIAllowsCSV(t *testing.T) {
	dir := t.TempDir()
	res, log := runFile(t, dir, "users.sql", "id,status\n1,ok\n")
	if res.CSV != 1 || res.PIISkip != 0 {
		t.Fatalf("результат=%+v log=%q", res, log)
	}
	if _, err := os.Stat(filepath.Join(dir, "users.csv")); err != nil {
		t.Fatal(err)
	}
}

func TestTabularSQLSemicolonDelimiter(t *testing.T) {
	dir := t.TempDir()
	res, _ := runFile(t, dir, "users.sql", "email;phone\na@example.test;555\n")
	if res.CSV != 1 {
		t.Fatalf("результат=%+v", res)
	}
	got, err := os.ReadFile(filepath.Join(dir, "users.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "\"email\",\"phone\"\n\"a@example.test\",\"555\"\n" {
		t.Fatalf("CSV=%q", got)
	}
}

func TestInsertSQLStillParsedNotAsTable(t *testing.T) {
	dir := t.TempDir()
	res, log := runFile(t, dir, "dump.sql", "INSERT INTO t (email, phone) VALUES (1, '555');\n")
	if res.Created != 1 || res.CSV != 1 {
		t.Fatalf("результат=%+v", res)
	}
	if strings.Contains(log, "не INSERT, а таблица") {
		t.Fatalf("INSERT не должен идти как таблица:\n%s", log)
	}
}

func TestSelectThenInsertIsNotTabularDump(t *testing.T) {
	dir := t.TempDir()
	sql := "SELECT email, phone FROM users;\nINSERT INTO t (email, phone) VALUES ('a@example.test', '555');\n"
	res, log := runFile(t, dir, "dump.sql", sql)
	if strings.Contains(log, "не INSERT, а таблица") {
		t.Fatalf("SELECT не должен становиться шапкой:\n%s", log)
	}
	if res.Created != 1 || res.CSV != 1 {
		t.Fatalf("INSERT после SELECT потерян: %+v log=%q", res, log)
	}
	got, err := os.ReadFile(filepath.Join(dir, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "a@example.test") {
		t.Fatalf("CSV=%q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "dump.csv")); !os.IsNotExist(err) {
		t.Fatalf("ложный dump.csv, err=%v", err)
	}
}

func TestTabularAllRowsTooWideDoesNotKeepHeaderOnlyCSV(t *testing.T) {
	dir := t.TempDir()
	res, log := runFile(t, dir, "users.sql", "email,phone\na,b,c,d\ne,f,g,h\n")
	if res.CSV != 0 {
		t.Fatalf("нельзя оставлять CSV из одной шапки: %+v log=%q", res, log)
	}
	if res.UnitFail < 1 {
		t.Fatalf("нужна ошибка лишних значений: %+v log=%q", res, log)
	}
	if _, err := os.Stat(filepath.Join(dir, "users.csv")); !os.IsNotExist(err) {
		t.Fatalf("users.csv не должен остаться, err=%v", err)
	}
}
