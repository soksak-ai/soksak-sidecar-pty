//go:build windows

package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type conPTYProcess struct {
	input, output *os.File
	console       windows.Handle
	process       windows.Handle
	job           windows.Handle
	pid           uint32
	consoleOnce   sync.Once
	closeOnce     sync.Once
	waitOnce      sync.Once
	waitErr       error
	exited        chan struct{}
}

func startSessionProcess(shell, cwd string, environment []string, cols, rows uint16) (sessionProcess, error) {
	if cols > 32767 || rows > 32767 {
		return nil, fmt.Errorf("ConPTY size exceeds int16: %dx%d", cols, rows)
	}
	inputRead, inputWrite, err := windowsPipe()
	if err != nil {
		return nil, fmt.Errorf("create ConPTY input pipe: %w", err)
	}
	outputRead, outputWrite, err := windowsPipe()
	if err != nil {
		windows.CloseHandle(inputRead)
		windows.CloseHandle(inputWrite)
		return nil, fmt.Errorf("create ConPTY output pipe: %w", err)
	}
	cleanupPipes := func() {
		for _, handle := range []windows.Handle{inputRead, inputWrite, outputRead, outputWrite} {
			if handle != 0 {
				_ = windows.CloseHandle(handle)
			}
		}
	}

	var console windows.Handle
	if err := windows.CreatePseudoConsole(
		windows.Coord{X: int16(cols), Y: int16(rows)}, inputRead, outputWrite, 0, &console,
	); err != nil {
		cleanupPipes()
		return nil, fmt.Errorf("create ConPTY: %w", err)
	}
	_ = windows.CloseHandle(inputRead)
	inputRead = 0
	_ = windows.CloseHandle(outputWrite)
	outputWrite = 0

	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(console)
		cleanupPipes()
		return nil, fmt.Errorf("allocate ConPTY process attributes: %w", err)
	}
	defer attributes.Delete()
	if err := attributes.Update(
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		*(*unsafe.Pointer)(unsafe.Pointer(&console)), unsafe.Sizeof(console),
	); err != nil {
		windows.ClosePseudoConsole(console)
		cleanupPipes()
		return nil, fmt.Errorf("attach ConPTY process attribute: %w", err)
	}

	executable, err := windows.UTF16PtrFromString(shell)
	if err != nil {
		windows.ClosePseudoConsole(console)
		cleanupPipes()
		return nil, fmt.Errorf("encode shell path: %w", err)
	}
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{shell}))
	if err != nil {
		windows.ClosePseudoConsole(console)
		cleanupPipes()
		return nil, fmt.Errorf("encode shell command line: %w", err)
	}
	var directory *uint16
	if cwd != "" {
		directory, err = windows.UTF16PtrFromString(cwd)
		if err != nil {
			windows.ClosePseudoConsole(console)
			cleanupPipes()
			return nil, fmt.Errorf("encode shell working directory: %w", err)
		}
	}
	environmentBlock, err := windowsEnvironmentBlock(environment)
	if err != nil {
		windows.ClosePseudoConsole(console)
		cleanupPipes()
		return nil, err
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		windows.ClosePseudoConsole(console)
		cleanupPipes()
		return nil, fmt.Errorf("create shell job: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)),
	); err != nil {
		windows.CloseHandle(job)
		windows.ClosePseudoConsole(console)
		cleanupPipes()
		return nil, fmt.Errorf("configure shell job: %w", err)
	}

	startup := windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb:    uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
			Flags: windows.STARTF_USESTDHANDLES,
		},
		ProcThreadAttributeList: attributes.List(),
	}
	info := windows.ProcessInformation{}
	processSecurity := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{}))}
	threadSecurity := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{}))}
	flags := uint32(windows.CREATE_DEFAULT_ERROR_MODE | windows.CREATE_UNICODE_ENVIRONMENT |
		windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_SUSPENDED)
	if err := windows.CreateProcess(
		executable, commandLine, &processSecurity, &threadSecurity, false, flags,
		&environmentBlock[0], directory, &startup.StartupInfo, &info,
	); err != nil {
		windows.CloseHandle(job)
		windows.ClosePseudoConsole(console)
		cleanupPipes()
		return nil, fmt.Errorf("start shell in ConPTY: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, info.Process); err != nil {
		_ = windows.TerminateProcess(info.Process, 1)
		windows.CloseHandle(info.Thread)
		windows.CloseHandle(info.Process)
		windows.CloseHandle(job)
		windows.ClosePseudoConsole(console)
		cleanupPipes()
		return nil, fmt.Errorf("assign shell to job: %w", err)
	}
	if _, err := windows.ResumeThread(info.Thread); err != nil {
		_ = windows.TerminateJobObject(job, 1)
		windows.CloseHandle(info.Thread)
		windows.CloseHandle(info.Process)
		windows.CloseHandle(job)
		windows.ClosePseudoConsole(console)
		cleanupPipes()
		return nil, fmt.Errorf("resume ConPTY shell: %w", err)
	}
	windows.CloseHandle(info.Thread)
	result := &conPTYProcess{
		input:   os.NewFile(uintptr(inputWrite), "conpty-input"),
		output:  os.NewFile(uintptr(outputRead), "conpty-output"),
		console: console, process: info.Process, job: job, pid: info.ProcessId,
		exited: make(chan struct{}),
	}
	go result.watchExit()
	return result, nil
}

func windowsPipe() (windows.Handle, windows.Handle, error) {
	var read, write windows.Handle
	if err := windows.CreatePipe(&read, &write, nil, 0); err != nil {
		return 0, 0, err
	}
	return read, write, nil
}

func windowsEnvironmentBlock(environment []string) ([]uint16, error) {
	ordered := append([]string(nil), environment...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return strings.ToUpper(ordered[i]) < strings.ToUpper(ordered[j])
	})
	block := make([]uint16, 0, 256)
	for _, entry := range ordered {
		encoded, err := windows.UTF16FromString(entry)
		if err != nil {
			return nil, fmt.Errorf("encode environment entry: %w", err)
		}
		block = append(block, encoded...)
	}
	if len(block) == 0 {
		block = append(block, 0)
	}
	block = append(block, 0)
	return block, nil
}

func (p *conPTYProcess) Read(buffer []byte) (int, error) { return p.output.Read(buffer) }
func (p *conPTYProcess) Write(data []byte) (int, error)  { return p.input.Write(data) }
func (p *conPTYProcess) Resize(cols, rows uint16) error {
	if cols > 32767 || rows > 32767 {
		return fmt.Errorf("ConPTY size exceeds int16: %dx%d", cols, rows)
	}
	return windows.ResizePseudoConsole(p.console, windows.Coord{X: int16(cols), Y: int16(rows)})
}
func (p *conPTYProcess) PID() uint32 { return p.pid }
func (p *conPTYProcess) watchExit() {
	_, _ = windows.WaitForSingleObject(p.process, windows.INFINITE)
	p.closeConsole()
	close(p.exited)
}
func (p *conPTYProcess) Terminate() error {
	if p.job == 0 {
		return nil
	}
	return windows.TerminateJobObject(p.job, 1)
}
func (p *conPTYProcess) Wait() error {
	p.waitOnce.Do(func() {
		<-p.exited
		if p.process != 0 {
			_ = windows.CloseHandle(p.process)
			p.process = 0
		}
		if p.job != 0 {
			_ = windows.CloseHandle(p.job)
			p.job = 0
		}
	})
	return p.waitErr
}
func (p *conPTYProcess) Close() error {
	var first error
	p.closeOnce.Do(func() {
		if err := p.input.Close(); err != nil {
			first = err
		}
		if err := p.output.Close(); first == nil && err != nil {
			first = err
		}
		p.closeConsole()
	})
	return first
}

func (p *conPTYProcess) closeConsole() {
	p.consoleOnce.Do(func() { windows.ClosePseudoConsole(p.console) })
}

// ForegroundGroup is zero here. A console has no process group, so nothing on this platform can
// say which program is in front, and the caller falls back to the shell's own child.
func (p *conPTYProcess) ForegroundGroup() uint32 { return 0 }
