package wirepod_ttr

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fforchino/vector-go-sdk/pkg/vector"
	"github.com/fforchino/vector-go-sdk/pkg/vectorpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// TestDeadlockGuard_SafeSend verifies that SafeSendBool and SafeSendString
// never block or panic, even when channels are nil, unbuffered, or full.
func TestDeadlockGuard_SafeSend(t *testing.T) {
	// 1. Nil channel
	if SafeSendBool(nil, true) {
		t.Fatal("expected SafeSendBool(nil) to return false")
	}
	if SafeSendString(nil, "test") {
		t.Fatal("expected SafeSendString(nil) to return false")
	}

	// 2. Unbuffered channel with no reader - must not block
	unbufBool := make(chan bool)
	done := make(chan bool)
	go func() {
		delivered := SafeSendBool(unbufBool, true)
		if delivered {
			t.Errorf("expected undelivered for unbuffered channel with no reader")
		}
		done <- true
	}()

	select {
	case <-done:
		// success: did not block
	case <-time.After(200 * time.Millisecond):
		t.Fatal("SafeSendBool deadlocked on unbuffered channel!")
	}

	// 3. Full buffered channel - must drop rather than block
	bufBool := make(chan bool, 1)
	bufBool <- true // fill it
	if SafeSendBool(bufBool, false) {
		t.Fatal("expected SafeSendBool to return false when buffer is full")
	}
}

// TestDeadlockGuard_WaitTimeouts verifies that WaitBoolWithTimeout and WaitSignalWithTimeout
// cleanly timeout and return rather than blocking indefinitely.
func TestDeadlockGuard_WaitTimeouts(t *testing.T) {
	ctx := context.Background()

	// 1. Channel that never receives - must timeout cleanly
	emptyCh := make(chan bool)
	start := time.Now()
	val, ok := WaitBoolWithTimeout(ctx, emptyCh, 50*time.Millisecond, "emptyChTest")
	elapsed := time.Since(start)

	if ok || val {
		t.Fatalf("expected timeout with ok=false, got val=%v, ok=%v", val, ok)
	}
	if elapsed < 40*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Fatalf("timeout duration out of expected range: %v", elapsed)
	}

	// 2. Channel that receives in time
	readyCh := make(chan bool, 1)
	readyCh <- true
	val, ok = WaitBoolWithTimeout(ctx, readyCh, 50*time.Millisecond, "readyChTest")
	if !ok || !val {
		t.Fatalf("expected val=true, ok=true, got val=%v, ok=%v", val, ok)
	}

	// 3. Context cancelled before timeout
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	val, ok = WaitBoolWithTimeout(cancelCtx, emptyCh, 1*time.Second, "cancelledTest")
	if ok {
		t.Fatal("expected ok=false for cancelled context")
	}
}

// TestDeadlockGuard_Watchdog verifies that Watchdog fires its alert if not disarmed,
// and does not fire if disarmed in time.
func TestDeadlockGuard_Watchdog(t *testing.T) {
	var fired atomic.Bool

	// 1. Armed watchdog that triggers
	wd := NewWatchdog("TestWatchdogTrigger", 40*time.Millisecond, func() {
		fired.Store(true)
	})
	time.Sleep(70 * time.Millisecond)
	wd.Stop()

	if !fired.Load() {
		t.Fatal("expected watchdog to fire when deadline was exceeded")
	}

	// 2. Disarmed watchdog before deadline
	fired.Store(false)
	wd2 := NewWatchdog("TestWatchdogDisarm", 100*time.Millisecond, func() {
		fired.Store(true)
	})
	time.Sleep(10 * time.Millisecond)
	wd2.Stop()
	time.Sleep(120 * time.Millisecond)

	if fired.Load() {
		t.Fatal("watchdog fired even though it was stopped before deadline")
	}
}

// --- Mock gRPC Client for BehaviorControl Testing ---

type mockBehaviorControlClient struct {
	grpc.ClientStream
	sendFn func(*vectorpb.BehaviorControlRequest) error
	recvFn func() (*vectorpb.BehaviorControlResponse, error)
}

func (m *mockBehaviorControlClient) Send(req *vectorpb.BehaviorControlRequest) error {
	if m.sendFn != nil {
		return m.sendFn(req)
	}
	return nil
}

func (m *mockBehaviorControlClient) Recv() (*vectorpb.BehaviorControlResponse, error) {
	if m.recvFn != nil {
		return m.recvFn()
	}
	return nil, io.EOF
}

func (m *mockBehaviorControlClient) Header() (metadata.MD, error) { return nil, nil }
func (m *mockBehaviorControlClient) Trailer() metadata.MD         { return nil }
func (m *mockBehaviorControlClient) CloseSend() error             { return nil }
func (m *mockBehaviorControlClient) Context() context.Context     { return context.Background() }
func (m *mockBehaviorControlClient) SendMsg(interface{}) error    { return nil }
func (m *mockBehaviorControlClient) RecvMsg(interface{}) error    { return nil }

type mockExternalInterfaceClient struct {
	vectorpb.ExternalInterfaceClient
	behaviorControlFn func(context.Context, ...grpc.CallOption) (vectorpb.ExternalInterface_BehaviorControlClient, error)
}

func (m *mockExternalInterfaceClient) BehaviorControl(ctx context.Context, opts ...grpc.CallOption) (vectorpb.ExternalInterface_BehaviorControlClient, error) {
	if m.behaviorControlFn != nil {
		return m.behaviorControlFn(ctx, opts...)
	}
	return nil, errors.New("unimplemented")
}

// TestDeadlock_BControlFailure_UnblocksCaller verifies that when robot.Conn.BehaviorControl
// fails (network failure, TLS failure, or RPC error), start receives false and caller NEVER hangs.
func TestDeadlock_BControlFailure_UnblocksCaller(t *testing.T) {
	mockClient := &mockExternalInterfaceClient{
		behaviorControlFn: func(ctx context.Context, opts ...grpc.CallOption) (vectorpb.ExternalInterface_BehaviorControlClient, error) {
			return nil, errors.New("simulated network connection refused")
		},
	}
	rob := &vector.Vector{Conn: mockClient}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := make(chan bool, 1)
	stop := make(chan bool, 1)

	BControl(rob, ctx, start, stop)

	select {
	case granted := <-start:
		if granted {
			t.Fatal("expected granted=false on BehaviorControl connection error")
		}
		// Success: caller was notified of failure within deadline!
	case <-time.After(500 * time.Millisecond):
		t.Fatal("DEADLOCK: caller hung waiting on start channel after BehaviorControl failed!")
	}
}

// TestDeadlock_BControlRecvFailure_UnblocksCaller verifies that when BehaviorControl starts
// but the stream errors before granting control, start receives false and caller NEVER hangs.
func TestDeadlock_BControlRecvFailure_UnblocksCaller(t *testing.T) {
	stream := &mockBehaviorControlClient{
		sendFn: func(req *vectorpb.BehaviorControlRequest) error { return nil },
		recvFn: func() (*vectorpb.BehaviorControlResponse, error) {
			return nil, errors.New("simulated stream closed unexpectedly")
		},
	}
	mockClient := &mockExternalInterfaceClient{
		behaviorControlFn: func(ctx context.Context, opts ...grpc.CallOption) (vectorpb.ExternalInterface_BehaviorControlClient, error) {
			return stream, nil
		},
	}
	rob := &vector.Vector{Conn: mockClient}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := make(chan bool, 1)
	stop := make(chan bool, 1)

	BControl(rob, ctx, start, stop)

	select {
	case granted := <-start:
		if granted {
			t.Fatal("expected granted=false when stream breaks before grant")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("DEADLOCK: caller hung waiting on start channel after stream broken!")
	}
}

// TestDeadlock_BControlSuccessAndRelease verifies normal control acquisition and release,
// ensuring the goroutine terminates cleanly when stop is signaled.
func TestDeadlock_BControlSuccessAndRelease(t *testing.T) {
	released := make(chan bool, 1)
	stream := &mockBehaviorControlClient{
		sendFn: func(req *vectorpb.BehaviorControlRequest) error {
			if req.GetControlRelease() != nil {
				released <- true
			}
			return nil
		},
		recvFn: func() (*vectorpb.BehaviorControlResponse, error) {
			return &vectorpb.BehaviorControlResponse{
				ResponseType: &vectorpb.BehaviorControlResponse_ControlGrantedResponse{
					ControlGrantedResponse: &vectorpb.ControlGrantedResponse{},
				},
			}, nil
		},
	}
	mockClient := &mockExternalInterfaceClient{
		behaviorControlFn: func(ctx context.Context, opts ...grpc.CallOption) (vectorpb.ExternalInterface_BehaviorControlClient, error) {
			return stream, nil
		},
	}
	rob := &vector.Vector{Conn: mockClient}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := make(chan bool, 1)
	stop := make(chan bool, 1)

	BControl(rob, ctx, start, stop)

	select {
	case granted := <-start:
		if !granted {
			t.Fatal("expected granted=true")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for control grant")
	}

	// Release control
	SafeSendBool(stop, true)

	select {
	case <-released:
		// success: release message sent cleanly
	case <-time.After(500 * time.Millisecond):
		t.Fatal("DEADLOCK: BControl failed to release control after stop signal")
	}
}

// TestDeadlock_ConcurrentChannelStress tests safe channel patterns under 100 concurrent workers
// to guarantee zero race conditions, deadlocks, or blocked senders under heavy load.
func TestDeadlock_ConcurrentChannelStress(t *testing.T) {
	const workers = 100
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func(id int) {
			defer wg.Done()

			start := make(chan bool, 1)
			stop := make(chan bool, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			// Half simulate success, half simulate error
			if id%2 == 0 {
				SafeSendBool(start, true)
			} else {
				SafeSendBool(start, false)
			}

			val, ok := WaitBoolWithTimeout(ctx, start, 50*time.Millisecond, "stressTest")
			if !ok {
				t.Errorf("worker %d timed out", id)
				return
			}

			if val {
				SafeSendBool(stop, true)
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// success: all workers completed with zero deadlocks
	case <-time.After(3 * time.Second):
		t.Fatal("DEADLOCK: Concurrent workers timed out!")
	}
}

