package skiplist

import (
	"fmt"
	"math/rand"
	"testing"
)

func testTT[T ElemType[T]](size int, newData func(int) T, modifyFn func(T) T) {
	sl := NewSkipList[T]()
	pool := []int{}
	for i := 1; i <= size; i++ {
		pool = append(pool, i)
	}
	rand.Shuffle(len(pool), func(i, j int) {
		pool[i], pool[j] = pool[j], pool[i]
	})
	for _, i := range pool {
		d := newData(i)
		rk := sl.Insert(d)
		fmt.Printf("insert %v,%d\n", d, rk)
	}
	printFn := func() {
		sl.Foreach(func(d T, rank int) bool {
			fmt.Printf("%v,%d; ", d, rank)
			return true
		})
		fmt.Printf("\nsl %d,%d\n", sl.level, sl.length)
	}
	printFn()
	// 根据rank查询数据
	rk := rand.Intn(size) + 1
	//rk = rk - size
	e, ok := sl.SearchByRank(rk)
	fmt.Printf("%v, 第%drank的元素是%v, rank:%d\n", ok, rk, e, sl.GetRank(e))
	// 更新数据
	if ok {
		newd := modifyFn(e)
		newrk := sl.Update(e, newd)
		if newrk == 0 {
			newrk = rk
		}
		fmt.Printf("olddata:%v 更新后 data:%v, newrank:%d \n", e, newd, newrk)
	}
	printFn()
	// 范围查询
	vv := sl.SearchByRankRange(rk, rk+rand.Intn(max(sl.Len()-rk, 1)))
	for i, v := range vv {
		fmt.Printf("范围查询 rank:%d=>%v\n", rk+i, v)
	}
	// 删除
	for i := 0; i < size/2; i++ {
		d := newData(pool[i])
		sl.Remove(d)
	}
	printFn()
}

// 测试数字
func TestSkiplistInt(t *testing.T) {
	testTT(10, func(i int) Int64 { return Int64(i) }, func(i Int64) Int64 { return i + 100 })
}

// 测试复杂数据
func TestSkiplistData(t *testing.T) {
	testTT(10, func(i int) *_Data {
		return &_Data{id: int64(i), score: int64(i * 10), ts: int64(i * 10000)}
	}, func(e *_Data) *_Data {
		return &_Data{id: e.id, score: e.score + 1000, ts: e.ts + 10000}
	})
}

type _Data struct {
	id    int64
	score int64
	ts    int64
}

// 降序
func (d *_Data) Compare(o *_Data) int {
	if d.score > o.score {
		return -1
	} else if d.score < o.score {
		return 1
	}
	if d.ts < o.ts {
		return -1
	} else if d.ts > o.ts {
		return 1
	}
	return int(d.id - o.id)
}

// Int64 升序
type Int64 int64

var _ ElemType[Int64] = Int64(0)

func (v Int64) Compare(o Int64) int {
	if v < o {
		return -1
	} else if v > o {
		return 1
	}
	return 0
}
func (v Int64) UniqueEqual(o Int64) bool {
	return v == o
}

// RInt64 降序
type RInt64 int64

func (v RInt64) Compare(o RInt64) int {
	if v < o {
		return 1
	} else if v > o {
		return -1
	}
	return 0
}
func (v RInt64) UniqueEqual(o RInt64) bool {
	return v == o
}
