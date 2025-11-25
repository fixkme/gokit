package clock

type Promise struct {
	TimerId int64
	NowTs   int64 // 当前时间戳 毫秒
	Data    any
}

// 定时器实现
// receiver和batch 两种投递方式是互斥的，选择其中一个
// 如果batch可用的话，默认优先使用batch，否则使用receiver
type _Timer struct {
	id         int64             // ID
	when       int64             // 到期时间戳 毫秒
	data       any               // 数据
	receiver   chan<- *Promise   // 处理器
	batch      chan<- []*Promise // 批量处理器
	prev, next *_Timer           // 双向链表
}

func (t *_Timer) removeFromList() bool {
	if t.prev == nil || t.next == nil {
		return false
	}
	t.prev.next = t.next
	t.next.prev = t.prev
	t.prev = nil
	t.next = nil
	return true
}

type _List struct {
	root *_Timer //哨兵
}

func newTimerList() *_List {
	l := new(_List)
	l.root = new(_Timer)
	l.root.prev = l.root
	l.root.next = l.root
	return l
}

func (l *_List) PushBack(t *_Timer) {
	tail := l.root.prev
	tail.next = t
	t.prev = tail
	t.next = l.root
	l.root.prev = t
}

// 有序插入
func (l *_List) InsertSorted(t *_Timer) {
	if t == nil {
		return
	}
	if l.IsEmpty() {
		l.PushBack(t)
		return
	}
	// 查找插入位置
	current := l.root.next
	for current != l.root && current.when <= t.when {
		current = current.next
	}
	// 插入
	t.prev = current.prev
	t.next = current
	current.prev.next = t
	current.prev = t
}

// Remove 从链表中移除指定节点
func (l *_List) Remove(t *_Timer) bool {
	if t == l.root {
		return false
	}
	t.prev.next = t.next
	t.next.prev = t.prev
	t.prev = nil
	t.next = nil
	return true
}

// IsEmpty 检查链表是否为空
func (l *_List) IsEmpty() bool {
	return l.root.next == l.root
}

// SafeClear 安全的清空链表
func (l *_List) SafeClear() {
	for !l.IsEmpty() {
		t := l.root.next
		l.Remove(t)
	}
}

// Clear 快速清空，让gc检查node引用和回收内存
func (l *_List) Clear() {
	l.root.prev = l.root
	l.root.next = l.root
}

// Range 遍历链表中的节点, fn不能修改链表
func (l *_List) Range(call string, fn func(t *_Timer) bool) {
	for t := l.root.next; t != l.root; t = t.next {
		if !fn(t) {
			break
		}
	}
}

// PopRange 删除并遍历链表中的节点
func (l *_List) PopRange(fn func(t *_Timer) bool) {
	for !l.IsEmpty() {
		t := l.root.next
		l.Remove(t)
		if !fn(t) {
			break
		}
	}
}
