package app

import (
	"sql2csv/internal/convert"
	"sql2csv/internal/csvout"
	"sql2csv/internal/logx"
	"sql2csv/internal/scan"
	"sql2csv/internal/xlsconv"
)

func outcomeFromConvert(fr convert.Result) fileOutcome {
	return fileOutcome{
		openErr:  fr.OpenErr,
		created:  fr.Created,
		skipped:  fr.Skipped,
		csv:      fr.CSV,
		failed:   fr.Failed,
		piiSkip:  fr.PIISkip,
		unitFail: fr.UnitFail,
	}
}

func convertExcel(log *logx.Logger, reg *csvout.Registry, file scan.SQLFile) (out fileOutcome) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Errorf("%s: сбой обработки (%v), файл пропущен", file.Path, rec)
			out = fileOutcome{skipped: 1, failed: true}
		}
	}()
	xr := xlsconv.File(reg, file.Path)
	return fileOutcome{
		openErr:     xr.OpenErr,
		writeErr:    xr.WriteErr,
		csv:         xr.CSV,
		skipTooMany: xr.SkipTooMany,
	}
}

func convertDirFiles(
	acc *accumulator,
	log *logx.Logger,
	reg *csvout.Registry,
	submit func(func()),
	sqls, excels []scan.SQLFile,
) {
	for _, file := range sqls {
		acc.start(file)
		out := outcomeFromConvert(convert.Schedule(log, reg, file, submit))
		acc.record(file, out)
	}
	for _, file := range excels {
		acc.start(file)
		out := convertExcel(log, reg, file)
		acc.record(file, out)
	}
}
