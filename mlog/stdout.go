package mlog

import (
	"fmt"
	"log"
	"os"
)

type stdoutLogger struct {
	level Level
}

func newStdoutLogger(level Level) *stdoutLogger {
	l := &stdoutLogger{
		level: level,
	}
	log.SetFlags(log.Ldate | log.Lmicroseconds)
	return l
}

func (l *stdoutLogger) IsLevelEnabled(level Level) bool {
	return l.level >= level
}

func (l *stdoutLogger) Log(level Level, args ...interface{}) {
	if l.IsLevelEnabled(level) {
		log.Println(getLevelTag(level) + fmt.Sprint(args...))
	}
}

func (l *stdoutLogger) Logf(level Level, format string, args ...interface{}) {
	if l.IsLevelEnabled(level) {
		log.Println(getLevelTag(level) + fmt.Sprintf(format, args...))
	}
}

func (l *stdoutLogger) Trace(v ...any) {
	l.Log(TraceLevel, v...)
}

func (l *stdoutLogger) Tracef(format string, v ...any) {
	l.Logf(TraceLevel, format, v...)
}

func (l *stdoutLogger) Debug(v ...any) {
	l.Log(DebugLevel, v...)
}

func (l *stdoutLogger) Debugf(format string, v ...any) {
	l.Logf(DebugLevel, format, v...)
}

func (l *stdoutLogger) Info(v ...any) {
	l.Log(InfoLevel, v...)
}

func (l *stdoutLogger) Infof(format string, v ...any) {
	l.Logf(InfoLevel, format, v...)
}

func (l *stdoutLogger) Notice(v ...any) {
	l.Log(NoticeLevel, v...)
}

func (l *stdoutLogger) Noticef(format string, v ...any) {
	l.Logf(NoticeLevel, format, v...)
}

func (l *stdoutLogger) Warn(v ...any) {
	l.Log(WarnLevel, v...)
}

func (l *stdoutLogger) Warnf(format string, v ...any) {
	l.Logf(WarnLevel, format, v...)
}

func (l *stdoutLogger) Error(v ...any) {
	l.Log(ErrorLevel, v...)
}

func (l *stdoutLogger) Errorf(format string, v ...any) {
	l.Logf(ErrorLevel, format, v...)
}

func (l *stdoutLogger) Fatal(v ...any) {
	l.Log(FatalLevel, v...)
	os.Exit(1)
}

func (l *stdoutLogger) Fatalf(format string, v ...any) {
	l.Logf(FatalLevel, format, v...)
	os.Exit(1)
}
