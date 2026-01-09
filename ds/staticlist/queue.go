package staticlist

import "unsafe"

// 双向链表实现队列，支持删除元素O(1)
type Queue[T any] struct {
	pool *StaticList[QNode[T]]
	root *QNode[T] //哨兵
	len  int
	cap  int
}

type QNode[T any] struct {
	Value      T
	prev, next *QNode[T]
}

func NewQueue[T any](cap int) *Queue[T] {
	q := &Queue[T]{
		pool: NewStaticList[QNode[T]](1 + cap),
		root: nil,
		len:  0,
		cap:  cap,
	}
	q.init()
	return q
}

func (q *Queue[T]) init() {
	p := q.pool.Malloc()
	q.root = q.pool.GetDataPointer(p)
	q.root.prev = q.root
	q.root.next = q.root
}

func (q *Queue[T]) Push(data T) *QNode[T] {
	p := q.pool.Malloc()
	if p == Null {
		return nil //full
	}
	q.len++
	slot := q.pool.GetNode(p)
	slot.Next = p //分配好后，slot.Next=nil，完全可以用来存Data所在index
	node := &slot.Data
	node.Value = data

	tail := q.root.prev
	tail.next = node
	node.prev = tail
	node.next = q.root
	q.root.prev = node
	return node
}

func (q *Queue[T]) Pop() (data T, ok bool) {
	if q.IsEmpty() {
		return //empty
	}
	node := q.root.next
	data = node.Value
	ok = q.Remove(node)
	return
}

func (q *Queue[T]) Remove(node *QNode[T]) bool {
	if node.prev == nil || node.next == nil {
		return false
	}

	pdata := unsafe.Pointer(node)
	slot := (*Node[QNode[T]])(pdata)
	p := slot.Next // 在Push的时候设置的index

	// p := q.pool.GetDataIndex(node)
	// if p < 0 {
	// 	return false
	// }

	node.prev.next = node.next
	node.next.prev = node.prev

	// 在Free时会清除，这里不用显式清除prev，next
	// node.prev = nil
	// node.next = nil

	q.pool.Free(p)
	q.len--
	return true
}

func (q *Queue[T]) Clear() {
	q.PopRange(func(*T) bool { return true })
}

func (q *Queue[T]) First() *T {
	if q.IsEmpty() {
		return nil
	}
	node := q.root.next
	return &node.Value
}

func (q *Queue[T]) IsEmpty() bool {
	return q.root.next == q.root
}

func (q *Queue[T]) IsFull() bool {
	return q.len == q.cap
}

func (q *Queue[T]) Len() int {
	return q.len
}

// Range 遍历链表中的节点, fn不能修改链表
func (q *Queue[T]) Range(fn func(*T) bool) {
	for node := q.root.next; node != q.root; node = node.next {
		if !fn(&node.Value) {
			break
		}
	}
}

// PopRange 删除并遍历链表中的节点
func (q *Queue[T]) PopRange(fn func(*T) bool) {
	for q.root.next != q.root {
		node := q.root.next
		data := node.Value
		if !fn(&data) {
			break
		}
		q.Remove(node)
	}
}
