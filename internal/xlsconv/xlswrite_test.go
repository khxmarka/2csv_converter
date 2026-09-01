package xlsconv

import (
	"path/filepath"
	"testing"
)

func writeXLS(t *testing.T, sheets []specSheet) string {
	t.Helper()
	if len(sheets) == 0 {
		t.Fatal("нужен хотя бы один лист")
	}
	out := make([]Sheet, len(sheets))
	for i, sh := range sheets {
		out[i] = Sheet{Name: sh.Name, Rows: sh.Rows}
	}
	path := filepath.Join(t.TempDir(), "book.xls")
	if err := WriteXLS(path, out); err != nil {
		t.Fatal(err)
	}
	return path
}
