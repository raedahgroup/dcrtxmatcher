package field

import (
	"fmt"
	"testing"
	//"github.com/raedahgroup/dcrtxmatcher/finitefield"
)

func TestShiflLL(t *testing.T) {
	f := uint128{0, 1}

	s := uint128{0, 127}

	f1 := f.ShiftLL(s)

	fmt.Println("f1.Hi: ", f1.Hi, f1.Lo)
}

func TestShiflL(t *testing.T) {
	f := uint128{0, 1}

	s := 127

	f1 := f.ShiftL(uint64(s))

	fmt.Println("f1.Hi: ", f1.Hi, f1.Lo)
}

func TestReduce(t *testing.T) {
	a := UInt64(2)
	a.reduceOnce()

	fmt.Println("a ", a)
}
