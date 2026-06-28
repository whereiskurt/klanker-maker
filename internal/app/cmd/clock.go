package cmd

import "time"

// sleep is the package's wall-clock sleep seam. Production code calls sleep(d)
// instead of time.Sleep(d) so the test binary can replace it with a no-op
// (see TestMain in main_test.go), removing real wall-clock waits from the cmd
// test suite without changing any control flow — the sleeps still execute, they
// just return instantly under test.
//
// It is NOT a general clock abstraction: timing-sensitive production paths that
// need a real delay (e.g. the select/ticker liveness loops in shell.go) keep
// using time.After / time.NewTicker directly. Only the fixed "wait for an
// external thing to settle" time.Sleep calls route through this seam.
var sleep = time.Sleep
