package xlsconv

import "github.com/xuri/excelize/v2"

// isWorksheet отличает worksheet от chart/dialog/macrosheet:
// GetSheetDimension идёт через workSheetReader и на не-листе данных даёт ошибку.
func isWorksheet(f *excelize.File, name string) bool {
	_, err := f.GetSheetDimension(name)
	return err == nil
}
