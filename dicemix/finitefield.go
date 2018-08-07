package dicemix

import (
	"encoding/binary"
	"log"

	"github.com/cznic/mathutil"
)

//Redefine uint64 so can add func for this type
type UInt64 uint64

// P - size of field. The field size 9223372036854775807
//2305843009213693951

const P UInt64 = (1 << 63) - 1

// Field -- where n is between 0 <= n < P.
type Field struct {
	n UInt64
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
	var value = (src << 3) | (op2 >> 63)
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
func NewField(value UInt64) Field {
	return Field{value.reduceOnce().reduceOnceAssert()}
}

// Parse UInt64 from bytes
func Uint64FromBytes(b []byte) UInt64 {
	return UInt64(binary.BigEndian.Uint64(b[:8]))
}

// Neg negates number in the field
func (src Field) Neg() Field {
	return Field{(P - src.n).reduceOnce().reduceOnceAssert()}
}

// Add adds two field elements and reduces the resulting number in the field
func (src Field) Add(op2 Field) Field {
	return Field{(src.n + op2.n).reduceOnce().reduceOnceAssert()}
}

// AddAssign works same as Add, assigns final value to src
func (src *Field) AddAssign(op2 Field) {
	*src = src.Add(op2)
}

// Sub subtracts two field elements and reduces the resulting number in the field
func (src Field) Sub(op2 Field) Field {
	if op2.n > src.n {
		return Field{(P - op2.n + src.n).reduceOnce().reduceOnceAssert()}
	}
	return Field{(src.n - op2.n).reduceOnce().reduceOnceAssert()}
}

// SubAssign works same as Sub, assigns final value to src
func (src *Field) SubAssign(op2 Field) {
	*src = src.Sub(op2)
}

// Mul muliplies two field elements and reduces the resulting number in the field
func (src Field) Mul(op2 Field) Field {
	var high, low uint64 = mathutil.MulUint128_64(uint64(src.n), uint64(op2.n))
	var rh, rl = UInt64(high), UInt64(low)
	var res = rh.reduceOnceMul(rl).reduceOnceAssert()
	return Field{res}
}

// MulAssign works same as Mul, assigns final value to src
func (src *Field) MulAssign(op2 Field) {
	*src = src.Mul(op2)
}
