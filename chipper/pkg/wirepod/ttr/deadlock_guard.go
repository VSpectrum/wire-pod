package wirepod_ttr

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/kercre123/wire-pod/chipper/pkg/logger"
)

// SafeSendBool sends a boolean to a channel non-blockingly.
// It returns true if delivered, false if the channel was nil or full.
func SafeSendBool(ch chan<- bool, val bool) bool {
	if ch == nil {
		return false
	}
	select {
	case ch <- val:
		return true
	default:
		return false
	}
}

// SafeSendString sends a string to a channel non-blockingly.
// It returns true if delivered, false if the channel was nil or full.
func SafeSendString(ch chan<- string, val string) bool {
	if ch == nil {
		return false
	}
	select {
	case ch <- val:
		return true
	default:
		return false
	}
}

// WaitBoolWithTimeout waits for a boolean from ch or times out.
// Returns (value, ok=true) if received, or (false, ok=false) on timeout/close.
func WaitBoolWithTimeout(ctx context.Context, ch <-chan bool, timeout time.Duration, opName string) (bool, bool) {
	if ch == nil {
		return false, false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case val, ok := <-ch:
		return val, ok
	case <-timer.C:
		logger.Println(fmt.Sprintf("[DeadlockGuard] Timeout waiting for '%s' after %v", opName, timeout))
		return false, false
	case <-ctx.Done():
		logger.Println(fmt.Sprintf("[DeadlockGuard] Context cancelled waiting for '%s': %v", opName, ctx.Err()))
		return false, false
	}
}

// WaitSignalWithTimeout waits for any value or close on ch, or times out.
// Returns true if signaled, false on timeout or context cancellation.
func WaitSignalWithTimeout(ctx context.Context, ch <-chan bool, timeout time.Duration, opName string) bool {
	_, ok := WaitBoolWithTimeout(ctx, ch, timeout, opName)
	return ok
}

// Watchdog tracks long-running operations and logs a deadlock alert with stack trace
// if the operation exceeds its configured deadline without being stopped.
type Watchdog struct {
	name      string
	timeout   time.Duration
	timer     *time.Timer
	stopCh    chan struct{}
	once      sync.Once
	onTimeout func()
}

// NewWatchdog creates and arms a new watchdog timer.
func NewWatchdog(name string, timeout time.Duration, onTimeout func()) *Watchdog {
	w := &Watchdog{
		name:      name,
		timeout:   timeout,
		stopCh:    make(chan struct{}),
		onTimeout: onTimeout,
	}

	w.timer = time.AfterFunc(timeout, func() {
		buf := make([]byte, 4096)
		n := runtime.Stack(buf, false)
		logger.Println(fmt.Sprintf("[DeadlockWatchdog:ALERT] Operation '%s' exceeded deadline %v! Possible hang or deadlock.\nStack dump:\n%s",
			w.name, w.timeout, string(buf[:n])))
		if w.onTimeout != nil {
			w.onTimeout()
		}
	})

	return w
}

// Stop disarms the watchdog.
func (w *Watchdog) Stop() {
	if w == nil {
		return
	}
	w.once.Do(func() {
		if w.timer != nil {
			w.timer.Stop()
		}
		close(w.stopCh)
	})
}

