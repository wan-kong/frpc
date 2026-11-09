//go:build windows

package runtime

import (
	"os/exec"
	"syscall"
	"unsafe"
)

func processGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func prepareChild(cmd *exec.Cmd) { processGroup(cmd) }

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	createJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	setInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	assignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
)

const (
	jobObjectExtendedLimitInformationClass = 9
	jobObjectLimitKillOnJobClose           = 0x2000
	processSetQuota                        = 0x0100
	processTerminate                       = 0x0001
)

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
}

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IOInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// adoptChild puts the frpc process in a job whose handle the session holds,
// so Windows terminates it if the session dies without a chance to stop it.
func adoptChild(pid int) func() {
	job, _, _ := createJobObjectW.Call(0, 0)
	if job == 0 {
		return func() {}
	}
	info := jobObjectExtendedLimitInformation{BasicLimitInformation: jobObjectBasicLimitInformation{LimitFlags: jobObjectLimitKillOnJobClose}}
	if ok, _, _ := setInformationJobObject.Call(job, jobObjectExtendedLimitInformationClass, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)); ok == 0 {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return func() {}
	}
	process, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(pid))
	if err != nil {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return func() {}
	}
	defer syscall.CloseHandle(process)
	if ok, _, _ := assignProcessToJobObject.Call(job, uintptr(process)); ok == 0 {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return func() {}
	}
	return func() { _ = syscall.CloseHandle(syscall.Handle(job)) }
}

func signalChild(cmd *exec.Cmd) error { return cmd.Process.Kill() }
func killChild(cmd *exec.Cmd) error   { return killGroup(cmd) }
