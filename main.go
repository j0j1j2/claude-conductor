package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/cloudchamb3r/claude-conductor/cmd"
	"github.com/cloudchamb3r/claude-conductor/internal/exitcode"
)

func main() {
	err := cmd.Root.Execute()
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	var ee *cmd.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.Code)
	}
	os.Exit(exitcode.InternalError)
}
