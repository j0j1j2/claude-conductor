package exitcode

import "testing"

func TestConstants(t *testing.T) {
	cases := map[string]struct {
		got  int
		want int
	}{
		"OK":            {OK, 0},
		"Timeout":       {Timeout, 2},
		"Crash":         {Crash, 3},
		"UnknownSlave":  {UnknownSlave, 4},
		"Busy":          {Busy, 5},
		"NotInTmux":     {NotInTmux, 10},
		"InternalError": {InternalError, 99},
	}
	for name, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", name, c.got, c.want)
		}
	}
}
