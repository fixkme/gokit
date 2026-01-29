package errs

import (
	"errors"
	"fmt"
	"testing"
)

func TestErr(t *testing.T) {
	err := Unknown.Printf("test")
	fmt.Println(err)
	fmt.Println(errors.Is(err, Unknown))
}
