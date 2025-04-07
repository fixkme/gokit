package log

import "fmt"

type Logger interface {
	Output(level string, s string)
}

var logger Logger

func SetLogger(l Logger) {
	logger = l
}

func Debug(format string, a ...interface{}) {
	if logger == nil {
		return
	}
	logger.Output("debug", fmt.Sprintf(format, a...))
}

func Info(format string, a ...interface{}) {
	if logger == nil {
		return
	}
	logger.Output("info", fmt.Sprintf(format, a...))
}

func Warn(format string, a ...interface{}) {
	if logger == nil {
		return
	}
	logger.Output("warn", fmt.Sprintf(format, a...))
}

func Error(format string, a ...interface{}) {
	if logger == nil {
		return
	}
	logger.Output("error", fmt.Sprintf(format, a...))
}
