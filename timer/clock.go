package timer

import (
	"errors"
	"fmt"
	"time"
)

const (
	_SIEXP            = 1
	_SI               = 10 * (1 << _SIEXP) // ms
	_TIME_WHEEL_LEVEL = 4
)

var (
	_LEVEL_DIVIS = [_TIME_WHEEL_LEVEL]int64{0, 10, 18, 24}
	_LEVEL_SLOTS = [_TIME_WHEEL_LEVEL]int64{1 << 10, 1 << 8, 1 << 6, 1 << 6}
	_LEVEL_MASKS = [_TIME_WHEEL_LEVEL]int64{}
	_LEVEL_TICKS = [_TIME_WHEEL_LEVEL]int64{}
)

func init() {
	for i := 0; i < _TIME_WHEEL_LEVEL; i++ {
		_LEVEL_MASKS[i] = _LEVEL_SLOTS[i] - 1
		if i > 0 {
			_LEVEL_TICKS[i] = _LEVEL_SLOTS[i] * _LEVEL_SLOTS[i-1]
		} else {
			_LEVEL_TICKS[i] = _LEVEL_SLOTS[i]
		}
	}
}

type Clock struct {
	genId    int64
	lastTime int64
	slot     [_TIME_WHEEL_LEVEL]int64
	tw       [_TIME_WHEEL_LEVEL]timeWheel
	taskch   chan func()
	closed   bool
	locs     map[int64]map[int64]*_Timer //记录位置
}

func NewClock() *Clock {
	c := &Clock{}
	c.taskch = make(chan func(), 10240)
	c.locs = make(map[int64]map[int64]*_Timer)
	for i := 0; i < _TIME_WHEEL_LEVEL; i++ {
		c.slot[i] = 0
		c.tw[i] = make(timeWheel, _LEVEL_SLOTS[i])
	}
	return c
}

type timeWheel []map[int64]*_Timer

func (c *Clock) Start(done <-chan struct{}) {
	go c.run(done)
}

func (c *Clock) NewTimer(when int64, data any, receiver chan<- *Promise, batch chan<- []*Promise) (id int64, err error) {
	t := &_Timer{
		when:     when,
		data:     data,
		receiver: receiver,
		batch:    batch,
	}
	err = c.pushTask(func() {
		c.genId++
		t.id = c.genId
		c.addTimer(t)
		id = t.id
	})
	return
}

func (c *Clock) CancelTimer(id int64) (ok bool, err error) {
	err = c.pushTask(func() {
		t := c.delTimer(id)
		ok = t != nil
	})
	return
}

func (c *Clock) UpdateTimer(id int64, when int64) (ok bool, err error) {
	err = c.pushTask(func() {
		ok = c.updateTimer(id, when)
	})
	return
}

func (c *Clock) addTimer(timer *_Timer) {
	var ticks, level, slot int64
	ticks = (timer.when - c.lastTime) / _SI
	if ticks <= 0 {
		ticks = 1
	}
	for level = 0; level < _TIME_WHEEL_LEVEL; level++ {
		if ticks < _LEVEL_TICKS[level] {
			slot = ((ticks >> _LEVEL_DIVIS[level]) + c.slot[level]) & _LEVEL_MASKS[level]
			break
		}
	}
	if level == _TIME_WHEEL_LEVEL {
		level--
		slot = _LEVEL_MASKS[level]
	}
	fmt.Printf("------------new timer [%d %d, %d], when=%d, lastTime=%d, ticks:%d\n", timer.id, level, slot, timer.when, c.lastTime, ticks)
	c.putTimer(level, slot, timer)
	return
}

func (c *Clock) putTimer(level, slot int64, timer *_Timer) {
	timerList := c.tw[level][slot]
	if timerList == nil {
		timerList = make(map[int64]*_Timer)
		c.tw[level][slot] = timerList
	}
	timerList[timer.id] = timer
	c.locs[timer.id] = timerList
}

func (c *Clock) delTimer(id int64) *_Timer {
	list := c.locs[id]
	t, ok := list[id]
	if ok {
		delete(list, id)
		delete(c.locs, id)
		return t
	}
	return nil
}

func (c *Clock) updateTimer(id int64, when int64) bool {
	t := c.delTimer(id)
	if t != nil {
		t.when = when
		c.addTimer(t)
		return true
	}
	return false
}

func (c *Clock) tick(nowMs, tkTime int64) {
	// 0层触发定时器
	c.slot[0] = (c.slot[0] + 1) & _LEVEL_MASKS[0]
	batchs := make(map[chan<- []*Promise][]*Promise)
	timers := c.tw[0][c.slot[0]]
	for _, timer := range timers {
		//删除
		delete(timers, timer.id)
		delete(c.locs, timer.id)
		//触发
		if timer.when <= nowMs {
			fmt.Printf("timer trigger:%d, when:%d, now:%d\n", timer.id, timer.when, nowMs)
			//传递到期定时器
			promise := &Promise{TimerId: timer.id, NowTs: nowMs, Data: timer.data}
			if timer.batch != nil {
				batchs[timer.batch] = append(batchs[timer.batch], promise)
			} else {
				select {
				case timer.receiver <- promise:
				default:
					c.putTimer(0, (c.slot[0]+1)&_LEVEL_MASKS[0], timer) //放入下一个tick
				}
			}
		} else {
			fmt.Printf("timer adjust:%+v, now:%d\n", timer, nowMs)
			// 重新加入时间轮, 一般是下一次tick
			c.addTimer(timer)
		}
	}
	for ch, promises := range batchs {
		ch <- promises
	}
	// 高层轮动
	var timer *_Timer
	var level, slot, ticks int64
	for i := 1; i < _TIME_WHEEL_LEVEL; i++ {
		if c.slot[i-1] != 0 {
			break
		}
		c.slot[i] = (c.slot[i] + 1) & _LEVEL_MASKS[i]
		// move timer
		timers := c.tw[i][c.slot[i]]
		for _, timer = range timers {
			//删除
			delete(timers, timer.id)
			delete(c.locs, timer.id)
			//加入到下一层
			ticks = (timer.when - tkTime + _SI - 1) / _SI //diff 向上取整
			if ticks <= 0 {
				ticks = 1
			}
			for level = 0; level < _TIME_WHEEL_LEVEL; level++ {
				if ticks < _LEVEL_TICKS[level] {
					slot = ((ticks >> _LEVEL_DIVIS[level]) + c.slot[level]) & _LEVEL_MASKS[level]
					break
				}
			}
			if level == _TIME_WHEEL_LEVEL {
				level--
				slot = _LEVEL_MASKS[level]
			}
			c.putTimer(level, slot, timer)
		}
	}
}

func (c *Clock) run(done <-chan struct{}) {
	tickTimeSpan := time.Millisecond * _SI
	tickTimer := time.NewTimer(tickTimeSpan)
	nowMs := time.Now().UnixMilli()
	c.lastTime = nowMs
	var tk int64
	for {
		select {
		case <-done:
			c.closed = true
			return
		case tm := <-tickTimer.C:
			nowMs = tm.UnixMilli()
			tk = c.lastTime + _SI
			c.lastTime += _SI * ((nowMs - c.lastTime) / _SI)
			for ; tk <= c.lastTime; tk += _SI {
				c.tick(nowMs, tk)
			}
			tickTimer.Reset(tickTimeSpan)
		case fn, ok := <-c.taskch:
			if ok {
				fn()
			}
		}
	}
}

func (c *Clock) pushTask(f func()) (err error) {
	errch := make(chan error, 1)
	ff := func() {
		if c.closed {
			errch <- errors.New("clock task chan closed")
		}
		defer close(errch)
		f()
	}
	select {
	case c.taskch <- ff:
	default:
		err = errors.New("pushTask falied timer task channel full")
		return
	}
	err = <-errch
	return
}
