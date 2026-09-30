package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fetchWorld is a copy of what Herdr's build step sees — the manifest and
// the script — plus a fake release directory served over file://, and a PATH
// holding only the tools the script uses, so Go can be taken away.
type fetchWorld struct {
	t              *testing.T
	root, release  string
	path           string
	asset, version string
}

func newFetchWorld(t *testing.T, withGo bool) *fetchWorld {
	t.Helper()
	w := &fetchWorld{t: t, root: t.TempDir(), release: t.TempDir(), version: "9.9.9"}
	for _, f := range []string{"scripts/fetch.sh"} {
		b, err := os.ReadFile(filepath.Join("..", "..", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(w.root, filepath.Dir(f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(w.root, f), b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(w.root, "herdr-plugin.toml"),
		[]byte("id = \"herdr-context\"\nversion = \""+w.version+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.asset = "herdr-context-" + runtime.GOOS + "-" + runtime.GOARCH
	// A PATH of only the tools the script needs: no go unless asked for.
	bin := t.TempDir()
	tools := []string{"sh", "sed", "head", "uname", "curl", "awk", "cut", "mktemp", "rm", "mkdir", "chmod", "mv", "sha256sum", "shasum"}
	if withGo {
		tools = append(tools, "go")
	}
	for _, tool := range tools {
		if p, err := exec.LookPath(tool); err == nil {
			if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(bin, "curl")); err != nil {
		t.Skip("curl is not installed")
	}
	w.path = bin
	return w
}

// publish puts binary and a checksums.txt listing sum into the fake release.
func (w *fetchWorld) publish(binary []byte, sum string) {
	w.t.Helper()
	dir := filepath.Join(w.release, "v"+w.version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, w.asset), binary, 0o644); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(sum+"  "+w.asset+"\n"), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

func (w *fetchWorld) run() (string, error) {
	cmd := exec.Command("sh", "scripts/fetch.sh")
	cmd.Dir = w.root
	cmd.Env = []string{"PATH=" + w.path, "HOME=" + w.root, "HERDR_CONTEXT_BASE=file://" + w.release}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func shaHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestFetchInstallsTheReleasesBinary(t *testing.T) {
	w := newFetchWorld(t, false)
	binary := []byte("#!/bin/sh\necho prebuilt\n")
	w.publish(binary, shaHex(binary))
	out, err := w.run()
	if err != nil {
		t.Fatalf("fetch failed: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(w.root, "bin", "herdr-context.exe"))
	if err != nil || string(got) != string(binary) {
		t.Fatalf("installed %q, %v", got, err)
	}
	if fi, _ := os.Stat(filepath.Join(w.root, "bin", "herdr-context.exe")); fi.Mode().Perm()&0o100 == 0 {
		t.Fatal("the installed binary is not executable")
	}
}

func TestFetchRefusesABinaryThatFailsItsChecksum(t *testing.T) {
	w := newFetchWorld(t, false)
	w.publish([]byte("tampered"), shaHex([]byte("the real one")))
	out, err := w.run()
	if err == nil || !strings.Contains(out, "does not match the release's checksum") {
		t.Fatalf("a mismatched binary was accepted: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(w.root, "bin", "herdr-context.exe")); err == nil {
		t.Fatal("a mismatched binary was installed")
	}
}

func TestFetchWithoutAReleaseOrGoFailsClearly(t *testing.T) {
	w := newFetchWorld(t, false) // nothing published
	out, err := w.run()
	if err == nil || !strings.Contains(out, "Go is not installed") {
		t.Fatalf("want a clear failure, got %v\n%s", err, out)
	}
}
