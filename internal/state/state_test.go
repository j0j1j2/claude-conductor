package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestValidateSlaveID(t *testing.T) {
	good := []string{"s1", "s99", "worker-a", "abc_123", "A"}
	bad := []string{
		"", "s/1", "../master", "master", "sessions", "audit.log",
		"-flag", "_internal_x", "s 1", "x.y",
		strings.Repeat("a", 33), // too long
	}
	for _, g := range good {
		if err := ValidateSlaveID(g); err != nil {
			t.Errorf("ValidateSlaveID(%q) unexpectedly rejected: %v", g, err)
		}
	}
	for _, b := range bad {
		if err := ValidateSlaveID(b); err == nil {
			t.Errorf("ValidateSlaveID(%q) should reject", b)
		}
	}
}

func TestSlaveDir_invalidIDReturnsEmpty(t *testing.T) {
	if got := SlaveDir("foo", "../escape"); got != "" {
		t.Errorf("SlaveDir with traversal returned %q, want empty", got)
	}
}

func TestSessionDir(t *testing.T) {
	home := tempHome(t)
	got := SessionDir("foo")
	want := filepath.Join(home, ".conductor", "sessions", "foo")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSlaveDir(t *testing.T) {
	home := tempHome(t)
	got := SlaveDir("foo", "s1")
	want := filepath.Join(home, ".conductor", "sessions", "foo", "s1")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCreatePending_exclusive(t *testing.T) {
	tempHome(t)
	slaveDir := SlaveDir("s", "s1")
	if err := os.MkdirAll(slaveDir, 0o755); err != nil {
		t.Fatal(err)
	}

	turnA, err := CreatePending(slaveDir)
	if err != nil {
		t.Fatalf("first CreatePending failed: %v", err)
	}
	if turnA == "" {
		t.Fatal("expected non-empty turn id")
	}
	if _, err := CreatePending(slaveDir); err == nil {
		t.Fatal("second CreatePending should fail (busy)")
	}

	if err := RemovePending(slaveDir); err != nil {
		t.Fatalf("RemovePending failed: %v", err)
	}
	turnB, err := CreatePending(slaveDir)
	if err != nil {
		t.Fatalf("CreatePending after remove failed: %v", err)
	}
	if turnA == turnB {
		t.Error("expected distinct turn ids across separate locks")
	}
}

func TestCreatePending_stalePidIsCleared(t *testing.T) {
	tempHome(t)
	slaveDir := SlaveDir("s", "s1")
	if err := os.MkdirAll(slaveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slaveDir, ".pending"), []byte("999999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CreatePending(slaveDir); err != nil {
		t.Fatalf("CreatePending should clear stale PID lock, got: %v", err)
	}
}

func TestCreatePending_livePidStillBusy(t *testing.T) {
	tempHome(t)
	slaveDir := SlaveDir("s", "s1")
	if err := os.MkdirAll(slaveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := PendingLock{PID: os.Getpid(), TurnID: "abc"}
	b, _ := json.Marshal(lock)
	if err := os.WriteFile(filepath.Join(slaveDir, ".pending"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CreatePending(slaveDir); err == nil {
		t.Fatal("expected ErrBusy when current process holds the lock")
	}
}

func TestWriteReadDone_turnID(t *testing.T) {
	tempHome(t)
	slaveDir := SlaveDir("s", "s1")
	if err := os.MkdirAll(slaveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteDone(slaveDir, "turn-xyz", "hello"); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDone(slaveDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.TurnID != "turn-xyz" || got.Text != "hello" {
		t.Errorf("got %+v, want {turn-xyz, hello}", got)
	}
}

func TestReadDone_legacyOrMalformedHasEmptyTurnID(t *testing.T) {
	// Malformed / legacy .done files no longer trick the caller into
	// accepting them — ReadDone returns an empty TurnID so tryFinish
	// treats it as a mismatch.
	tempHome(t)
	slaveDir := SlaveDir("s", "s1")
	if err := os.MkdirAll(slaveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slaveDir, ".done"), []byte("legacy content"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDone(slaveDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.TurnID != "" {
		t.Errorf("expected empty TurnID for non-JSON .done, got %q", got.TurnID)
	}
}

func TestSlaveExists(t *testing.T) {
	tempHome(t)
	if SlaveExists("s", "s1") {
		t.Error("should not exist yet")
	}
	if err := os.MkdirAll(SlaveDir("s", "s1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !SlaveExists("s", "s1") {
		t.Error("should exist now")
	}
}

// silence go vet about unused import
var _ = fmt.Sprintf
