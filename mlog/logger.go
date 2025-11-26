package mlog

import (
	"context"
	"sync"
)

type Logger interface {
	Logf(level Level, format string, args ...interface{})
	IsLevelEnabled(level Level) bool
}

var logger Logger

func SetLogger(l Logger) {
	logger = l
}

func UseDefaultLogger(ctx context.Context, wg *sync.WaitGroup, path string, logName string, level Level, stdOut bool) error {
	l, err := newDefaultLogger(path, logName, level, stdOut)
	if err != nil {
		return err
	}
	l.Start(ctx, wg)
	SetLogger(l)
	return nil
}

type Level uint32

const (
	PanicLevel Level = iota
	FatalLevel
	ErrorLevel
	WarnLevel
	InfoLevel
	DebugLevel
	TraceLevel
)

func Debug(format string, a ...interface{}) {
	if logger == nil {
		return
	}
	logger.Logf(DebugLevel, format, a...)
}

func Info(format string, a ...interface{}) {
	if logger == nil {
		return
	}
	logger.Logf(InfoLevel, format, a...)
}

func Warn(format string, a ...interface{}) {
	if logger == nil {
		return
	}
	logger.Logf(WarnLevel, format, a...)
}

func Error(format string, a ...interface{}) {
	if logger == nil {
		return
	}
	logger.Logf(ErrorLevel, format, a...)
}
