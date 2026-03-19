package staticlist

import (
	"fmt"
	"testing"
)

func TestQueue(t *testing.T) {
	type Data struct {
		A int
	}
	q := NewQueue[*Data](4)

	for i := 0; i < 6; i++ {
		if q.Push(&Data{A: i}) == nil {
			fmt.Println("push full", q.Len())
		}
	}

	q.Pop()
	q.Range(func(a **Data) bool {
		v := *a
		fmt.Printf("%d, ", v.A)
		return true
	})
	fmt.Println()

	for i := 0; i < 6; i++ {
		v := q.Pop()
		if v == nil {
			fmt.Println("pop empty")
			break
		}
		fmt.Printf("%d, ", v.A)
	}
	fmt.Println(q.IsEmpty(), q.Len(), q.pool.free)

	for i := 0; i < 7; i++ {
		if q.Push(&Data{A: i}) == nil {
			fmt.Println("push full", q.Len())
		}
	}
	q.Range(func(a **Data) bool {
		v := *a
		fmt.Printf("%d, ", v.A)
		return true
	})
	fmt.Println()

	fmt.Println(q.IsEmpty(), q.Len(), q.pool.free)
}

func TestLRUCache(t *testing.T) {
	cache := NewLRUCache(2)
	cache.Put(1, 1)
	cache.Put(2, 2)
	fmt.Println(cache.Get(1))
	cache.Put(3, 3) // 该操作会使得关键字 2 作废
	fmt.Println(cache.Get(2))
}

// 以LRU测试队列
type LRUCache struct {
	queue    *Queue[KV]
	keys     map[int]*QNode[KV]
	capacity int
}
type KV struct {
	Key   int
	Value int
}

func NewLRUCache(capacity int) LRUCache {
	q := NewQueue[KV](capacity)
	return LRUCache{
		queue:    q,
		keys:     make(map[int]*QNode[KV]),
		capacity: capacity,
	}
}

func (this *LRUCache) Get(key int) int {
	qNode, ok := this.keys[key]
	if !ok {
		return -1
	}
	this.queue.MoveToBack(qNode)
	return qNode.Value.Value
}

func (this *LRUCache) Put(key int, value int) {
	qNode, ok := this.keys[key]
	if ok {
		qNode.Value.Value = value
		this.queue.MoveToBack(qNode)
	} else {
		var kv KV
		if this.queue.len >= this.capacity {
			kv = this.queue.Pop()
			delete(this.keys, kv.Key)
		}
		kv.Key, kv.Value = key, value
		this.keys[key] = this.queue.Push(kv)
	}
}
