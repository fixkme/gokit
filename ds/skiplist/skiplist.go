package skiplist

import (
	"math/rand"
	"time"
)

const (
	maxLevel  = 32   // 跳跃表最大层数
	skipListP = 0.25 // 随机概率
)

func randomLevel(r *rand.Rand) int {
	level := 1
	for r.Float32() < skipListP && level < maxLevel {
		level++
	}
	return level
}

type Node[T ElemType[T]] struct {
	Data  T
	level []skiplistLevel[T]
}

type skiplistLevel[T ElemType[T]] struct {
	next *Node[T]
	span int
}

type SkipList[T ElemType[T]] struct {
	header *Node[T]
	level  int
	length int
	rand   *rand.Rand
}

func NewSkipList[T ElemType[T]]() *SkipList[T] {
	header := &Node[T]{}
	header.level = make([]skiplistLevel[T], maxLevel)
	return &SkipList[T]{
		header: header,
		level:  1,
		length: 0,
		rand:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// 插入新的元素，返回rank;
// 这里假设data不在表中，由上层保证data不重复
func (sl *SkipList[T]) Insert(data T) int {
	var update [maxLevel]*Node[T]
	var x *Node[T]
	var rank [maxLevel]int
	var i, level int

	x = sl.header
	for i = sl.level - 1; i >= 0; i-- {
		if i == sl.level-1 {
			rank[i] = 0
		} else {
			rank[i] = rank[i+1]
		}
		for x.level[i].next != nil && (x.level[i].next.Data.Compare(data) < 0) {
			rank[i] += x.level[i].span
			x = x.level[i].next
		}
		update[i] = x
	}
	level = randomLevel(sl.rand)
	if level > sl.level {
		for i = sl.level; i < level; i++ {
			rank[i] = 0
			update[i] = sl.header
			update[i].level[i].span = sl.length
		}
		sl.level = level
	}
	x = &Node[T]{Data: data, level: make([]skiplistLevel[T], level)}
	for i = 0; i < level; i++ {
		x.level[i].next = update[i].level[i].next
		update[i].level[i].next = x

		/* update span covered by update[i] as x is inserted here */
		x.level[i].span = update[i].level[i].span - (rank[0] - rank[i])
		update[i].level[i].span = (rank[0] - rank[i]) + 1
	}

	/* increment span for untouched levels */
	for i = level; i < sl.level; i++ {
		update[i].level[i].span++
	}

	sl.length++
	return rank[0] + 1 //从1开始
}

func (sl *SkipList[T]) Remove(data T) bool {
	var update [maxLevel]*Node[T]
	var x *Node[T]
	var i int

	x = sl.header
	for i = sl.level - 1; i >= 0; i-- {
		for x.level[i].next != nil && (x.level[i].next.Data.Compare(data) < 0) {
			x = x.level[i].next
		}
		update[i] = x
	}

	x = x.level[0].next
	if x != nil && x.Data.Compare(data) == 0 {
		// delete x
		for i = 0; i < sl.level; i++ {
			if update[i].level[i].next == x {
				update[i].level[i].span += x.level[i].span - 1
				update[i].level[i].next = x.level[i].next
			} else {
				update[i].level[i].span -= 1
			}
		}
		for sl.level > 1 && sl.header.level[sl.level-1].next == nil {
			sl.level--
		}
		sl.length--
		return true
	}
	return false /* not found */
}

func (sl *SkipList[T]) Contains(data T) bool {
	x := sl.header
	for i := sl.level - 1; i >= 0; i-- {
		for x.level[i].next != nil && (x.level[i].next.Data.Compare(data) < 0) {
			x = x.level[i].next
		}
	}
	x = x.level[0].next
	return x != nil && x.Data.Compare(data) == 0
}

// 更新数据, 返回rank, rank=0表示rank位置不变, rank>0表示data的rank改变
// 这里假设old在表中，由上层保证
func (sl *SkipList[T]) Update(old, data T) int {
	var update [maxLevel]*Node[T]
	var x *Node[T]
	var i int

	x = sl.header
	for i = sl.level - 1; i >= 0; i-- {
		for x.level[i].next != nil && (x.level[i].next.Data.Compare(old) < 0) {
			x = x.level[i].next
		}
		update[i] = x
	}

	/* Jump to our element: note that this function assumes that the
	 * element with the matching score exists. */
	prev := x
	x = x.level[0].next /* Assert(x && x == old) */

	/* If the node, after the score update, would be still exactly
	 * at the same position, we can just update the score without
	 * actually removing and re-inserting the element in the skiplist. */
	if (prev == sl.header || prev.Data.Compare(data) < 0) &&
		(x.level[0].next == nil || x.level[0].next.Data.Compare(data) > 0) {
		x.Data = data
		return 0 //位置不变，上层根据需要查询rank
	}

	// delete x
	for i = 0; i < sl.level; i++ {
		if update[i].level[i].next == x {
			update[i].level[i].span += x.level[i].span - 1
			update[i].level[i].next = x.level[i].next
		} else {
			update[i].level[i].span -= 1
		}
	}
	for sl.level > 1 && sl.header.level[sl.level-1].next == nil {
		sl.level--
	}
	sl.length--

	// insert data
	return sl.Insert(data)
}

// 通过数据获取rank
func (sl *SkipList[T]) GetRank(data T) int {
	rank := 0
	x := sl.header
	for i := sl.level - 1; i >= 0; i-- {
		for x.level[i].next != nil && (x.level[i].next.Data.Compare(data) < 0) {
			rank += x.level[i].span
			x = x.level[i].next
		}
	}
	x = x.level[0].next
	if x != nil && x.Data.Compare(data) == 0 {
		return rank + 1 //从1开始
	}
	return -1
}

// 根据rank获取节点
func (sl *SkipList[T]) getNodeByRank(rank int) *Node[T] {
	visited := 0
	x := sl.header
	for i := sl.level - 1; i >= 0; i-- {
		for x.level[i].next != nil && (visited+x.level[i].span) <= rank {
			visited += x.level[i].span
			x = x.level[i].next
		}
		if visited == rank {
			return x
		}
	}
	return nil
}

// 通过rank查找数据，rank从1开始
func (sl *SkipList[T]) SearchByRank(rank int) (T, bool) {
	node := sl.getNodeByRank(rank)
	if node != nil {
		return node.Data, true
	}
	return *new(T), false
}

// 通过rank范围查找数据，rank从1开始
func (sl *SkipList[T]) SearchByRankRange(min, max int) []T {
	res := make([]T, 0)
	st := sl.getNodeByRank(min)
	if st == nil {
		return nil
	}

	for x := st; min <= max && x != nil; x = x.level[0].next {
		res = append(res, x.Data)
		min++
	}
	return res
}

// 遍历
func (sl *SkipList[T]) Foreach(f func(T, int) bool) {
	x := sl.header
	var rank int = 1
	for x = x.level[0].next; x != nil; x = x.level[0].next {
		if !f(x.Data, rank) {
			break
		}
		rank++
	}
}

func (sl *SkipList[T]) Clear() {
	for i := 0; i < maxLevel; i++ {
		sl.header.level[i].next = nil
		sl.header.level[i].span = 0
	}
	sl.level = 1
	sl.length = 0
}

func (sl *SkipList[T]) Len() int {
	return sl.length
}
