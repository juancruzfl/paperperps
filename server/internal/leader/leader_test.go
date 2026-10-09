package leader

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestOnlyOneHolder(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	const key = 42_4242
	ctx := context.Background()

	first, err := Acquire(ctx, url, key, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	// A second process must wait while the first holds the lock.
	waitCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if _, err := Acquire(waitCtx, url, key, 50*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second Acquire err = %v, want deadline exceeded", err)
	}

	// Once released, the next waiter gets it.
	acquired := make(chan *Lock, 1)
	go func() {
		l, err := Acquire(ctx, url, key, 50*time.Millisecond)
		if err != nil {
			t.Error(err)
			close(acquired)
			return
		}
		acquired <- l
	}()
	time.Sleep(100 * time.Millisecond)
	if err := first.Release(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case second := <-acquired:
		if second != nil {
			second.Release(ctx)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter did not get the lock after release")
	}
}

func TestWatchReportsLostConnection(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	l, err := Acquire(ctx, url, 42_4243, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	l.conn.Close(ctx) // simulate the connection dropping

	done := make(chan error, 1)
	go func() { done <- l.Watch(ctx, 50*time.Millisecond) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Watch returned nil, want error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not notice the lost connection")
	}
}
