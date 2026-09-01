package xlsconv

import (
	"fmt"

	"github.com/xuri/excelize/v2"
)

func readXLSX(path string) (Book, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return Book{}, err
	}
	defer func() { _ = f.Close() }()

	names := make([]string, 0)
	for _, name := range f.GetSheetList() {
		if !isWorksheet(f, name) {
			continue
		}
		names = append(names, name)
		if len(names) > MaxSheets {
			return Book{SkipTooMany: true}, nil
		}
	}

	sheets := make([]Sheet, 0, len(names))
	for _, name := range names {
		rows, err := f.GetRows(name)
		if err != nil {
			return Book{}, fmt.Errorf("лист %q: %w", name, err)
		}
		sheets = append(sheets, makeSheet(name, rows))
	}
	return Book{Sheets: sheets}, nil
}

// isWorksheet отличает worksheet от chart/dialog/macrosheet:
// GetSheetDimension идёт через workSheetReader и на не-листе данных даёт ошибку.
func isWorksheet(f *excelize.File, name string) bool {
	_, err := f.GetSheetDimension(name)
	return err == nil
}
