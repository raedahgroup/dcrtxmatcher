package main

/*
#cgo CFLAGS: -I ../include
#cgo LDFLAGS: -L /usr/local/flint/lib -lflint -lmpfr -lmpir -lpthread
#cgo LDFLAGS: -Wl,-rpath,/usr/local/flint/lib
#include <stdio.h>
#include "solverflint.h"
#include <stdlib.h>
*/
import "C"
import "fmt"
import "unsafe"
import "github.com/raedahgroup/dcrtxmatcher/finitefield"

func main() {
	/* power_sums = [
	    (384ae5480f49d67c51b83df1fff94e90),
	    (6e9de51c5deca89883084cd992088c11),
	    (38132da941235c87e3f33762aa488840),
	    (75bc93bff8a8ce7b4fb23af15dbbaebc),
	    (1f8abf68afa44bf42a0da59b4885d94c),
	];
	expected = [
	    (0b1b5dcbb65d530c4a19d3cfe5033887),
	    (27d9803748f6be6875282823a6ac5d5a),
	    (3a3112db6e48449711521bbc42944db3),
	    (52027185cadce683709dfb288e7de45b),
	    (792282e3d6d099ed10862b19a337869f),
	];*/

	sums := [][]byte{[]byte("384ae5480f49d67c51b83df1fff94e90"),
		[]byte("6e9de51c5deca89883084cd992088c11"),
		[]byte("38132da941235c87e3f33762aa488840"),
		[]byte("75bc93bff8a8ce7b4fb23af15dbbaebc"),
		[]byte("1f8abf68afa44bf42a0da59b4885d94c")}

	outer := make([]*C.char, 5)

	msgs := (**C.char)(unsafe.Pointer(&outer[0]))

	p := C.CString("7FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF")
	ret := C.solve(msgs, p, StringsToChars(sums), C.ulong(5))
	defer C.free(unsafe.Pointer(p))

	omsg := CharsToStrings(5, msgs)
	fmt.Println("ret of solve and roots", ret, omsg)
}

//solve polynomial with ps is prime number
//sums is slice of finite field with length in size
//return the roots in slice of string
func GetRoots(ps string, powersums []field.Field, size int) []string {

	sumb := [][]byte{}
	for i := 0; i < size; i++ {
		sumb = append(sumb, []byte(powersums[i].HexStr()))
	}

	outer := make([]*C.char, size)
	msgs := (**C.char)(unsafe.Pointer(&outer[0]))

	p := C.CString(ps)
	ret := C.solve(msgs, p, StringsToChars(sumb), C.ulong(size))
	defer C.free(unsafe.Pointer(p))

	if ret == 0 {
		omsg := CharsToStrings(C.int(size), msgs)
		return omsg
	}
	return []string{}

}

func StringsToChars(b [][]byte) **C.char {
	outer := make([]*C.char, len(b)+1)
	for i, inner := range b {
		outer[i] = C.CString(string(inner))
	}
	return (**C.char)(unsafe.Pointer(&outer[0]))
}

func CharsToStrings(argc C.int, argv **C.char) []string {
	length := int(argc)
	tmpslice := (*[1 << 30]*C.char)(unsafe.Pointer(argv))[:length:length]
	gostrings := make([]string, length)
	for i, s := range tmpslice {
		gostrings[i] = C.GoString(s)
	}
	return gostrings
}
