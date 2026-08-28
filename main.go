// Команда sql2csv: консольный конвертер INSERT-ов из .sql в CSV.
package main

import (
	"os"

	"sql2csv/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
