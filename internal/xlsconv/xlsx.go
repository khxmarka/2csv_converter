package xlsconv

import "github.com/xuri/excelize/v2"

// isWorksheet distinguishes data worksheets from chart, dialog, and macro sheets.
// GetSheetDimension rejects non-worksheet XML through excelize's worksheet reader.
func isWorksheet(f *excelize.File, name string) bool {
	_, err := f.GetSheetDimension(name)
	return err == nil
}
