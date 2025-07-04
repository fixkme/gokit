package mlog

import (
	"context"
	"fmt"
	"sync"
)

type Logger interface {
	Output(level string, s string)
	CanLog(level string) bool
}

var logger Logger

func SetLogger(l Logger) {
	logger = l
}

func UseDefaultLogger(ctx context.Context, wg *sync.WaitGroup, path string, logName string, level string, stdOut bool) error {
	l, err := newDefaultLogger(path, logName, level, stdOut)
	if err != nil {
		return err
	}
	l.Start(ctx, wg)
	SetLogger(l)
	return nil
}

func Debug(format string, a ...interface{}) {
	if logger == nil || !logger.CanLog("debug") {
		return
	}
	logger.Output("debug", fmt.Sprintf(format, a...))
}

func Info(format string, a ...interface{}) {
	if logger == nil || !logger.CanLog("info") {
		return
	}
	logger.Output("info", fmt.Sprintf(format, a...))
}

func Warn(format string, a ...interface{}) {
	if logger == nil || !logger.CanLog("warn") {
		return
	}
	logger.Output("warn", fmt.Sprintf(format, a...))
}

func Error(format string, a ...interface{}) {
	if logger == nil || !logger.CanLog("error") {
		return
	}
	logger.Output("error", fmt.Sprintf(format, a...))
}
