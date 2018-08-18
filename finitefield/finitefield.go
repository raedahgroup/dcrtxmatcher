package field

import (
	"encoding/binary"
	"fmt"
	"log"

	"github.com/cznic/mathutil"
)

//Redefine uint64 so can add func for this type
type UInt64 uint64

// P - size of field. The field size 2305843009213693951

const P UInt64 = (1 << 61) - 1

// Field -- where n is between 0 <= n < P.
type Ff struct {
	n UInt64
}

func (ff Ff) Value() uint64 {
	return uint64(ff.n)
}

func (ff Ff) Str(base int) string {
	if base == 10 {
		return fmt.Sprintf("%d", uint64(ff.n))
	} else if base == 16 {
		return fmt.Sprintf("%x", uint64(ff.n))
	}
	return ""

}

func (src UInt64) reduceOnce() UInt64 {
	var value = (src & P) + (src >> 63)
	if value == P {
		return 0
	}
	return value
}

func (src UInt64) reduceOnceAssert() UInt64 {
	var res = src.reduceOnce()
	if res >= P {
		log.Fatalf("Error: Expected result should be less than field size %d >= %d", res, P)
	}
	return res
}

func (src UInt64) reduceOnceMul(op2 UInt64) UInt64 {
	var value = (src << 3) | (op2 >> 61)
	value = (op2 & P) + value
	if value == P {
		return 0
	}
	return value
}

func asLimbs(x UInt64) (uint32, uint32) {
	return uint32(x >> 32), uint32(x)
}

// NewField reduces initial value in the field
func NewField(value UInt64) Ff {
	return Ff{value.reduceOnce().reduceOnceAssert()}
}

// Parse UInt64 from bytes
func Uint64FromBytes(b []byte) UInt64 {
	return UInt64(binary.BigEndian.Uint64(b[:8]))
}

// Neg negates number in the field
func (src Ff) Neg() Ff {
	return Ff{(P - src.n).reduceOnce().reduceOnceAssert()}
}

// Add adds two field elements and reduces the resulting number in the field
func (src Ff) Add(op2 Ff) Ff {
	return Ff{(src.n + op2.n).reduceOnce().reduceOnceAssert()}
}

// AddAssign works same as Add, assigns final value to src
func (src *Ff) AddAssign(op2 Ff) {
	*src = src.Add(op2)
}

// Sub subtracts two field elements and reduces the resulting number in the field
func (src Ff) Sub(op2 Ff) Ff {
	if op2.n > src.n {
		return Ff{(P - op2.n + src.n).reduceOnce().reduceOnceAssert()}
	}
	return Ff{(src.n - op2.n).reduceOnce().reduceOnceAssert()}
}

// SubAssign works same as Sub, assigns final value to src
func (src *Ff) SubAssign(op2 Ff) {
	*src = src.Sub(op2)
}

// Mul muliplies two field elements and reduces the resulting number in the field
func (src Ff) Mul(op2 Ff) Ff {
	var high, low uint64 = mathutil.MulUint128_64(uint64(src.n), uint64(op2.n))
	var rh, rl = UInt64(high), UInt64(low)
	var res = rh.reduceOnceMul(rl).reduceOnceAssert()
	return Ff{res}
}

// MulAssign works same as Mul, assigns final value to src
func (src *Ff) MulAssign(op2 Ff) {
	*src = src.Mul(op2)
}
