package xlsconv

import (
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

type specSheet struct {
	Name       string
	Rows       [][]string
	Hidden     bool
	VeryHidden bool
}

func writeXLSX(t *testing.T, sheets []specSheet) string {
	t.Helper()
	if len(sheets) == 0 {
		t.Fatal("нужен хотя бы один лист")
	}
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	for i, sh := range sheets {
		name := sh.Name
		if i == 0 {
			if err := f.SetSheetName("Sheet1", name); err != nil {
				t.Fatalf("SetSheetName: %v", err)
			}
		} else {
			if _, err := f.NewSheet(name); err != nil {
				t.Fatalf("NewSheet %s: %v", name, err)
			}
		}
		for r, row := range sh.Rows {
			for c, val := range row {
				cell, err := excelize.CoordinatesToCellName(c+1, r+1)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.SetCellValue(name, cell, val); err != nil {
					t.Fatalf("SetCellValue %s %s: %v", name, cell, err)
				}
			}
		}
	}
	// Скрывать после создания всех листов: в книге должен остаться хотя бы один видимый.
	for _, sh := range sheets {
		switch {
		case sh.VeryHidden:
			if err := f.SetSheetVisible(sh.Name, false, true); err != nil {
				t.Fatalf("veryHidden %s: %v", sh.Name, err)
			}
		case sh.Hidden:
			if err := f.SetSheetVisible(sh.Name, false); err != nil {
				t.Fatalf("hidden %s: %v", sh.Name, err)
			}
		}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "book.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	return path
}

func assertSheetNames(t *testing.T, book Book, want ...string) {
	t.Helper()
	if book.SkipTooMany {
		t.Fatal("не ожидали SkipTooMany")
	}
	if len(book.Sheets) != len(want) {
		t.Fatalf("листов: %d, ожидалось %d", len(book.Sheets), len(want))
	}
	for i, name := range want {
		if book.Sheets[i].Name != name {
			t.Fatalf("лист %d: имя %q, ожидалось %q", i, book.Sheets[i].Name, name)
		}
	}
}

func cell(t *testing.T, sh Sheet, row, col int) string {
	t.Helper()
	if row < 0 || row >= len(sh.Rows) {
		t.Fatalf("лист %q: нет строки %d (строк %d)", sh.Name, row, len(sh.Rows))
	}
	r := sh.Rows[row]
	if col < 0 || col >= len(r) {
		return ""
	}
	return r[col]
}
