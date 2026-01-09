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
		v, _ := q.Pop()
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
