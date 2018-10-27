package flint

import (
	"fmt"
	"testing"

	"github.com/decred/dcrwallet/dcrtxclient/finitefield"
)

var testdata = [][]field.Uint128{
	[]field.Uint128{
		field.Uint128{0x7FFFFFFFFFFFFFF0, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFFFF1, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFFFF2, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFFFF3, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFFFF4, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFFFF5, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFFFF6, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFFFF7, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFFFF8, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFFFF9, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFFFFA, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFFFFB, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFFFFC, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFFFFD, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFFFFE, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFFFFF, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFFF0F, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFFF1F, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFFF2F, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFFF3F, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFFF4F, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFFF5F, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFFF6F, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFFF7F, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFFF8F, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFFF9F, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFFFAF, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFFFBF, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFFFCF, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFFFDF, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFFFEF, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFF0FF, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFF1FF, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFF2FF, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x7FFFFFFFFFFFF3FF, 0xFFFFFFFFFFFFFFFE},
		field.Uint128{0x6FFFFFFFFFFFF4FF, 0xFFFFFFFFFFFFFFFE}},
	[]field.Uint128{
		field.Uint128{0x0b1b5dcbb65d530c, 0x4a19d3cfe5033887},
		field.Uint128{0x27d9803748f6be68, 0x75282823a6ac5d5a}},
	[]field.Uint128{
		field.Uint128{0x0, 0x4a19d3cfe5033887},
		field.Uint128{0x0, 0x75282823a6ac5d5a}},
	[]field.Uint128{
		field.Uint128{0x4f19d3cfd5033890, 0x0},
		field.Uint128{0x4f19d3cfd5033892, 0xFFFFFFFFFFFFFFFe},
		field.Uint128{0x4f19d3cfd5033891, 0x0},
		field.Uint128{0x4FFFFFFFFFFFFFFF, 0x1},
		field.Uint128{0x7a282823c6ac5d09, 0x0}},
	[]field.Uint128{
		field.Uint128{0x0db1d11d5c1d709d, 0xd8d9e10b61e52846},
		field.Uint128{0x054bd2916e61025b, 0x2f4ce54b9c3bc8fc},
		field.Uint128{0x0a6c79713de09d8d, 0x45a0e5cbf273733f},
		field.Uint128{0x0dc1bdef26c55dfb, 0xa6718f1afd04a849},
		field.Uint128{0x0a77f722cf439f18, 0xc8620c6caca4b6d1},
		field.Uint128{0x0dfe0902ab3c1663, 0x9e9367d568a166b5}},
	[]field.Uint128{
		field.Uint128{0x0e95dd8a4bf5dcfa, 0x37d4a6334a840ca},
		field.Uint128{0x0d11321ff1bc76e3, 0x40dc069fb3563b42},
		field.Uint128{0x00907bdb7d4f843e, 0x62121e69493965da},
		field.Uint128{0x0ddbcc9a2a21264c, 0x27d3ad15f9e73ab3},
		field.Uint128{0x050f28b2cdaf9959, 0xcaec60626f62cb08},
		field.Uint128{0x028323d94eae2906, 0x4a5712fe776ef460}},
}

func TestSolve(t *testing.T) {

	for i := 0; i < len(testdata); i++ {

		psum := make([]field.Field, len(testdata[i]))
		for j := 0; j < len(testdata[i]); j++ {
			P := field.Field{}
			for k := 0; k < len(testdata[i]); k++ {
				P = P.Add(field.NewFF(testdata[i][k]).Exp(uint64(j + 1)))
			}
			psum[j] = P
		}

		for _, ff := range psum {
			fmt.Println("power sum:", ff.N.HexStr())
		}

		ret, roots := GetRoots(field.Prime.HexStr(), psum, len(testdata[i]))
		fmt.Printf("Ret %d. Number roots: %d. Roots: %v\n", ret, len(roots), roots)

		if ret != 0 {
			t.Error("Can not solve with input data")
		}

		for _, r := range testdata[i] {
			exist := false
			for k := 0; k < len(roots); k++ {
				if r.HexStr() == roots[k] {
					exist = true
					break
				}
			}
			if !exist {
				t.Errorf("Can not find root %s", r.HexStr())
			}
		}
	}
}
