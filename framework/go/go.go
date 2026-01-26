package g

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/fixkme/gokit/mlog"
)

const DefaultChanSize = 10240

var (
	ErrGoClosed   = errors.New("go is closed")
	ErrGoChanFull = errors.New("go chan is full")
)

type Go struct {
	ChanCb       chan func()
	panicHandler func(r any)
	isClosed     atomic.Bool
}

// New 新建Go
func New(size int) *Go {
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
	g.isClosed.Store(true)
	close(g.ChanCb)
}

func (g *Go) IsClosed() bool {
	return g.isClosed.Load()
}

func (g *Go) Discard() {
	for cb := range g.ChanCb {
		cb()
	}
}

func (g *Go) SyncRun(f func()) (err error) {
	errCh := make(chan error, 1)
	call := func() {
		if g.isClosed.Load() {
			errCh <- ErrGoClosed
			return
		}
		defer close(errCh)
		f()
	}
	select {
	case g.ChanCb <- call:
	default:
		return ErrGoChanFull
	}
	return <-errCh
}

func (g *Go) CtxRun(ctx context.Context, f func()) error {
	errCh := make(chan error, 1)
	call := func() {
		if g.isClosed.Load() {
			errCh <- ErrGoClosed
			return
		}
		defer close(errCh)
		f()
	}
	select {
	case g.ChanCb <- call:
	default:
		return ErrGoChanFull
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			return err
		}
	}
}

func (g *Go) TrySubmit(f func()) bool {
	call := func() {
		if g.isClosed.Load() {
			return
		}
		f()
	}

	var ok bool
	select {
	case g.ChanCb <- call:
		ok = true
	default:
	}
	return ok
}

func (g *Go) MustSubmit(f func()) {
	call := func() {
		if g.isClosed.Load() {
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
