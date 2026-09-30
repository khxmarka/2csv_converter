// Package config defines the fixed input roots used by the command.
package config

import (
	"fmt"
	"strings"
)

const (
	// RootDB is selected by the "db" prompt answer.
	RootDB = `C:\Source\db`
	// RootCombo is selected by the "combo" prompt answer.
	RootCombo = `C:\Source\combo`
)

// RootFor maps a case-insensitive combo/db answer to its fixed input root.
// Leading and trailing whitespace is ignored.
func RootFor(choice string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "db":
		return RootDB, nil
	case "combo":
		return RootCombo, nil
	default:
		got := strings.TrimSpace(choice)
		if got == "" {
			return "", fmt.Errorf("ожидалось combo или db")
		}
		return "", fmt.Errorf("ожидалось combo или db, получено %q", got)
	}
}
