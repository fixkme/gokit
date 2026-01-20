package skiplist

type ElemType[T any] interface {
	Compare(T) int
	//UniqueEqual(T) bool
}
