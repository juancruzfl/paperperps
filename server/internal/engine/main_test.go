package engine

import (
	"fmt"
	"os"
	"testing"

	"github.com/juancruzfl/paperperps/server/internal/dbtest"
)

// TestMain points the engine tests at a fresh database. Each test engine loads every
// account with open orders or positions at startup, so a shared, ever-growing database
// would make the suite slower with every run.
func TestMain(m *testing.M) {
	url, drop, ok, err := dbtest.Create("paperperps_engine")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create test database:", err)
		os.Exit(1)
	}
	if ok {
		os.Setenv("TEST_DATABASE_URL", url)
	}
	code := m.Run()
	drop()
	os.Exit(code)
}
