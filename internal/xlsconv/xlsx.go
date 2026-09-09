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

	allNames := f.GetSheetList()
	if len(allNames) > MaxSheets {
		return Book{SkipTooMany: true}, nil
	}
	names := make([]string, 0, len(allNames))
	for _, name := range allNames {
		if !isWorksheet(f, name) {
			continue
		}
		names = append(names, name)
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
