package protocol

import "testing"

// TestNormalizeWaitMs covers the shared wait policy: non-positive values fall
// back to DefaultWaitMs, a positive maxMs caps, and maxMs <= 0 means uncapped.
func TestNormalizeWaitMs(t *testing.T) {
	cases := []struct {
		name      string
		ms, maxMs int
		want      int
	}{
		{"zero defaults", 0, MaxWaitMs, DefaultWaitMs},
		{"negative defaults", -5, MaxWaitMs, DefaultWaitMs},
		{"under cap passes through", 500, MaxWaitMs, 500},
		{"over cap clamps", MaxWaitMs + 1, MaxWaitMs, MaxWaitMs},
		{"uncapped when maxMs zero", MaxWaitMs * 2, 0, MaxWaitMs * 2},
		{"default still applies when uncapped", 0, 0, DefaultWaitMs},
	}
	for _, c := range cases {
		if got := NormalizeWaitMs(c.ms, c.maxMs); got != c.want {
			t.Errorf("%s: NormalizeWaitMs(%d, %d) = %d, want %d", c.name, c.ms, c.maxMs, got, c.want)
		}
	}
}

// TestSleepWaitMessage verifies SleepWait returns the shared completion
// message for the normalized duration (small values only — the 60s cap and
// 1s default are covered by NormalizeWaitMs and the MCP/daemon call sites).
func TestSleepWaitMessage(t *testing.T) {
	if got := SleepWait(1); got != FormatWaitResult(1) {
		t.Errorf("SleepWait(1) = %q, want %q", got, FormatWaitResult(1))
	}
}
