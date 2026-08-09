//go:build !windows

package portablesh

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func prepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
}

func newExternalCommand(ctx context.Context, path string, args, _ []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args[1:]...)
	cmd.Args = append([]string{args[0]}, args[1:]...)
	return cmd
}

func configureExtraFiles(cmd *exec.Cmd, descriptors map[int]Descriptor) ([]io.Closer, error) {
	maxFD := 2
	for fd := range descriptors {
		if fd > maxFD {
			maxFD = fd
		}
	}
	if maxFD == 2 {
		return nil, nil
	}
	cmd.ExtraFiles = make([]*os.File, maxFD-2)
	var closers []io.Closer
	for fd := 3; fd <= maxFD; fd++ {
		descriptor, ok := descriptors[fd]
		if ok {
			file, fileOK := descriptorOSFile(descriptor)
			if !fileOK {
				closeAll(closers)
				return nil, fmt.Errorf("descriptor %d is not backed by an OS file", fd)
			}
			cmd.ExtraFiles[fd-3] = file
			continue
		}
		file, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		if err != nil {
			closeAll(closers)
			return nil, err
		}
		cmd.ExtraFiles[fd-3] = file
		closers = append(closers, file)
	}
	return closers, nil
}

func descriptorOSFile(descriptor Descriptor) (*os.File, bool) {
	var selected *os.File
	if descriptor.Reader != nil {
		file, ok := descriptor.Reader.(*os.File)
		if !ok {
			return nil, false
		}
		selected = file
	}
	if descriptor.Writer != nil {
		file, ok := descriptor.Writer.(*os.File)
		if !ok || selected != nil && selected != file {
			return nil, false
		}
		selected = file
	}
	return selected, selected != nil
}

func runPreparedCommand(_ context.Context, cmd *exec.Cmd) error {
	prepareCommand(cmd)
	return cmd.Run()
}
