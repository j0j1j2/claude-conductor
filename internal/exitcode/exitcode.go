// Package exitcode defines conductor CLI exit codes.
package exitcode

const (
	OK            = 0
	Timeout       = 2
	Crash         = 3
	UnknownSlave  = 4
	Busy          = 5
	NotInTmux     = 10
	InternalError = 99
)
