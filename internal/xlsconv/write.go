package xlsconv

import (
	"fmt"
	"path/filepath"
	"strings"

	"sql2csv/internal/csvout"

	"github.com/nkiri/xls"
	"github.com/xuri/excelize/v2"
)

// Result summarizes CSV publication for one Excel workbook.
type Result struct {
	SkipTooMany bool
	CSV         int
	Paths       []string
	OpenErr     error
	WriteErr    error
}

// File converts worksheets as tables whose first row is the CSV header. A
// workbook exceeding MaxSheets is left untouched, and one sheet failure does
// not remove outputs already published for other sheets.
func File(reg *csvout.Registry, path string) Result {
	if reg == nil {
		return Result{OpenErr: errNeedRegistry}
	}
	if strings.EqualFold(filepath.Ext(path), ".xlsx") {
		return fileXLSX(reg, path)
	}
	return fileXLS(reg, path)
}

func fileXLS(reg *csvout.Registry, path string) Result {
	xlsMemoryMu.Lock()
	defer xlsMemoryMu.Unlock()

	book, tooMany, err := openXLS(path)
	if err != nil {
		return Result{OpenErr: err}
	}
	if tooMany {
		return Result{SkipTooMany: true}
	}

	dir := filepath.Dir(path)
	stem := bookStem(path)
	var out Result
	for i := 0; i < book.SheetCount(); i++ {
		sh := book.Sheet(i)
		if sh == nil {
			continue
		}
		p, err := writeXLSSheet(reg, dir, stem, sh)
		if err != nil {
			if out.WriteErr == nil {
				out.WriteErr = err
			}
			continue
		}
		if p == "" {
			continue
		}
		out.CSV++
		out.Paths = append(out.Paths, p)
	}
	return out
}

func writeXLSSheet(reg *csvout.Registry, dir, stem string, sh *xls.Sheet) (string, error) {
	width, last := 0, -1
	for i := 0; i < sh.RowCount(); i++ {
		row := sh.Row(i)
		width = max(width, row.CellCount())
		for col := 0; col < row.CellCount(); col++ {
			if row.Cell(col).Value() != "" {
				last = i
			}
		}
	}
	if last < 0 {
		return "", nil
	}
	rowValues := func(row *xls.Row) []string {
		values := make([]string, row.CellCount())
		for i := range values {
			values[i] = row.Cell(i).Value()
		}
		return values
	}
	header := padRow(rowValues(sh.Row(0)), width)
	data := csvout.CreateData(dir)
	defer data.Abort()
	for i := 1; i <= last; i++ {
		if err := data.Row(rowValues(sh.Row(i))); err != nil {
			return "", err
		}
	}
	if err := data.Finish(); err != nil {
		return "", err
	}
	res, err := csvout.CommitPlain(reg, dir, csvBase(stem, sh.Name()), header, data)
	if err != nil {
		return "", err
	}
	return res.Path, nil
}

func fileXLSX(reg *csvout.Registry, path string) Result {
	book, err := excelize.OpenFile(path, excelize.Options{
		UnzipSizeLimit:    MaxXLSXUnpackedBytes,
		UnzipXMLSizeLimit: MaxXLSXXMLMemoryBytes,
	})
	if err != nil {
		return Result{OpenErr: err}
	}
	defer func() { _ = book.Close() }()

	allNames := book.GetSheetList()
	if len(allNames) > MaxSheets {
		return Result{SkipTooMany: true}
	}
	names := make([]string, 0, len(allNames))
	for _, name := range allNames {
		if !isWorksheet(book, name) {
			continue
		}
		names = append(names, name)
	}

	dir := filepath.Dir(path)
	stem := bookStem(path)
	var out Result
	for _, name := range names {
		p, err := writeXLSXSheet(reg, book, dir, stem, name)
		if err != nil {
			if out.WriteErr == nil {
				out.WriteErr = err
			}
			continue
		}
		if p != "" {
			out.CSV++
			out.Paths = append(out.Paths, p)
		}
	}
	return out
}

// writeXLSXSheet reads a worksheet once and stages rows without padding;
// CommitPlain applies the widest row. Empty sheets and trailing empty rows do
// not produce CSV records.
func writeXLSXSheet(reg *csvout.Registry, book *excelize.File, dir, stem, name string) (string, error) {
	rows, err := book.Rows(name)
	if err != nil {
		return "", fmt.Errorf("лист %q: %w", name, err)
	}
	data := csvout.CreateData(dir)
	defer data.Abort()
	var header []string
	first, nonEmpty := true, false
	pendingEmpty := 0
	for rows.Next() {
		cols, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			return "", fmt.Errorf("лист %q: %w", name, err)
		}
		empty := rowEmpty(cols)
		nonEmpty = nonEmpty || !empty
		if first {
			first = false
			header = cols
			continue
		}
		if empty {
			pendingEmpty++
			continue
		}
		for ; pendingEmpty > 0; pendingEmpty-- {
			if err := data.Row(nil); err != nil {
				_ = rows.Close()
				return "", err
			}
		}
		if err := data.Row(cols); err != nil {
			_ = rows.Close()
			return "", err
		}
	}
	iterErr := rows.Error()
	closeErr := rows.Close()
	if iterErr != nil {
		return "", fmt.Errorf("лист %q: %w", name, iterErr)
	}
	if closeErr != nil {
		return "", closeErr
	}
	if !nonEmpty {
		return "", nil
	}
	if err := data.Finish(); err != nil {
		return "", err
	}
	res, err := csvout.CommitPlain(reg, dir, csvBase(stem, name), header, data)
	if err != nil {
		return "", err
	}
	return res.Path, nil
}

func rowEmpty(row []string) bool {
	for _, value := range row {
		if value != "" {
			return false
		}
	}
	return true
}

func bookStem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func csvBase(book, sheet string) string {
	return csvout.FileBaseDefault(book, "book") + "_" + csvout.FileBaseDefault(sheet, "sheet")
}

func padRow(row []string, n int) []string {
	if len(row) >= n {
		return row
	}
	out := make([]string, n)
	copy(out, row)
	return out
}
