package clock

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/fixkme/gokit/mlog"
	"github.com/fixkme/gokit/util"
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
			_LEVEL_TICKS[i] = _LEVEL_SLOTS[i] * _LEVEL_TICKS[i-1]
		} else {
			_LEVEL_TICKS[i] = _LEVEL_SLOTS[i]
		}
	}
}

type Clock struct {
	genId    int64
	lastTime int64
	slot     [_TIME_WHEEL_LEVEL]int64 //每层的指针位置
	tw       [_TIME_WHEEL_LEVEL]timeWheel
	taskch   chan func()
	closed   atomic.Bool
	locs     map[int64]*_Timer //记录位置
}

func NewClock() *Clock {
	c := &Clock{}
	c.taskch = make(chan func(), 10240)
	c.locs = make(map[int64]*_Timer)
	for i := 0; i < _TIME_WHEEL_LEVEL; i++ {
		c.slot[i] = 0
		c.tw[i] = make(timeWheel, _LEVEL_SLOTS[i])
	}
	c.closed.Store(false)
	return c
}

type timeWheel []*_List

func (c *Clock) Start(quit <-chan struct{}) {
	go c.run(quit)
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
	ticks = (timer.when - c.lastTime + _SI - 1) / _SI //diff 向上取整
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
	mlog.Debug("------------add timer [%d, %d, %d], when=%d, lastTime=%d, ticks:%d\n", timer.id, level, slot, timer.when, c.lastTime, ticks)
	c.putTimer(level, slot, timer)
}

func (c *Clock) putTimer(level, slot int64, timer *_Timer) {
	timerList := c.tw[level][slot]
	if timerList == nil {
		timerList = newTimerList()
		c.tw[level][slot] = timerList
	}
	timerList.PushBack(timer)
	c.locs[timer.id] = timer
}

func (c *Clock) delTimer(id int64) *_Timer {
	timer, ok := c.locs[id]
	if ok {
		timer.removeFromList()
		delete(c.locs, id)
		return timer
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

func (c *Clock) trigger(nowMs int64) {
	batchs := make(map[chan<- []*Promise][]*Promise)
	timerList := c.tw[0][c.slot[0]]
	if timerList == nil {
		return
	}
	timerList.PopRange(func(timer *_Timer) bool {
		//删除
		delete(c.locs, timer.id)
		//触发
		if timer.when <= nowMs {
			mlog.Debug("------------timer trigger id:%d, when:%d, now:%d\n", timer.id, timer.when, nowMs)
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
			mlog.Debug("timer adjust id:%d, when:%d, now:%d, now slot:%d\n", timer.id, timer.when, nowMs, c.slot[0])
			// 重新加入时间轮, 一般是下一次tick
			c.addTimer(timer)
		}
		return true
	})

	for ch, promises := range batchs {
		ch <- promises
	}
}

func (c *Clock) tick(nowMs, tkTime int64) {
	c.slot[0] = (c.slot[0] + 1) & _LEVEL_MASKS[0]
	// 0层触发定时器
	c.trigger(nowMs)
	// 高层轮动
	var level, slot, ticks int64
	for i := 1; i < _TIME_WHEEL_LEVEL; i++ {
		if c.slot[i-1] != 0 {
			break
		}
		c.slot[i] = (c.slot[i] + 1) & _LEVEL_MASKS[i]
		// move timer
		timerList := c.tw[i][c.slot[i]]
		if timerList == nil {
			continue
		}
		timerList.PopRange(func(timer *_Timer) bool {
			//删除
			// delete(c.locs, timer.id) 后续还会加入
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
			return true
		})
	}
}

func (c *Clock) run(quit <-chan struct{}) {
	tickTimeSpan := time.Millisecond * _SI
	tickTimer := time.NewTimer(tickTimeSpan)
	nowMs := util.NowMs()
	c.lastTime = nowMs
	var tk int64
	for {
		select {
		case <-quit:
			c.closed.Store(true)
			close(c.taskch)
			return
		case <-tickTimer.C:
			nowMs = util.NowMs() // 可以修改时间
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
	if c.closed.Load() {
		return errors.New("clock is closed")
	}
	done := make(chan struct{}, 1)
	ff := func() {
		defer close(done)
		f()
	}
	select {
	case c.taskch <- ff:
	default:
		err = errors.New("pushTask falied timer task channel full")
		return
	}
	<-done
	return
}
