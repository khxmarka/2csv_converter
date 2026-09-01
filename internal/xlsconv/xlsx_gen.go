package xlsconv

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/xuri/excelize/v2"
)

// WriteXLSX пишет книгу excelize (для тестов и фикстур).
func WriteXLSX(path string, sheets []Sheet) error {
	if len(sheets) == 0 {
		return fmt.Errorf("xlsconv: нужен хотя бы один лист")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	for i, sh := range sheets {
		name := sh.Name
		if i == 0 {
			if err := f.SetSheetName("Sheet1", name); err != nil {
				return err
			}
		} else if _, err := f.NewSheet(name); err != nil {
			return err
		}
		for r, row := range sh.Rows {
			for c, val := range row {
				cell, err := excelize.CoordinatesToCellName(c+1, r+1)
				if err != nil {
					return err
				}
				if err := f.SetCellValue(name, cell, val); err != nil {
					return err
				}
			}
		}
	}
	return f.SaveAs(path)
}
