package staticlist

import "unsafe"

type Node[T any] struct {
	Data T
	Next int
}

type StaticList[T any] struct {
	datas []Node[T]
	free  int
	zero  T // 零值
}

const Null = -1

func NewStaticList[T any](size int) *StaticList[T] {
	list := &StaticList[T]{
		datas: make([]Node[T], size),
	}
	list.Reset()
	return list
}

func (list *StaticList[T]) Malloc() int {
	p := list.free
	if p != Null {
		slot := &list.datas[p]
		list.free = slot.Next
		slot.Next = Null
	}
	return p
}

func (list *StaticList[T]) Free(p int) {
	node := &list.datas[p]
	node.Data = list.zero
	node.Next = list.free
	list.free = p
}

func (list *StaticList[T]) GetNode(p int) *Node[T] {
	return &list.datas[p]
}

func (list *StaticList[T]) GetDataValue(p int) T {
	return list.datas[p].Data
}

func (list *StaticList[T]) SetDataValue(p int, val T) {
	list.datas[p].Data = val
}

func (list *StaticList[T]) Reset() {
	size := len(list.datas)
	for i := 0; i < size-1; i++ {
		list.datas[i].Data = list.zero
		list.datas[i].Next = i + 1
	}
	list.datas[size-1].Next = Null
	list.free = 0
}

func (list *StaticList[T]) GetDataPointer(p int) *T {
	return &list.datas[p].Data
}

func (list *StaticList[T]) GetDataIndex(dataPtr *T) int {
	if len(list.datas) == 0 {
		return -1
	}

	pdata := unsafe.Pointer(dataPtr)
	elemPtr := (*Node[T])(pdata)

	sliceDataPtr := unsafe.Pointer(&list.datas[0])
	targetPtr := unsafe.Pointer(elemPtr)

	elemSize := unsafe.Sizeof(list.datas[0])
	offset := uintptr(targetPtr) - uintptr(sliceDataPtr)

	// 检查偏移是否合法
	if offset%elemSize != 0 {
		return -1
	}
	index := int(offset / elemSize)
	if index < 0 || index >= len(list.datas) {
		return -1
	}

	if &list.datas[index] == elemPtr {
		return index
	}

	return -1
}
