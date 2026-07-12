package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

const (
	argonMemory      = 32 * 1024
	argonIterations  = 3
	argonParallelism = 1
	argonSaltLength  = 16
	argonKeyLength   = 32

	defaultPasswordHashConcurrency = 2
	maxPasswordHashConcurrency     = 16
)

type passwordKeyDeriver func(password, salt []byte) []byte

type passwordHashLimiter struct {
	slots chan struct{}
}

func newPasswordHashLimiter(limit int) *passwordHashLimiter {
	if limit < 1 {
		limit = defaultPasswordHashConcurrency
	}
	return &passwordHashLimiter{slots: make(chan struct{}, limit)}
}

func (l *passwordHashLimiter) run(ctx context.Context, work func()) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case l.slots <- struct{}{}:
		defer func() { <-l.slots }()
		work()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var processPasswordHashLimiter = struct {
	sync.RWMutex
	limiter *passwordHashLimiter
}{limiter: newPasswordHashLimiter(defaultPasswordHashConcurrency)}

func configurePasswordHashConcurrency(limit int) {
	processPasswordHashLimiter.Lock()
	processPasswordHashLimiter.limiter = newPasswordHashLimiter(limit)
	processPasswordHashLimiter.Unlock()
}

func currentPasswordHashLimiter() *passwordHashLimiter {
	processPasswordHashLimiter.RLock()
	limiter := processPasswordHashLimiter.limiter
	processPasswordHashLimiter.RUnlock()
	return limiter
}

func hashPasswordWithLimiter(ctx context.Context, password string, limiter *passwordHashLimiter, derive passwordKeyDeriver) (string, error) {
	if len(password) > 1024 {
		return "", fmt.Errorf("password is too long")
	}
	if limiter == nil || derive == nil {
		return "", fmt.Errorf("password hashing is not configured")
	}
	var salt, key []byte
	var workErr error
	if err := limiter.run(ctx, func() {
		salt = make([]byte, argonSaltLength)
		if _, workErr = rand.Read(salt); workErr != nil {
			return
		}
		key = derive([]byte(password), salt)
	}); err != nil {
		return "", err
	}
	if workErr != nil {
		return "", workErr
	}
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func hashPasswordContext(ctx context.Context, password string) (string, error) {
	return hashPasswordWithLimiter(ctx, password, currentPasswordHashLimiter(), func(password, salt []byte) []byte {
		return argon2.IDKey(password, salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	})
}

func hashPassword(password string) (string, error) {
	return hashPasswordContext(context.Background(), password)
}

func verifyPasswordContext(ctx context.Context, encoded, password string) (bool, bool) {
	if strings.HasPrefix(encoded, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(encoded), []byte(password)) == nil, true
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, false
	}
	var memory uint64
	var iterations uint64
	var parallelism uint64
	for _, field := range strings.Split(parts[3], ",") {
		pair := strings.SplitN(field, "=", 2)
		if len(pair) != 2 {
			return false, false
		}
		value, err := strconv.ParseUint(pair[1], 10, 32)
		if err != nil {
			return false, false
		}
		switch pair[0] {
		case "m":
			memory = value
		case "t":
			iterations = value
		case "p":
			parallelism = value
		}
	}
	if memory == 0 || memory > 256*1024 || iterations == 0 || iterations > 10 || parallelism == 0 || parallelism > 8 {
		return false, false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return false, false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) < 16 || len(want) > 64 {
		return false, false
	}
	var got []byte
	if err := currentPasswordHashLimiter().run(ctx, func() {
		got = argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), uint8(parallelism), uint32(len(want)))
	}); err != nil {
		return false, false
	}
	return subtle.ConstantTimeCompare(got, want) == 1, false
}

func verifyPassword(encoded, password string) (bool, bool) {
	return verifyPasswordContext(context.Background(), encoded, password)
}
