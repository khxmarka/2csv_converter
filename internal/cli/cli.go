// Package cli разбирает аргументы командной строки и вызывает app.
package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"sql2csv/internal/app"
	"sql2csv/internal/config"
	"sql2csv/internal/logx"
)

const (
	exitOK    = 0
	exitFatal = 1
	exitUsage = 2
)

// Run выполняет запуск и возвращает код выхода процесса.
func Run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("sql2csv", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { printUsage(stderr) }
	help := flags.Bool("help", false, "показать справку и выйти")
	flags.BoolVar(help, "h", false, "то же, что --help")

	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if *help {
		printUsage(stdout)
		return exitOK
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "error: лишние аргументы: %s\n", strings.Join(flags.Args(), " "))
		printUsage(stderr)
		return exitUsage
	}

	log := logx.New(stderr)
	if _, err := app.Run(log, config.Root); err != nil {
		log.Errorf("%v", err)
		return exitFatal
	}
	return exitOK
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `sql2csv — конвертер INSERT-ов из SQL-файлов в CSV.

Рекурсивно обходит %s, из каждого .sql извлекает операторы
INSERT ... VALUES и сохраняет CSV рядом с исходным файлом. По итогам запуска
пишет %s\converted.txt.

Использование:
  sql2csv          запустить обработку
  sql2csv --help   показать эту справку

Коды выхода:
  0  корень существует и обработан
  1  корень отсутствует или не является директорией
  2  ошибка в аргументах командной строки

Правила обработки описаны в CONSTRAINTS_AND_POLICY.md.
`, config.Root, config.Root)
}
