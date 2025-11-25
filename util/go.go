package util

import (
	"fmt"
	"runtime"
	"strings"
)

func GoroutineID() int {
	var buf [64]byte
	n := runtime.Stack(buf[:], false) // 获取当前 goroutine 的调用栈
	idField := strings.Fields(strings.TrimPrefix(string(buf[:n]), "goroutine "))[0]
	id := 0
	fmt.Sscanf(idField, "%d", &id)
	return id
}
