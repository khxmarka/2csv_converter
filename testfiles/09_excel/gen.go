// Генератор ручных фикстур Excel. Из корня репозитория:
//
//	go run ./testfiles/09_excel
package main

import (
	"log"
	"path/filepath"

	"sql2csv/internal/xlsconv"
)

func main() {
	dir := filepath.Join("testfiles", "09_excel")
	must(xlsconv.WriteXLSX(filepath.Join(dir, "01_one.xlsx"), []xlsconv.Sheet{
		{Name: "Data", Rows: [][]string{
			{"id", "name"},
			{"1", "Ann"},
		}},
	}))
	must(xlsconv.WriteXLSX(filepath.Join(dir, "02_two.xlsx"), []xlsconv.Sheet{
		{Name: "First", Rows: [][]string{{"a"}}},
		{Name: "Second", Rows: [][]string{{"b"}}},
	}))
	six := make([]xlsconv.Sheet, 6)
	for i := range six {
		six[i] = xlsconv.Sheet{Name: string(rune('A' + i)), Rows: [][]string{{"x"}}}
	}
	must(xlsconv.WriteXLS(filepath.Join(dir, "03_six.xls"), six))
	must(xlsconv.WriteXLSX(filepath.Join(dir, "04_quote.xlsx"), []xlsconv.Sheet{
		{Name: "Q", Rows: [][]string{{`he said "hello"`}}},
	}))
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
