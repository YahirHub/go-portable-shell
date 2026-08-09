//go:build windows

package portablesh

import (
	"os/exec"
	"syscall"
	"time"
)

const createNewProcessGroup = 0x00000200

func prepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
	cmd.WaitDelay = 2 * time.Second
}
