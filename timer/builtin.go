package timer

import "sync"

var (
	builtinClock *Clock
	onec         sync.Once
	NewTimer     func(when int64, data any, receiver chan<- *Promise, batch chan<- []*Promise) (id int64, err error)
	CancelTimer  func(id int64) (ok bool, err error)
	UpdateTimer  func(id int64, when int64) (ok bool, err error)
)

func Start(quit <-chan struct{}) {
	onec.Do(func() {
		builtinClock = NewClock()
		builtinClock.Start(quit)
		NewTimer = builtinClock.NewTimer
		CancelTimer = builtinClock.CancelTimer
		UpdateTimer = builtinClock.UpdateTimer
	})
}
