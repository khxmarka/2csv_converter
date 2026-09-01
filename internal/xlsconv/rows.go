package xlsconv

func makeSheet(name string, rows [][]string) Sheet {
	rows = trimTrailingEmpty(rows)
	return Sheet{
		Name:  name,
		Rows:  rows,
		Empty: isEmptySheet(rows),
	}
}

func isEmptySheet(rows [][]string) bool {
	for _, row := range rows {
		if !rowEmpty(row) {
			return false
		}
	}
	return true
}

func trimTrailingEmpty(rows [][]string) [][]string {
	end := len(rows)
	for end > 0 && rowEmpty(rows[end-1]) {
		end--
	}
	if end == 0 {
		return nil
	}
	return rows[:end]
}

func rowEmpty(row []string) bool {
	for _, c := range row {
		if c != "" {
			return false
		}
	}
	return true
}
