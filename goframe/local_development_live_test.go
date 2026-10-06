package goframe

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	talon "github.com/darkmice/talon-sdk-go"
)

// Verify actual process death rather than relying on Close to flush the WAL.
func TestLocalDevelopmentCommitKillRestart(t *testing.T) {
	if os.Getenv("TALON_TEST_LOCAL_DEVELOPMENT") != "1" {
		t.Skip("requires explicit real local development config")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"binary", "result_v2", "goframe"} {
		t.Run(route, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "db")
			for _, phase := range []string{"write", "read"} {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				cmd := exec.CommandContext(ctx, executable, "-test.run=^TestLocalDevelopmentKillRestartChild$", "-test.v")
				cmd.Env = append(os.Environ(), "TALON_DEV_RESTART_ROUTE="+route, "TALON_DEV_RESTART_PHASE="+phase, "TALON_DEV_RESTART_PATH="+path)
				output, err := cmd.CombinedOutput()
				cancel()
				t.Logf("%s: %s", phase, output)
				if phase == "write" {
					var exit *exec.ExitError
					if !errors.As(err, &exit) {
						t.Fatalf("expected SIGKILL, got %v", err)
					}
					status, ok := exit.Sys().(syscall.WaitStatus)
					if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
						t.Fatalf("child did not terminate by SIGKILL: %v", err)
					}
				} else if err != nil {
					t.Fatalf("restart read: %v", err)
				}
			}
		})
	}
}

func TestLocalDevelopmentKillRestartChild(t *testing.T) {
	phase := os.Getenv("TALON_DEV_RESTART_PHASE")
	if phase == "" {
		t.Skip("subprocess helper")
	}
	db := openContractSQL(t, os.Getenv("TALON_DEV_RESTART_ROUTE"), os.Getenv("TALON_DEV_RESTART_PATH"))
	if phase == "write" {
		contractExec(t, db, "CREATE TABLE durable_dev (id INTEGER PRIMARY KEY, operation_id TEXT, value TEXT)")
		contractExec(t, db, "CREATE INDEX durable_dev_operation ON durable_dev(operation_id)")
		if err := db.begin(); err != nil {
			t.Fatal(err)
		}
		contractExec(t, db, "INSERT INTO durable_dev VALUES (?, ?, ?)", talon.IntegerValue(1), contractText(t, "receipt-1"), contractText(t, "committed"))
		if err := db.commit(); err != nil {
			t.Fatal(err)
		}
		if err := db.begin(); err != nil {
			t.Fatal(err)
		}
		contractExec(t, db, "INSERT INTO durable_dev VALUES (?, ?, ?)", talon.IntegerValue(2), contractText(t, "receipt-2"), contractText(t, "uncommitted"))
		process, err := os.FindProcess(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		if err := process.Kill(); err != nil {
			t.Fatal(err)
		}
		t.Fatal("SIGKILL unexpectedly returned")
	}
	defer db.close()
	assertContractRows(t, contractRows(t, db, "SELECT value FROM durable_dev WHERE operation_id = ?", contractText(t, "receipt-1")), "committed")
	assertContractRows(t, contractRows(t, db, "SELECT value FROM durable_dev WHERE operation_id = ?", contractText(t, "receipt-2")))
	assertContractRows(t, contractRows(t, db, "SELECT value FROM durable_dev"), "committed")
}
