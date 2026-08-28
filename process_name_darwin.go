//go:build darwin && cgo

package main

/*
#include <libproc.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static char *soksak_process_name = NULL;

static int soksak_apply_process_name(const char *name) {
	char *next = strdup(name);
	if (next == NULL) return 0;
	setprogname(next);
	free(soksak_process_name);
	soksak_process_name = next;
	return 1;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func applyPlatformProcessName(label string) error {
	if len(label) == 0 || len(label) > 31 {
		return fmt.Errorf("Darwin process label has %d bytes; want 1 through 31", len(label))
	}
	name := C.CString(label)
	defer C.free(unsafe.Pointer(name))
	if C.soksak_apply_process_name(name) == 0 {
		return fmt.Errorf("setprogname could not retain the process label")
	}
	actual, err := currentDarwinProcessName(int(C.getpid()))
	if err != nil {
		return err
	}
	if actual != label {
		return fmt.Errorf("Darwin process name = %q, want %q", actual, label)
	}
	return nil
}

func currentDarwinProcessName(pid int) (string, error) {
	buffer := make([]byte, 1024)
	count := C.proc_name(C.int(pid), unsafe.Pointer(&buffer[0]), C.uint32_t(len(buffer)))
	if count <= 0 {
		return "", fmt.Errorf("proc_name(%d) returned %d", pid, int(count))
	}
	return string(buffer[:count]), nil
}
