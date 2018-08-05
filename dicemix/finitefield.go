package dicemix

import (
	"errors"
	//"math/big"
)

const (
	p = (1 << 63) - 1
)

type (
	//finite field with prime p
	Fp struct {
		//n is in range 1..p-1
		n uint64
	}
)

func NewFieldElement(n uint64) (*Fp, error) {
	if n >= p {
		return nil, errors.New("n must less than p")
	}

	return &Fp{n: n}, nil
}

//compare two elements
func (fp *Fp) Equal(fp2 *Fp) bool {
	if fp.n == fp2.n {
		return true
	}
	return false
}

//negative value of fp
func (fp *Fp) Negative() *Fp {
	return &Fp{p - fp.n}
}

//add two elements
func (fp *Fp) Add(fp2 *Fp) (*Fp, error) {
	if fp.Equal(fp2) == true {
		return nil, errors.New("two fields equal")
	}
	return &Fp{fp.n + fp2.n}, nil
}

//add two elements and assign to first
func (fp *Fp) AddAssign(fp2 *Fp) {
	fp, _ = fp.Add(fp2)
}

//subtract two elements
func (fp *Fp) Sub(fp2 *Fp) *Fp {
	if fp2.n > fp.n {
		return &Fp{p - fp2.n + fp.n}
	}
	return &Fp{fp.n + (-fp2.n)}
}

//subtract two elements add assign to first
func (fp *Fp) SubAssign(fp2 *Fp) {
	fp = fp.Sub(fp2)
}
