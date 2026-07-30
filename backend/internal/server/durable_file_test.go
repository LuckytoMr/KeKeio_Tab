package server

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFileAtomicDurableReplacesTargetAndCleansTemporaryFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("write old target: %v", err)
	}

	if err := writeFileAtomicDurable(path, ".durable-test-*", 0o600, func(writer io.Writer) error {
		_, err := writer.Write([]byte("new"))
		return err
	}); err != nil {
		t.Fatalf("replace target: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read replaced target: %v", err)
	}
	if string(raw) != "new" {
		t.Fatalf("target content = %q, want new", raw)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat replaced target: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("target mode = %o, want 600", info.Mode().Perm())
		}
	}
	assertNoDurableTestTemporaryFiles(t, directory)
}

func TestWriteFileAtomicDurablePreservesTargetWhenWriterFails(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("write old target: %v", err)
	}
	writeErr := errors.New("injected write failure")

	err := writeFileAtomicDurable(path, ".durable-test-*", 0o600, func(writer io.Writer) error {
		if _, err := writer.Write([]byte("partial")); err != nil {
			return err
		}
		return writeErr
	})
	if !errors.Is(err, writeErr) {
		t.Fatalf("write failure = %v, want injected error", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read preserved target: %v", err)
	}
	if string(raw) != "old" {
		t.Fatalf("target content = %q, want old", raw)
	}
	assertNoDurableTestTemporaryFiles(t, directory)
}

func TestAdminV1CopyFileAtomicReplacesTarget(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.sqlite")
	destinationPath := filepath.Join(directory, "staged.sqlite")
	if err := os.WriteFile(sourcePath, []byte("snapshot"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(destinationPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale destination: %v", err)
	}

	if err := adminV1CopyFileAtomic(sourcePath, destinationPath, 0o600); err != nil {
		t.Fatalf("copy file atomically: %v", err)
	}
	raw, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatalf("read copied destination: %v", err)
	}
	if string(raw) != "snapshot" {
		t.Fatalf("copied content = %q, want snapshot", raw)
	}
}

func TestRenameAndRemoveFileDurableAcrossDirectories(t *testing.T) {
	root := t.TempDir()
	sourceDirectory := filepath.Join(root, "source")
	destinationDirectory := filepath.Join(root, "destination")
	if err := os.MkdirAll(sourceDirectory, 0o700); err != nil {
		t.Fatalf("create source directory: %v", err)
	}
	if err := os.MkdirAll(destinationDirectory, 0o700); err != nil {
		t.Fatalf("create destination directory: %v", err)
	}
	sourcePath := filepath.Join(sourceDirectory, "state")
	destinationPath := filepath.Join(destinationDirectory, "state")
	if err := os.WriteFile(sourcePath, []byte("durable"), 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	if err := renameFileDurable(sourcePath, destinationPath); err != nil {
		t.Fatalf("rename file durably: %v", err)
	}
	if _, err := os.Stat(sourcePath); !os.IsNotExist(err) {
		t.Fatalf("source survived rename: %v", err)
	}
	if err := removeFileDurable(destinationPath); err != nil {
		t.Fatalf("remove file durably: %v", err)
	}
	if _, err := os.Stat(destinationPath); !os.IsNotExist(err) {
		t.Fatalf("destination survived remove: %v", err)
	}
	if err := removeFileDurable(destinationPath); err != nil {
		t.Fatalf("remove missing file: %v", err)
	}
}

func assertNoDurableTestTemporaryFiles(t *testing.T, directory string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, ".durable-test-*"))
	if err != nil {
		t.Fatalf("glob temporary files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files survived: %v", matches)
	}
}
