package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/j0j1j2/claude-conductor/internal/cli"
	"github.com/j0j1j2/claude-conductor/internal/exitcode"
)

func main() {
	exit := 99
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintln(os.Stderr, "conductor panic:", r)
			fmt.Fprintln(os.Stderr, string(debug.Stack()))
			os.Exit(exitcode.InternalError)
		}
		os.Exit(exit)
	}()

	executed, execErr := cli.ExecuteRoot()
	exit = 0
	if execErr != nil {
		fmt.Fprintln(os.Stderr, execErr)
		var ee *cli.ExitError
		if errors.As(execErr, &ee) {
			exit = ee.Code
		} else {
			exit = exitcode.InternalError
		}
	}
	if executed != nil {
		cli.WriteAudit(executed, os.Args[1:], exit)
	}
}
