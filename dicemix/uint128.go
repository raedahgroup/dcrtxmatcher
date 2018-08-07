package dicemix

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/pkg/errors"
)

// Big endian uint128
type Uint128 struct {
	H, L uint64
}

//compare two uint128
//-1: less than, 0: equal, 1:greater
func (u *Uint128) Compare(o *Uint128) int {
	if u.H < o.H {
		return -1
	} else if u.H > o.H {
		return 1
	}

	if u.L < o.L {
		return -1
	} else if u.L > o.L {
		return 1
	}

	return 0
}

//And bits
func (u *Uint128) And(o *Uint128) {
	u.H &= o.H
	u.L &= o.L
}

//Or bits
func (u *Uint128) Or(o *Uint128) {
	u.H |= o.H
	u.L |= u.L
}

//xor bit
func (u *Uint128) Xor(o *Uint128) {
	u.H ^= o.H
	u.L ^= o.L
}

//Add
func (u *Uint128) Add(o *Uint128) {
	carry := u.L
	u.L += o.L
	u.H += o.H

	if u.L < carry {
		u.H += 1
	}
}

// Sub returns a new Uint128 decremented by n.
func (u Uint128) Sub(n uint64) Uint128 {
	lo := u.L - n
	hi := u.H
	if u.L < lo {
		hi--
	}
	return Uint128{hi, lo}
}

//parse uint128 from string
func NewFromString(s string) (u *Uint128, err error) {

	if len(s) > 32 {
		return nil, fmt.Errorf("s:%s length greater than 32", s)
	}

	b, err := hex.DecodeString(fmt.Sprintf("%032s", s))
	if err != nil {
		return nil, err
	}
	rdr := bytes.NewReader(b)
	u = new(Uint128)
	err = binary.Read(rdr, binary.BigEndian, u)
	return
}

//return hexstring of uint128
func (u *Uint128) HexString() string {
	if u.H == 0 {
		return fmt.Sprintf("%x", u.L)
	}
	return fmt.Sprintf("%x%016x", u.H, u.L)
}

// GetBytes returns a big-endian byte representation.
func (u Uint128) GetBytes() []byte {
	buf := make([]byte, 16)
	binary.BigEndian.PutUint64(buf[:8], u.H)
	binary.BigEndian.PutUint64(buf[8:], u.L)
	return buf
}

// String returns a hexadecimal string representation.
func (u Uint128) String() string {
	return hex.EncodeToString(u.GetBytes())
}

//return string
//func (u *Uint128) String() string {
//	return fmt.Sprintf("0x%032x", u.HexString())
//}

// FromBytes parses the byte slice as a 128 bit big-endian unsigned integer.
func FromBytes(b []byte) Uint128 {
	hi := binary.BigEndian.Uint64(b[:8])
	lo := binary.BigEndian.Uint64(b[8:])
	return Uint128{hi, lo}
}

// FromString parses a hexadecimal string as a 128-bit big-endian unsigned integer.
func FromString(s string) (Uint128, error) {
	if len(s) > 32 {
		return Uint128{}, errors.Errorf("input string %s too large for uint128", s)
	}
	bytes, err := hex.DecodeString(s)
	if err != nil {
		return Uint128{}, errors.Wrapf(err, "could not decode %s as hex", s)
	}

	// Grow the byte slice if it's smaller than 16 bytes, by prepending 0s
	if len(bytes) < 16 {
		bytesCopy := make([]byte, 16)
		copy(bytesCopy[(16-len(bytes)):], bytes)
		bytes = bytesCopy
	}

	return FromBytes(bytes), nil
}

// FromInts takes in two unsigned 64-bit integers and constructs a Uint128.
func FromInts(h uint64, l uint64) Uint128 {
	return Uint128{h, l}
}
