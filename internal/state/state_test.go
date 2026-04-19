package state

import (
	"os"
	"path/filepath"
	"testing"
)

func tempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
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

	if err := CreatePending(slaveDir); err != nil {
		t.Fatalf("first CreatePending failed: %v", err)
	}
	if err := CreatePending(slaveDir); err == nil {
		t.Fatal("second CreatePending should fail (busy)")
	}

	if err := RemovePending(slaveDir); err != nil {
		t.Fatalf("RemovePending failed: %v", err)
	}
	if err := CreatePending(slaveDir); err != nil {
		t.Fatalf("CreatePending after remove failed: %v", err)
	}
}

func TestWriteReadDone(t *testing.T) {
	tempHome(t)
	slaveDir := SlaveDir("s", "s1")
	if err := os.MkdirAll(slaveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteDone(slaveDir, "hello"); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDone(slaveDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
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
