package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"modernc.org/sqlite"
)

type sqliteOnlineBackuper interface {
	NewBackup(string) (*sqlite.Backup, error)
}

func createSQLiteOnlineBackup(ctx context.Context, db *sql.DB, destinationPath string) (returnErr error) {
	if db == nil || destinationPath == "" {
		return fmt.Errorf("database and destination path are required")
	}
	if _, err := os.Stat(destinationPath); err == nil {
		return fmt.Errorf("backup destination already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	defer func() {
		if returnErr != nil {
			_ = os.Remove(destinationPath)
		}
	}()
	connection, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	returnErr = connection.Raw(func(driverConnection any) error {
		backuper, ok := driverConnection.(sqliteOnlineBackuper)
		if !ok {
			return fmt.Errorf("sqlite driver does not support online backup")
		}
		backup, err := backuper.NewBackup(destinationPath)
		if err != nil {
			return err
		}
		finished := false
		finish := func() error {
			if finished {
				return nil
			}
			finished = true
			return backup.Finish()
		}
		defer func() {
			if !finished {
				_ = finish()
			}
		}()
		for {
			if err := ctx.Err(); err != nil {
				return errors.Join(err, finish())
			}
			more, stepErr := backup.Step(256)
			if stepErr != nil {
				return errors.Join(stepErr, finish())
			}
			if !more {
				break
			}
		}
		return finish()
	})
	return returnErr
}
