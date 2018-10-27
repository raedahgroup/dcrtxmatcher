package flint

/*
#cgo CFLAGS: -I ../include
#cgo LDFLAGS: -L /usr/local/flint/lib -lflint -lmpfr -lmpir -lpthread
#cgo LDFLAGS: -Wl,-rpath,/usr/local/flint/lib
#include <stdio.h>
#include "solverflint.h"
#include <stdlib.h>
*/
import "C"
import "unsafe"
import "github.com/decred/dcrwallet/dcrtxclient/finitefield"

// GetRoots solves polynomial with ps is prime number
// sums is slice of finite field with length in size.
// Returns the roots in slice of string.
func GetRoots(ps string, powersums []field.Field, size int) (int, []string) {

	sumb := [][]byte{}
	for i := 0; i < size; i++ {
		sumb = append(sumb, []byte(powersums[i].HexStr()))
	}

	outer := make([]*C.char, size)
	msgs := (**C.char)(unsafe.Pointer(&outer[0]))

	p := C.CString(ps)
	ret := C.solve(msgs, p, StringsToChars(sumb), C.ulong(size), 16)
	defer C.free(unsafe.Pointer(p))

	if ret == 0 {
		omsgs := CharsToStrings(C.int(size), msgs)
		return int(ret), omsgs
	}
	return int(ret), []string{}
}

// StringsToChars converts slice of bytes(slice of string) in golang to string in C
func StringsToChars(b [][]byte) **C.char {
	outer := make([]*C.char, len(b)+1)
	for i, inner := range b {
		outer[i] = C.CString(string(inner))
	}
	return (**C.char)(unsafe.Pointer(&outer[0]))
}

//CharsToStrings converts string in C to slice of string in golang
func CharsToStrings(argc C.int, argv **C.char) []string {
	length := int(argc)
	tmpSlice := (*[1 << 30]*C.char)(unsafe.Pointer(argv))[:length:length]
	goString := make([]string, length)
	for i, s := range tmpSlice {
		goString[i] = C.GoString(s)
	}
	return goString
}
