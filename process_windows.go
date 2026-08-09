//go:build windows

package portablesh

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	createNewProcessGroup                  = 0x00000200
	jobObjectExtendedLimitInformationClass = 9
	jobObjectLimitKillOnJobClose           = 0x00002000
	processTerminate                       = 0x0001
	processSetQuota                        = 0x0100
	processQueryInformation                = 0x0400
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject       = kernel32.NewProc("TerminateJobObject")
	procOpenProcess              = kernel32.NewProc("OpenProcess")
	procCloseHandle              = kernel32.NewProc("CloseHandle")
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
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IOInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

func prepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
	cmd.WaitDelay = 2 * time.Second
}

func newExternalCommand(ctx context.Context, path string, args, environment []string) *exec.Cmd {
	extension := strings.ToLower(filepath.Ext(path))
	if extension != ".cmd" && extension != ".bat" {
		cmd := exec.CommandContext(ctx, path, args[1:]...)
		cmd.Args = append([]string{args[0]}, args[1:]...)
		return cmd
	}
	interpreter := envMap(environment)["COMSPEC"]
	if interpreter == "" {
		interpreter = "cmd.exe"
	}
	commandLine := quoteCMDArgument(path)
	for _, arg := range args[1:] {
		commandLine += " " + quoteCMDArgument(arg)
	}
	return exec.CommandContext(ctx, interpreter, "/d", "/s", "/c", commandLine)
}

func configureExtraFiles(_ *exec.Cmd, descriptors map[int]Descriptor) ([]io.Closer, error) {
	for fd := range descriptors {
		if fd > 2 {
			return nil, fmt.Errorf("descriptor %d cannot be inherited by Windows processes", fd)
		}
	}
	return nil, nil
}

func quoteCMDArgument(value string) string {
	value = strings.ReplaceAll(value, `"`, `\"`)
	for _, character := range `&|<>()^%!` {
		value = strings.ReplaceAll(value, string(character), "^"+string(character))
	}
	return `"` + value + `"`
}

func runPreparedCommand(ctx context.Context, cmd *exec.Cmd) error {
	prepareCommand(cmd)
	job, err := createKillJob()
	if err != nil {
		return cmd.Run()
	}
	defer closeWindowsHandle(job)
	cmd.Cancel = func() error {
		result, _, callErr := procTerminateJobObject.Call(uintptr(job), 1)
		if result == 0 && callErr != syscall.Errno(0) {
			return callErr
		}
		return nil
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	process, _, openErr := procOpenProcess.Call(
		processTerminate|processSetQuota|processQueryInformation,
		0,
		uintptr(uint32(cmd.Process.Pid)),
	)
	if process == 0 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("open child process for job object: %w", openErr)
	}
	processHandle := syscall.Handle(process)
	assigned, _, assignErr := procAssignProcessToJobObject.Call(uintptr(job), uintptr(processHandle))
	closeWindowsHandle(processHandle)
	if assigned == 0 {
		// Some hosts already place children in a non-breakaway job. Keep the
		// command functional; CommandContext still cancels its direct process.
		cmd.Cancel = func() error { return cmd.Process.Kill() }
		_ = assignErr
	}
	if err := ctx.Err(); err != nil {
		_ = cmd.Cancel()
	}
	return cmd.Wait()
}

func createKillJob() (syscall.Handle, error) {
	handle, _, callErr := procCreateJobObjectW.Call(0, 0)
	if handle == 0 {
		return 0, callErr
	}
	job := syscall.Handle(handle)
	information := jobObjectExtendedLimitInformation{}
	information.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	result, _, setErr := procSetInformationJobObject.Call(
		uintptr(job),
		jobObjectExtendedLimitInformationClass,
		uintptr(unsafe.Pointer(&information)),
		unsafe.Sizeof(information),
	)
	if result == 0 {
		closeWindowsHandle(job)
		return 0, setErr
	}
	return job, nil
}

func closeWindowsHandle(handle syscall.Handle) {
	if handle != 0 {
		_, _, _ = procCloseHandle.Call(uintptr(handle))
	}
}
