package util

func BoolTo[T ~int | ~int8 | ~int16 | ~int32 | ~int64 |
	~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64](b bool) T {
	if b {
		return 1
	}
	return 0
}
