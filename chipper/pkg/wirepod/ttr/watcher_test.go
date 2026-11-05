package wirepod_ttr

import (
	"math"
	"testing"
	"time"

	"github.com/kercre123/wire-pod/chipper/pkg/vars"
)

func TestAbsAngleDiff(t *testing.T) {
	if got := absAngleDiff(0, 0); got > 0.001 {
		t.Fatalf("expected ~0, got %v", got)
	}
	if got := absAngleDiff(float32(math.Pi-0.1), float32(-math.Pi+0.1)); math.Abs(float64(got)-0.2) > 0.01 {
		t.Fatalf("expected ~0.2 across pi boundary, got %v", got)
	}
	if got := absAngleDiff(0, float32(math.Pi)); math.Abs(float64(got)-math.Pi) > 0.01 {
		t.Fatalf("expected ~pi, got %v", got)
	}
}

func TestMicDirToTurnAngleRad(t *testing.T) {
	// Dir 3 = 90 deg right -> -pi/2
	if got := micDirToTurnAngleRad(3); math.Abs(float64(got)+math.Pi/2) > 0.01 {
		t.Fatalf("expected -pi/2 for dir 3, got %v", got)
	}
	// Dir 9 = 90 deg left -> +pi/2
	if got := micDirToTurnAngleRad(9); math.Abs(float64(got)-math.Pi/2) > 0.01 {
		t.Fatalf("expected +pi/2 for dir 9, got %v", got)
	}
	// Dir 6 = 180 deg behind -> -pi
	if got := micDirToTurnAngleRad(6); math.Abs(math.Abs(float64(got))-math.Pi) > 0.01 {
		t.Fatalf("expected +/-pi for dir 6, got %v", got)
	}
}

func TestNextSDKActionTagRange(t *testing.T) {
	for i := 0; i < 100; i++ {
		tag := nextSDKActionTag()
		if tag <= 2000001 || tag >= 2999999 {
			t.Fatalf("tag %d out of valid SDK range [2000001, 2999999]", tag)
		}
	}
}

func TestShouldTurnOnWake(t *testing.T) {
	vars.APIConfig.Knowledge.TurnOnWakeWord = true
	esn := "00112233"
	st := getOrCreateWatchState(esn)

	st.mu.Lock()
	st.status = 0
	st.faceSeenAtWake = false
	st.lastFaceSeen = time.Now().Add(-10 * time.Second)
	st.mu.Unlock()

	if !ShouldTurnOnWake(esn) {
		t.Fatal("expected ShouldTurnOnWake to be true when no face seen and off charger")
	}

	st.mu.Lock()
	st.status = robotStatusIsOnCharger
	st.mu.Unlock()

	if ShouldTurnOnWake(esn) {
		t.Fatal("expected ShouldTurnOnWake to be false when on charger")
	}

	st.mu.Lock()
	st.status = 0
	st.faceSeenAtWake = true
	st.mu.Unlock()

	if ShouldTurnOnWake(esn) {
		t.Fatal("expected ShouldTurnOnWake to be false when face was already seen at wake")
	}
}
