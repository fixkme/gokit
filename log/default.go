package log

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
	level  int
	stdOut bool
}

func newDefaultLogger(logpath, logName string, level string, stdOut bool) (*loggerImp, error) {
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

	mlog := &loggerImp{
		path:   logpath,
		ll:     fileLogger,
		file:   logfile,
		buff:   make(chan string, 0x10000),
		level:  getLevel(level),
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
				return
			case str := <-me.buff:
				if me.stdOut {
					fmt.Println(str)
				}
				me.ll.Println(str)
			case <-timer.C:

				size, err := getFileSize(me.file.Name())
				if err != nil {
					log.Printf("getFileSize error %v\n", err)
					continue
				}
				if size > maxSize {
					file, err := rotateLogFile(me.file.Name())
					if err != nil {
						log.Printf("rotateLogFile error %v\n", err)
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

func (me *loggerImp) logFormat(prefix string, format string, v ...interface{}) {
	me.buff <- fmt.Sprintf(prefix+format, v...)
}

func (me *loggerImp) Output(level, s string) {
	me.buff <- fmt.Sprintf("[%s] ", level) + s
}

func (me *loggerImp) CanLog(level string) bool {
	return me.level <= getLevel(level)
}

func getLevel(level string) (lv int) {
	switch level {
	case "trace":
		lv = 0
	case "debug":
		lv = 1
	case "info":
		lv = 2
	case "warn":
		lv = 4
	case "error":
		lv = 5
	case "fatal":
		lv = 6
	default:
		lv = 0
	}
	return lv
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
