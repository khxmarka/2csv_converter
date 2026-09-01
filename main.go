// Команда 2csv: консольный конвертер INSERT-ов из .sql и листов Excel в CSV.
package main

import (
	"os"

	"sql2csv/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
