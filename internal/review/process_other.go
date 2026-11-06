//go:build !unix

package review

import (
	"os/exec"
	"time"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.WaitDelay = 2 * time.Second
}
