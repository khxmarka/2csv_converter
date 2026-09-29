package app

import "os"

type fileOutcome struct {
	openErr     error
	writeErr    error
	created     int
	skipped     int
	csv         int
	skipTooMany bool
	failed      bool
	piiSkip     int
	unitFail    int
	splitFail   bool
	split       bool // лежавший .csv/.txt нарезан
}

// producedCSV — исходник можно удалить (§15): CSV получен и ни одна единица
// файла не провалилась. Иначе удаление унесло бы INSERT или лист, которые
// в CSV не попали (ошибка записи, лишние значения, занятое имя).
func producedCSV(out fileOutcome) bool {
	if out.writeErr != nil || out.failed || out.unitFail > 0 {
		return false
	}
	return out.created > 0 || out.csv > 0
}

var removeSource = os.Remove
