package util

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultDirMode  os.FileMode = 0755
	defaultFileMode os.FileMode = 0644
	defaultFileFlag int         = os.O_APPEND | os.O_CREATE | os.O_WRONLY
)

func OpenFile(fullpath string) (*os.File, error) {
	fullpath = strings.ReplaceAll(fullpath, "\\", "/")
	dir := filepath.Dir(fullpath)
	if _, err := os.Stat(dir); err != nil && !os.IsExist(err) {
		if err = os.MkdirAll(dir, defaultDirMode); err != nil {
			return nil, err
		}
	}

	return os.OpenFile(fullpath, defaultFileFlag, defaultFileMode)
}

func GetFileSize(filePath string) (int64, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return 0, err
	}
	return fileInfo.Size(), nil
}

func RotateLogFile(filePath string) (*os.File, error) {
	timestamp := time.Now().Format("20060102_150405")
	newFilePath := fmt.Sprintf("%s.%s", filePath, timestamp)

	err := os.Rename(filePath, newFilePath)
	if err != nil {
		return nil, err
	}
	return os.Create(filePath)
}
