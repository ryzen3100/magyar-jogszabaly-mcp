// TestMain turns the runtime DB-backed skip guards into one loud stderr
// notice, so a green run can never silently cover zero DB tests (T6).

package tools

import (
	"fmt"
	"os"
	"testing"
)

// dbSkippedTests is incremented by every DB-backed skip site in this package.
var dbSkippedTests int

func TestMain(m *testing.M) {
	code := m.Run()
	if dbSkippedTests > 0 {
		fmt.Fprintf(os.Stderr, "SKIP NOTICE: %d DB-backed tests skipped — data/database.db unavailable\n", dbSkippedTests)
	}
	os.Exit(code)
}
