package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/cloudchamb3r/claude-conductor/cmd"
	"github.com/cloudchamb3r/claude-conductor/internal/exitcode"
)

func main() {
	executed, execErr := cmd.ExecuteRoot()
	exit := 0
	if execErr != nil {
		fmt.Fprintln(os.Stderr, execErr)
		var ee *cmd.ExitError
		if errors.As(execErr, &ee) {
			exit = ee.Code
		} else {
			exit = exitcode.InternalError
		}
	}
	if executed != nil {
		cmd.WriteAudit(executed, os.Args[1:], exit)
	}
	os.Exit(exit)
}
