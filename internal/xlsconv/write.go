package xlsconv

import (
	"fmt"
	"path/filepath"
	"strings"

	"sql2csv/internal/csvout"

	"github.com/xuri/excelize/v2"
)

// Result — итог записи CSV одного Excel-файла. CSV на диск — только здесь.
type Result struct {
	SkipTooMany bool
	CSV         int
	Paths       []string
	OpenErr     error
	WriteErr    error
}

// File читает книгу как таблицы листов (не SQL/INSERT) и пишет CSV
// с заголовком из первой строки листа. Книгу с >5 листами не трогает.
// Провал одного листа не удаляет уже записанные.
func File(reg *csvout.Registry, path string) Result {
	if reg == nil {
		return Result{OpenErr: errNeedRegistry}
	}
	if strings.EqualFold(filepath.Ext(path), ".xlsx") {
		return fileXLSX(reg, path)
	}
	book, err := Read(path)
	if err != nil {
		return Result{OpenErr: err}
	}
	if book.SkipTooMany {
		return Result{SkipTooMany: true}
	}

	dir := filepath.Dir(path)
	stem := bookStem(path)
	var out Result
	for _, sh := range book.Sheets {
		if sh.Empty {
			continue
		}
		p, err := writeSheet(reg, dir, stem, sh)
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

func fileXLSX(reg *csvout.Registry, path string) Result {
	book, err := excelize.OpenFile(path)
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
		width, nonEmpty, err := xlsxSheetShape(book, name)
		if err != nil {
			if out.WriteErr == nil {
				out.WriteErr = fmt.Errorf("лист %q: %w", name, err)
			}
			continue
		}
		if !nonEmpty {
			continue
		}
		p, err := writeXLSXSheet(reg, book, dir, stem, name, width)
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

func xlsxSheetShape(book *excelize.File, name string) (width int, nonEmpty bool, err error) {
	rows, err := book.Rows(name)
	if err != nil {
		return 0, false, err
	}
	for rows.Next() {
		cols, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			return 0, false, err
		}
		if len(cols) > width {
			width = len(cols)
		}
		if !rowEmpty(cols) {
			nonEmpty = true
		}
	}
	iterErr := rows.Error()
	closeErr := rows.Close()
	if iterErr != nil {
		return 0, false, iterErr
	}
	if closeErr != nil {
		return 0, false, closeErr
	}
	return width, nonEmpty, nil
}

func writeXLSXSheet(
	reg *csvout.Registry,
	book *excelize.File,
	dir, stem, name string,
	width int,
) (string, error) {
	rows, err := book.Rows(name)
	if err != nil {
		return "", err
	}
	var writer *csvout.Writer
	first := true
	pendingEmpty := 0
	abort := func(err error) (string, error) {
		_ = rows.Close()
		if writer != nil {
			_ = writer.Abort()
		}
		return "", err
	}

	for rows.Next() {
		cols, err := rows.Columns()
		if err != nil {
			return abort(err)
		}
		if first {
			first = false
			writer, err = csvout.CreatePlain(reg, dir, csvBase(stem, name), padRow(cols, width))
			if err != nil {
				return abort(err)
			}
			continue
		}
		if rowEmpty(cols) {
			pendingEmpty++
			continue
		}
		for pendingEmpty > 0 {
			if err := writer.Row(nil); err != nil {
				return abort(err)
			}
			pendingEmpty--
		}
		if err := writer.Row(cols); err != nil {
			return abort(err)
		}
	}
	if err := rows.Error(); err != nil {
		return abort(err)
	}
	if err := rows.Close(); err != nil {
		if writer != nil {
			_ = writer.Abort()
		}
		return "", err
	}
	if writer == nil {
		return "", nil
	}
	res, err := writer.Commit()
	if err != nil {
		return "", err
	}
	return res.Path, nil
}

func bookStem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func csvBase(book, sheet string) string {
	return csvout.FileBaseDefault(book, "book") + "_" + csvout.FileBaseDefault(sheet, "sheet")
}

func maxWidth(rows [][]string) int {
	n := 0
	for _, row := range rows {
		if len(row) > n {
			n = len(row)
		}
	}
	return n
}

func writeSheet(reg *csvout.Registry, dir, book string, sh Sheet) (string, error) {
	width := maxWidth(sh.Rows)
	if width < 1 {
		return "", nil
	}
	header := padRow(sh.Rows[0], width)
	w, err := csvout.CreatePlain(reg, dir, csvBase(book, sh.Name), header)
	if err != nil {
		return "", err
	}
	for _, row := range sh.Rows[1:] {
		if err := w.Row(row); err != nil {
			_ = w.Abort()
			return "", err
		}
	}
	res, err := w.Commit()
	if err != nil {
		return "", err
	}
	return res.Path, nil
}

func padRow(row []string, n int) []string {
	if len(row) >= n {
		return row
	}
	out := make([]string, n)
	copy(out, row)
	return out
}
