package str

import (
	"unsafe"
)

func StrAsBytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

func BytesAsStr(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}
