package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/cloudchamb3r/claude-conductor/internal/cli"
	"github.com/cloudchamb3r/claude-conductor/internal/exitcode"
)

func main() {
	executed, execErr := cli.ExecuteRoot()
	exit := 0
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
	os.Exit(exit)
}
