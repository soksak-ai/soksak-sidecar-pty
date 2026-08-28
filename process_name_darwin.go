//go:build darwin && cgo

package main

/*
#include <libproc.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func currentDarwinProcessName(pid int) (string, error) {
	buffer := make([]byte, 1024)
	count := C.proc_name(C.int(pid), unsafe.Pointer(&buffer[0]), C.uint32_t(len(buffer)))
	if count <= 0 {
		return "", fmt.Errorf("proc_name(%d) returned %d", pid, int(count))
	}
	return string(buffer[:count]), nil
}
