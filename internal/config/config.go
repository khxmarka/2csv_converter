// Package config хранит константы, заданные CONSTRAINTS_AND_POLICY.md.
package config

import (
	"fmt"
	"strings"
)

const (
	// RootDB — корень при ответе db на приглашение combo/db.
	RootDB = `C:\Source\db`
	// RootCombo — корень при ответе combo на приглашение combo/db.
	RootCombo = `C:\Source\combo`
)

// RootFor возвращает жёсткий путь корня по ответу combo/db (пробелы по краям
// отбрасываются, регистр не важен). CLI-флага пути политика не предусматривает.
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
