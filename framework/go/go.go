package g

import (
	"errors"

	"github.com/fixkme/gokit/mlog"
)

var (
	ErrGoChanFull    = errors.New("go chan is full")
	ErrRoutineClosed = errors.New("routine agent is closed")
	ErrGoChanClosed  = errors.New("go chan is closed")
)

type Go struct {
	ChanCb       chan func()
	panicHandler func(r any)
	closed       bool
}

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
	g.closed = true
	close(g.ChanCb)
}

func (g *Go) SubmitWithResult(f func()) (errCh chan error) {
	errCh = make(chan error, 1)
	call := func() {
		if g.closed {
			errCh <- ErrGoChanClosed
			return
		}
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
	call := func() {
		if g.closed {
			return
		}
		f()
	}
	select {
	case g.ChanCb <- call:
		return true
	default:
		return false
	}
}

func (g *Go) MustSubmit(f func()) {
	call := func() {
		if g.closed {
			return
		}
		f()
	}
	g.ChanCb <- call
}

func (g *Go) Exec(cb func()) {
	defer func() {
		if r := recover(); r != nil {
			g.panicHandler(r)
		}
	}()

	cb()
}
