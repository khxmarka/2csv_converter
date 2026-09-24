package csvout

import (
	"errors"

	"sql2csv/internal/insert"
)

// ErrTooManyValues — в строке VALUES больше ячеек, чем колонок в заголовке.
// Означает пропуск этой строки, не всего INSERT.
var ErrTooManyValues = errors.New("значений больше, чем колонок")

// NormalizeRow приводит строку INSERT к ширине заголовка: NULL/дыры → "",
// недостающие ячейки дополняются. Лишние значения — ErrTooManyValues (строка).
func NormalizeRow(cells []insert.Cell, nCol int) (row []string, padded bool, err error) {
	if nCol < 0 {
		nCol = 0
	}
	if nCol == 0 {
		nCol = len(cells)
	}
	if len(cells) > nCol {
		return nil, false, ErrTooManyValues
	}
	row = make([]string, nCol)
	for i := 0; i < nCol; i++ {
		if i >= len(cells) {
			padded = true
			continue
		}
		switch cells[i].Kind {
		case insert.Null, insert.Missing:
		default:
			row[i] = cells[i].Text
		}
	}
	return row, padded, nil
}
