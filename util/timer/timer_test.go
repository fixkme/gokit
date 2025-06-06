package timer

import (
	"fmt"
	"testing"
	"time"
)

func TestTimer(t *testing.T) {
	done := make(chan struct{}, 1)
	Start(done)
	receiver := make(chan *Promise, 1024)
	tickerSpan := time.Second * 15
	ti := time.NewTimer(time.Second)
	go func() {
		for {
			select {
			case tm := <-ti.C:
				var delay int64 = 12000
				id, err := NewTimer(tm.UnixMilli()+delay, delay, receiver, nil)
				fmt.Printf("create timer:%d, %v, now:%d\n", id, err, tm.UnixMilli())
				ti.Reset(tickerSpan)
			case v, ok := <-receiver:
				if ok {
					fmt.Printf("receive timer:%+v\n", v)
				}
			default:
			}
		}
	}()

	select {}
}
