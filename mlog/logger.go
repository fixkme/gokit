package mlog

import (
	"context"
	"sync"
)

type Logger interface {
	Trace(v ...any)
	Debug(v ...any)
	Info(v ...any)
	Notice(v ...any)
	Warn(v ...any)
	Error(v ...any)
	Fatal(v ...any)

	Tracef(format string, v ...any)
	Debugf(format string, v ...any)
	Infof(format string, v ...any)
	Noticef(format string, v ...any)
	Warnf(format string, v ...any)
	Errorf(format string, v ...any)
	Fatalf(format string, v ...any)
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

func UseStdLogger(level Level) error {
	l := newStdoutLogger(level)
	SetLogger(l)
	return nil
}

type Level uint32

const (
	FatalLevel Level = iota
	ErrorLevel
	WarnLevel
	NoticeLevel
	InfoLevel
	DebugLevel
	TraceLevel
)

func Trace(a ...any) {
	if logger == nil {
		return
	}
	logger.Trace(a...)
}

func Tracef(format string, a ...any) {
	if logger == nil {
		return
	}
	logger.Tracef(format, a...)
}

func Debug(a ...any) {
	if logger == nil {
		return
	}
	logger.Debug(a...)
}

func Debugf(format string, a ...any) {
	if logger == nil {
		return
	}
	logger.Debugf(format, a...)
}

func Info(a ...any) {
	if logger == nil {
		return
	}
	logger.Info(a...)
}

func Infof(format string, a ...any) {
	if logger == nil {
		return
	}
	logger.Infof(format, a...)
}

func Notice(a ...any) {
	if logger == nil {
		return
	}
	logger.Notice(a...)
}

func Noticef(format string, a ...any) {
	if logger == nil {
		return
	}
	logger.Noticef(format, a...)
}

func Warn(a ...any) {
	if logger == nil {
		return
	}
	logger.Warn(a...)
}

func Warnf(format string, a ...any) {
	if logger == nil {
		return
	}
	logger.Warnf(format, a...)
}

func Error(a ...any) {
	if logger == nil {
		return
	}
	logger.Error(a...)
}

func Errorf(format string, a ...any) {
	if logger == nil {
		return
	}
	logger.Errorf(format, a...)
}

func Fatal(a ...any) {
	if logger == nil {
		return
	}
	logger.Fatal(a...)
}

func Fatalf(format string, a ...any) {
	if logger == nil {
		return
	}
	logger.Fatalf(format, a...)
}
