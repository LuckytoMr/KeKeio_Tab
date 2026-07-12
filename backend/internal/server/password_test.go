package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestHashPasswordWithLimiterCapsConcurrentDerivations(t *testing.T) {
	limiter := newPasswordHashLimiter(1)
	started := make(chan int32, 2)
	releaseFirst := make(chan struct{})
	results := make(chan error, 2)
	var calls atomic.Int32
	derive := func(_, _ []byte) []byte {
		call := calls.Add(1)
		started <- call
		if call == 1 {
			<-releaseFirst
		}
		return make([]byte, argonKeyLength)
	}

	go func() {
		_, err := hashPasswordWithLimiter(context.Background(), "first-safe-password", limiter, derive)
		results <- err
	}()
	select {
	case call := <-started:
		if call != 1 {
			t.Fatalf("first derivation call = %d", call)
		}
	case <-time.After(time.Second):
		t.Fatal("first derivation did not start")
	}

	go func() {
		_, err := hashPasswordWithLimiter(context.Background(), "second-safe-password", limiter, derive)
		results <- err
	}()
	secondStartedEarly := false
	select {
	case <-started:
		secondStartedEarly = true
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	if !secondStartedEarly {
		select {
		case call := <-started:
			if call != 2 {
				t.Fatalf("second derivation call = %d", call)
			}
		case <-time.After(time.Second):
			t.Fatal("second derivation did not start after the slot was released")
		}
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("hash password: %v", err)
		}
	}
	if secondStartedEarly {
		t.Fatal("password derivations exceeded the configured concurrency")
	}
}

func TestHashPasswordWithLimiterStopsWaitingWhenRequestIsCanceled(t *testing.T) {
	limiter := newPasswordHashLimiter(1)
	occupied := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = limiter.run(context.Background(), func() {
			close(occupied)
			<-release
		})
	}()
	<-occupied

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var derived atomic.Bool
	_, err := hashPasswordWithLimiter(ctx, "safe-password", limiter, func(_, _ []byte) []byte {
		derived.Store(true)
		return make([]byte, argonKeyLength)
	})
	close(release)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled hash wait error = %v, want context.Canceled", err)
	}
	if derived.Load() {
		t.Fatal("password derivation ran after its request was canceled")
	}
}

func TestVerifyPasswordContextSharesProcessWideArgonLimiter(t *testing.T) {
	defer configurePasswordHashConcurrency(defaultPasswordHashConcurrency)
	configurePasswordHashConcurrency(1)
	limiter := currentPasswordHashLimiter()
	occupied := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = limiter.run(context.Background(), func() {
			close(occupied)
			<-release
		})
	}()
	<-occupied

	encoded := fmt.Sprintf("$argon2id$v=19$m=8,t=1,p=1$%s$%s",
		base64.RawStdEncoding.EncodeToString(make([]byte, 16)),
		base64.RawStdEncoding.EncodeToString(make([]byte, 16)),
	)
	result := make(chan bool, 1)
	go func() {
		valid, _ := verifyPasswordContext(context.Background(), encoded, "password")
		result <- valid
	}()
	completedBeforeRelease := false
	select {
	case <-result:
		completedBeforeRelease = true
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if !completedBeforeRelease {
		select {
		case <-result:
		case <-time.After(time.Second):
			t.Fatal("password verification did not resume after the Argon slot was released")
		}
	}
	if completedBeforeRelease {
		t.Fatal("password verification bypassed the process-wide Argon limiter")
	}
}
