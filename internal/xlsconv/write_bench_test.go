package xlsconv

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/xuri/excelize/v2"

	"sql2csv/internal/csvout"
)

func BenchmarkFileXLSX(b *testing.B) {
	const rows = 20_000
	src := filepath.Join(b.TempDir(), "book.xlsx")
	f := excelize.NewFile()
	sw, err := f.NewStreamWriter("Sheet1")
	if err != nil {
		b.Fatal(err)
	}
	for r := 1; r <= rows; r++ {
		cell, _ := excelize.CoordinatesToCellName(1, r)
		n := strconv.Itoa(r)
		if err := sw.SetRow(cell, []any{n, "user" + n + "@example.test", "Name " + n, "555-" + n}); err != nil {
			b.Fatal(err)
		}
	}
	if err := sw.Flush(); err != nil {
		b.Fatal(err)
	}
	if err := f.SaveAs(src); err != nil {
		b.Fatal(err)
	}
	_ = f.Close()

	b.ReportAllocs()
	for b.Loop() {
		res := File(csvout.NewRegistry(), src)
		if res.CSV != 1 || res.WriteErr != nil {
			b.Fatalf("%+v", res)
		}
		_ = os.Remove(res.Paths[0])
	}
}
