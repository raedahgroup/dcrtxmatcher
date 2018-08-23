package main

import (
	"fmt"
	"testing"

	"github.com/raedahgroup/dcrtxmatcher/finitefield"
)

var testdata = [][]field.Uint128{
	[]field.Uint128{
		field.Uint128{0x7FFFFFFFFFFFFFFF, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFFFFF, 0xFFFFFFFFFFFFFFFE}},
	[]field.Uint128{
		field.Uint128{0x0b1b5dcbb65d530c, 0x4a19d3cfe5033887},
		field.Uint128{0x27d9803748f6be68, 0x75282823a6ac5d5a}},
	[]field.Uint128{
		field.Uint128{0x0, 0x4a19d3cfe5033887},
		field.Uint128{0x0, 0x75282823a6ac5d5a}},
}

func TestSolve(t *testing.T) {

	for i := 0; i < len(testdata); i++ {
		//		psum := make([]field.Field,len(testdata[i])
		//		for j := 1; j <= len(testdata[i]); j++ {
		//			sum := field.Field{}

		//		}
		f1 := field.NewFF(testdata[i][0])
		f2 := field.NewFF(testdata[i][1])
		s1 := f1.Add(f1.Mul(f1))
		s2 := f2.Add(f2.Mul(f2))

		roots := GetRoots(field.Prime.HexStr(), []field.Field{s1, s2}, 2)
		fmt.Println("roots:", roots)
	}

}
