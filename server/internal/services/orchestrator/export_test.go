package orchestrator

import "time"

const DispatchBatch = dispatchBatch

var DispatchLoop = dispatchLoop

var Consume = consume

// SetAutoClaim shortens consume's claim timings for a test.
func SetAutoClaim(t interface{ Cleanup(func()) }, interval, idle time.Duration) {
	oldInterval, oldIdle := autoClaimInterval, autoClaimIdle
	autoClaimInterval, autoClaimIdle = interval, idle
	t.Cleanup(func() { autoClaimInterval, autoClaimIdle = oldInterval, oldIdle })
}
