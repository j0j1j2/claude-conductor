package cli

import "testing"

func TestSendCmd_flags(t *testing.T) {
	f := sendCmd.Flags()
	if f.Lookup("timeout") == nil {
		t.Error("missing --timeout flag")
	}
	if f.Lookup("quiet") == nil {
		t.Error("missing --quiet flag")
	}
}
