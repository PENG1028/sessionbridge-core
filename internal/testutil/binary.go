package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// EchoBinary compiles a Go binary that prints all os.Args[1:] joined by spaces to stdout.
func EchoBinary(t *testing.T) string {
	t.Helper()
	return compileHelper(t, `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	fmt.Println(strings.Join(os.Args[1:], " "))
}
`)
}

// CatBinary compiles a Go binary that reads stdin and writes to stdout.
func CatBinary(t *testing.T) string {
	t.Helper()
	return compileHelper(t, `package main

import (
	"io"
	"os"
)

func main() {
	io.Copy(os.Stdout, os.Stdin)
}
`)
}

// SleepBinary compiles a Go binary that sleeps for N seconds (first arg).
func SleepBinary(t *testing.T) string {
	t.Helper()
	return compileHelper(t, `package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		return
	}
	n, err := strconv.Atoi(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid duration: %v", err)
		os.Exit(1)
	}
	time.Sleep(time.Duration(n) * time.Second)
}
`)
}

func compileHelper(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go compiler not available")
	}
	dir := t.TempDir()
	main := filepath.Join(dir, "main.go")
	if err := os.WriteFile(main, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "helper.exe")
	cmd := exec.Command("go", "build", "-o", bin, main)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return bin
}
