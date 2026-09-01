package xlsconv

import (
	"path/filepath"
	"strings"

	"sql2csv/internal/csvout"
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
