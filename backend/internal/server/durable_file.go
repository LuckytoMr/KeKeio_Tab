package server

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// writeFileAtomicDurable 先把内容和权限同步到同目录临时文件，再原子替换目标，
// 最后同步父目录，避免断电后目录项回退到替换前的状态。
func writeFileAtomicDurable(path, temporaryPattern string, mode os.FileMode, write func(io.Writer) error) error {
	if path == "" || write == nil {
		return fmt.Errorf("file path and writer are required")
	}
	directoryPath := filepath.Dir(path)
	if err := os.MkdirAll(directoryPath, 0o700); err != nil {
		return err
	}
	if temporaryPattern == "" {
		temporaryPattern = ".atomic-write-*"
	}
	temporary, err := os.CreateTemp(directoryPath, temporaryPattern)
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	temporaryClosed := false
	defer func() {
		if !temporaryClosed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if err := write(temporary); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	temporaryClosed = true
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}

	// 临时文件已经具有目标权限；再次设置可兼容不同平台的重命名语义。
	chmodErr := os.Chmod(path, mode)
	return errors.Join(chmodErr, syncDirectoryDurable(directoryPath))
}

func renameFileDurable(source, destination string) error {
	if source == "" || destination == "" {
		return fmt.Errorf("rename paths are required")
	}
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	sourceDirectory := filepath.Dir(source)
	destinationDirectory := filepath.Dir(destination)
	sourceSyncErr := syncDirectoryDurable(sourceDirectory)
	if sameDirectoryPath(sourceDirectory, destinationDirectory) {
		return sourceSyncErr
	}
	return errors.Join(sourceSyncErr, syncDirectoryDurable(destinationDirectory))
}

func removeFileDurable(path string) error {
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncDirectoryDurable(filepath.Dir(path))
}

func syncDirectoryDurable(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if runtime.GOOS == "windows" {
		// Windows 文件系统对目录句柄的 FlushFileBuffers 支持不一致；
		// 仍然尝试同步，但不因平台不支持目录 flush 而阻断正常写入。
		syncErr = nil
	}
	return errors.Join(syncErr, closeErr)
}

func sameDirectoryPath(left, right string) bool {
	leftAbsolute, leftErr := filepath.Abs(filepath.Clean(left))
	rightAbsolute, rightErr := filepath.Abs(filepath.Clean(right))
	if leftErr != nil || rightErr != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(leftAbsolute, rightAbsolute)
	}
	return leftAbsolute == rightAbsolute
}
