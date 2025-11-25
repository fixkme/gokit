package errs

import (
	"fmt"
	"strings"
)

type CodeError interface {
	error
	Code() int32
	Print(extras ...string) CodeError
	Printf(format string, args ...any) CodeError
	Is(error) bool
}

func CreateCodeError(code int32, desc string) CodeError {
	return &codeError{
		Errno: code, //  错误码数字
		Desc:  desc, //  错误描述字符串, 如：CURRENCY_NOT_ENOUGH、UNKNOWN_ERROR, max count limit
	}
}

func WrapError(err error) CodeError {
	x, ok := err.(*codeError)
	if ok {
		return x
	}
	return CreateCodeError(ErrCode_Unknown, err.Error())
}

type codeError struct {
	Errno int32
	Desc  string
}

func (e *codeError) Code() int32 {
	return e.Errno
}

func (e *codeError) Error() string {
	return e.Desc
}

func (e *codeError) String() string {
	return fmt.Sprintf("errno: %d, desc: %s", e.Errno, e.Desc)
}

func (e *codeError) Print(extras ...string) CodeError {
	if len(extras) == 0 {
		return e
	}
	ns := len(e.Desc) + len(extras)
	for _, extra := range extras {
		ns += len(extra)
	}
	builder := strings.Builder{}
	builder.Grow(ns)
	builder.WriteString(e.Desc)
	for _, extra := range extras {
		builder.WriteByte(',')
		builder.WriteString(extra)
	}
	er := &codeError{
		Errno: e.Errno,
		Desc:  builder.String(),
	}
	return er
}

func (e *codeError) Printf(format string, args ...any) CodeError {
	if len(format) == 0 {
		return e
	}
	desc := fmt.Sprintf(e.Desc+","+format, args...)
	er := &codeError{
		Errno: e.Errno,
		Desc:  desc,
	}
	return er
}

func (e *codeError) Is(target error) bool {
	if x, ok := target.(*codeError); ok {
		return x.Errno == e.Errno
	}
	return false
}
