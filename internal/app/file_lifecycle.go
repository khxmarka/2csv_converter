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
	split       bool // A pre-existing CSV or text file was split.
}

// producedCSV permits source deletion only when output exists and every unit
// succeeded. Otherwise deletion could discard an INSERT or sheet missing from CSV.
func producedCSV(out fileOutcome) bool {
	if out.writeErr != nil || out.failed || out.unitFail > 0 {
		return false
	}
	return out.created > 0 || out.csv > 0
}

var removeSource = os.Remove
