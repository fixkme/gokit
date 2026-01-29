package g

import (
	"errors"

	"github.com/fixkme/gokit/mlog"
)

var (
	ErrGoChanFull    = errors.New("go chan is full")
	ErrRoutineClosed = errors.New("routine agent is closed")
)

type Go struct {
	ChanCb       chan func()
	panicHandler func(r any)
}

// New 新建Go
func NewGoChan(size int) *Go {
	if size < 1024 {
		size = 1024
	} else if size > 102400 {
		size = 102400
	}

	g := new(Go)
	g.ChanCb = make(chan func(), size)
	g.panicHandler = func(r any) {
		mlog.Errorf("go run panic: %v", r)
	}
	return g
}

func (g *Go) SetPanicHandler(f func(r any)) {
	if f != nil {
		g.panicHandler = f
	}
}

func (g *Go) Close() {
	close(g.ChanCb)
}

func (g *Go) SubmitWithResult(f func()) (errCh chan error) {
	errCh = make(chan error, 1)
	call := func() {
		defer close(errCh)
		f()
	}
	select {
	case g.ChanCb <- call:
	default:
		errCh <- ErrGoChanFull
		return
	}
	return
}

func (g *Go) TrySubmit(f func()) (ok bool) {
	select {
	case g.ChanCb <- f:
		return true
	default:
		return false
	}
}

func (g *Go) MustSubmit(f func()) {
	g.ChanCb <- f
}

func (g *Go) Exec(cb func()) {
	defer func() {
		if r := recover(); r != nil {
			g.panicHandler(r)
		}
	}()

	cb()
}
