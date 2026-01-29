package g

import (
	"context"
	"sync"

	"github.com/fixkme/gokit/clock"
)

type RoutineAgent struct {
	*Go
	closeSig    chan struct{}
	isClosed    bool
	mutex       sync.RWMutex
	timerCh     chan *clock.Promise
	timerCb     TimerCb
	beforeClose func()
}

type TimerCb func(tid int64, now int64, data any)

func NewRoutineAgent(taskChSize, timerChSize int) *RoutineAgent {
	a := &RoutineAgent{
		Go:       NewGoChan(taskChSize),
		closeSig: make(chan struct{}),
		timerCh:  make(chan *clock.Promise, timerChSize),
	}
	return a
}

func (a *RoutineAgent) Init(timerCb TimerCb, beforeClose func()) {
	a.timerCb = timerCb
	a.beforeClose = beforeClose
}

func (a *RoutineAgent) GetTimerReciver() chan<- *clock.Promise {
	return a.timerCh
}

func (a *RoutineAgent) Run() {
	defer a.onClose()

	for {
		select {
		case <-a.closeSig:
			return
		case cb := <-a.Go.ChanCb:
			a.Go.Exec(cb)
		case t := <-a.timerCh:
			a.timerCb(t.TimerId, t.NowTs, t.Data)
		}
	}
}

func (a *RoutineAgent) onClose() {
	if a.beforeClose != nil {
		a.beforeClose()
	}
	a.Go.Close()
	for cb := range a.Go.ChanCb {
		a.Go.Exec(cb)
	}
}

func (a *RoutineAgent) Close() {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.isClosed {
		return
	}

	a.isClosed = true
	close(a.closeSig)
}

func (a *RoutineAgent) SyncRunFunc(f func()) (err error) {
	a.mutex.RLock()
	if a.isClosed {
		err = ErrRoutineClosed
		a.mutex.RUnlock()
		return
	}

	errCh := a.Go.SubmitWithResult(f)
	a.mutex.RUnlock()
	err = <-errCh
	return
}

func (a *RoutineAgent) CtxRunFunc(ctx context.Context, f func()) (err error) {
	a.mutex.RLock()
	if a.isClosed {
		err = ErrRoutineClosed
		a.mutex.RUnlock()
		return
	}

	errCh := a.Go.SubmitWithResult(f)
	for {
		select {
		case <-ctx.Done():

			return ctx.Err()
		case err := <-errCh:
			return err
		}
	}
}

func (a *RoutineAgent) TryRunFunc(f func()) error {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	if a.isClosed {
		return ErrRoutineClosed
	}

	if !a.Go.TrySubmit(f) {
		return ErrGoChanFull
	}
	return nil
}

func (a *RoutineAgent) MustRunFunc(f func()) error {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	if a.isClosed {
		return ErrRoutineClosed
	}

	a.Go.MustSubmit(f)
	return nil
}
