package skiplist

type RankTree[U comparable, T ElemType[T]] struct {
	sl   *SkipList[T]
	dict map[U]T
}

// U: 用户唯一标识，T: 参与排序的数据，一般需要包含U，以保证T在表里唯一
func NewRankTree[U comparable, T ElemType[T]]() *RankTree[U, T] {
	return &RankTree[U, T]{
		sl:   NewSkipList[T](),
		dict: make(map[U]T),
	}
}

// 添加或更新, 返回rank>0表示最新的rank, rank=0表示rank不变，上层根据需要查询rank
func (t *RankTree[U, T]) Update(userKey U, data T) (rank int) {
	if d, ok := t.dict[userKey]; ok {
		if d.Compare(data) == 0 {
			return
		}
		return t.sl.Update(d, data)
		//t.sl.Remove(d)
	}

	rank = t.sl.Insert(data)
	t.dict[userKey] = data
	return
}

// 删除
func (t *RankTree[U, T]) Remove(userKey U) bool {
	if d, ok := t.dict[userKey]; ok {
		t.sl.Remove(d)
		delete(t.dict, userKey)
		return true
	}
	return false
}

// 查询rank
func (t *RankTree[U, T]) GetRank(userKey U) int {
	if d, ok := t.dict[userKey]; ok {
		return t.sl.GetRank(d)
	}
	return -1
}

// 根据排名范围 查询信息
func (t *RankTree[U, T]) QueryByRankRange(min, max int) []T {
	if min < 1 {
		min = 1
	}
	if max < 0 {
		max = t.sl.length
	} else if max > t.sl.length {
		max = t.sl.length
	}
	if min > max {
		return nil
	}
	return t.sl.SearchByRankRange(min, max)
}

// 根据排名查询信息
func (t *RankTree[U, T]) QueryByRank(rank int) (result T, ex bool) {
	return t.sl.SearchByRank(rank)
}

// 获取排名列表
func (t *RankTree[U, T]) GetAll() []T {
	return t.sl.SearchByRankRange(1, t.sl.length)
}

// 遍历
func (t *RankTree[U, T]) Foreach(f func(data T, rank int) bool) {
	t.sl.Foreach(f)
}

func (t *RankTree[U, T]) Clear() {
	t.sl.Clear()
	clear(t.dict)
}

func (t *RankTree[U, T]) Len() int {
	return t.sl.length
}
