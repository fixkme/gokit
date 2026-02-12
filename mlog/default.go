package mlog

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type loggerImp struct {
	path   string
	file   *os.File
	ll     *log.Logger
	buff   chan string
	level  Level
	stdOut bool
}

func newDefaultLogger(logpath, logName string, level Level, stdOut bool) (*loggerImp, error) {
	// 默认使用当前路径
	if len(logpath) == 0 {
		logpath = "."
	}
	logname := filepath.Join(logpath, genLogName(logName))
	logfile, err := openFile(logname)
	if err != nil {
		return nil, err
	}
	fileLogger := log.New(logfile, "", log.Ldate|log.Lmicroseconds)
	if stdOut {
		log.SetFlags(log.Ldate | log.Lmicroseconds)
	}
	mlog := &loggerImp{
		path:   logpath,
		ll:     fileLogger,
		file:   logfile,
		buff:   make(chan string, 0x10000),
		level:  level,
		stdOut: stdOut,
	}
	return mlog, nil
}

func (me *loggerImp) Start(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("log recover error %v\n", r)
			}
			me.file.Close()
			wg.Done()
		}()

		maxSize := int64(100 * 1024 * 1024) // 100 MB
		timerInterval := 30 * time.Second
		timer := time.NewTimer(timerInterval)

		for {
			select {
			case <-ctx.Done():
				for {
					select {
					case str := <-me.buff:
						if me.stdOut {
							log.Println(str)
						}
						me.ll.Println(str)
					default:
						return
					}
				}
			case str := <-me.buff:
				if me.stdOut {
					log.Println(str)
				}
				me.ll.Println(str)
			case <-timer.C:
				size, err := getFileSize(me.file.Name())
				if err != nil {
					log.Println("mlog getFileSize error", err)
					continue
				}
				if size > maxSize {
					file, err := rotateLogFile(me.file.Name())
					if err != nil {
						log.Println("mlog rotateLogFile error", err)
						continue
					}
					me.ll.SetOutput(file)
					me.file.Close()
					me.file = file
				}

				timer.Reset(timerInterval)
			}
		}

	}()
}

func (me *loggerImp) Trace(args ...interface{}) {
	if me.IsLevelEnabled(TraceLevel) {
		me.buff <- (getLevelTag(TraceLevel) + fmt.Sprint(args...))
	}
}
func (me *loggerImp) Tracef(format string, args ...interface{}) {
	if me.IsLevelEnabled(TraceLevel) {
		me.buff <- (getLevelTag(TraceLevel) + fmt.Sprintf(format, args...))
	}
}

func (me *loggerImp) Debug(args ...interface{}) {
	if me.IsLevelEnabled(DebugLevel) {
		me.buff <- (getLevelTag(DebugLevel) + fmt.Sprint(args...))
	}
}

func (me *loggerImp) Debugf(format string, args ...interface{}) {
	if me.IsLevelEnabled(DebugLevel) {
		me.buff <- (getLevelTag(DebugLevel) + fmt.Sprintf(format, args...))
	}
}

func (me *loggerImp) Info(args ...interface{}) {
	if me.IsLevelEnabled(InfoLevel) {
		me.buff <- (getLevelTag(InfoLevel) + fmt.Sprint(args...))
	}
}

func (me *loggerImp) Infof(format string, args ...interface{}) {
	if me.IsLevelEnabled(InfoLevel) {
		me.buff <- (getLevelTag(InfoLevel) + fmt.Sprintf(format, args...))
	}
}

func (me *loggerImp) Notice(args ...interface{}) {
	if me.IsLevelEnabled(NoticeLevel) {
		me.buff <- (getLevelTag(NoticeLevel) + fmt.Sprint(args...))
	}
}

func (me *loggerImp) Noticef(format string, args ...interface{}) {
	if me.IsLevelEnabled(NoticeLevel) {
		me.buff <- (getLevelTag(NoticeLevel) + fmt.Sprintf(format, args...))
	}
}

func (me *loggerImp) Warn(args ...interface{}) {
	if me.IsLevelEnabled(WarnLevel) {
		me.buff <- (getLevelTag(WarnLevel) + fmt.Sprint(args...))
	}
}

func (me *loggerImp) Warnf(format string, args ...interface{}) {
	if me.IsLevelEnabled(WarnLevel) {
		me.buff <- (getLevelTag(WarnLevel) + fmt.Sprintf(format, args...))
	}
}

func (me *loggerImp) Error(args ...interface{}) {
	if me.IsLevelEnabled(ErrorLevel) {
		me.buff <- (getLevelTag(ErrorLevel) + fmt.Sprint(args...))
	}
}

func (me *loggerImp) Errorf(format string, args ...interface{}) {
	if me.IsLevelEnabled(ErrorLevel) {
		me.buff <- (getLevelTag(ErrorLevel) + fmt.Sprintf(format, args...))
	}
}

func (me *loggerImp) Fatal(args ...interface{}) {
	if me.IsLevelEnabled(FatalLevel) {
		me.buff <- (getLevelTag(FatalLevel) + fmt.Sprint(args...))
		time.Sleep(time.Second)
		os.Exit(1)
	}
}

func (me *loggerImp) Fatalf(format string, args ...interface{}) {
	if me.IsLevelEnabled(FatalLevel) {
		me.buff <- (getLevelTag(FatalLevel) + fmt.Sprintf(format, args...))
		time.Sleep(time.Second)
		os.Exit(1)
	}
}

func (me *loggerImp) Logf(level Level, format string, args ...interface{}) {
	if me.IsLevelEnabled(level) {
		if len(format) == 0 {
			me.buff <- getLevelTag(level) + fmt.Sprint(args...)
		} else {
			me.buff <- getLevelTag(level) + fmt.Sprintf(format, args...)
		}
	}
}

func (me *loggerImp) IsLevelEnabled(level Level) bool {
	return me.level >= level
}

func getLevelTag(level Level) string {
	switch level {
	case FatalLevel:
		return "[fatal] "
	case ErrorLevel:
		return "[error] "
	case WarnLevel:
		return "[warn] "
	case NoticeLevel:
		return "[notice] "
	case InfoLevel:
		return "[info] "
	case DebugLevel:
		return "[debug] "
	case TraceLevel:
		return "[trace] "
	}
	return ""
}

func genLogName(logName string) string {
	if logName == "" {
		logName = "mlog"
	}
	return logName + ".log"
}

const (
	defaultDirMode  os.FileMode = 0755
	defaultFileMode os.FileMode = 0644
	defaultFileFlag int         = os.O_APPEND | os.O_CREATE | os.O_WRONLY
)

func openFile(fullpath string) (*os.File, error) {
	fullpath = strings.ReplaceAll(fullpath, "\\", "/")
	dir := filepath.Dir(fullpath)
	if _, err := os.Stat(dir); err != nil && !os.IsExist(err) {
		if err = os.MkdirAll(dir, defaultDirMode); err != nil {
			return nil, err
		}
	}

	return os.OpenFile(fullpath, defaultFileFlag, defaultFileMode)
}

func getFileSize(filePath string) (int64, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return 0, err
	}
	return fileInfo.Size(), nil
}

func rotateLogFile(filePath string) (*os.File, error) {
	timestamp := time.Now().Format("20060102_150405")
	newFilePath := fmt.Sprintf("%s.%s", filePath, timestamp)

	err := os.Rename(filePath, newFilePath)
	if err != nil {
		return nil, err
	}
	return os.Create(filePath)
}
