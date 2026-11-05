package wirepod_ttr

import (
	"context"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fforchino/vector-go-sdk/pkg/vector"
	"github.com/fforchino/vector-go-sdk/pkg/vectorpb"
	"github.com/kercre123/wire-pod/chipper/pkg/logger"
	"github.com/kercre123/wire-pod/chipper/pkg/vars"
)

const (
	robotStatusIsMoving        = uint32(vectorpb.RobotStatus_ROBOT_STATUS_IS_MOVING)
	robotStatusIsPickedUp      = uint32(vectorpb.RobotStatus_ROBOT_STATUS_IS_PICKED_UP)
	robotStatusIsFalling       = uint32(vectorpb.RobotStatus_ROBOT_STATUS_IS_FALLING)
	robotStatusIsAnimating     = uint32(vectorpb.RobotStatus_ROBOT_STATUS_IS_ANIMATING)
	robotStatusIsOnCharger     = uint32(vectorpb.RobotStatus_ROBOT_STATUS_IS_ON_CHARGER)
	robotStatusAreWheelsMoving = uint32(vectorpb.RobotStatus_ROBOT_STATUS_ARE_WHEELS_MOVING)
	robotStatusIsBeingHeld     = uint32(vectorpb.RobotStatus_ROBOT_STATUS_IS_BEING_HELD)
)

type robotWatchState struct {
	mu                  sync.Mutex
	esn                 string
	running             bool
	lastFaceSeen        time.Time
	lastFaceID          int32
	poseAngleRad        float32
	headAngleRad        float32
	status              uint32
	lastWakeWordTime    time.Time
	poseAngleAtWake     float32
	faceSeenAtWake      bool
	lastMicDirection    uint32
	lastMicDirTime      time.Time
	micDirAtWake        uint32
	inVoiceSession      bool
	voiceSessionStarted time.Time
}

var (
	watchersMu      sync.Mutex
	robotWatchers   = make(map[string]*robotWatchState)
	watchersStarted bool
	sdkActionTagSeq int32 = 2000100
)

// nextSDKActionTag returns a valid SDK action tag in [2000001, 2999999]
// as required by vic-gateway ValidateActionTag.
func nextSDKActionTag() int32 {
	v := atomic.AddInt32(&sdkActionTagSeq, 1)
	if v <= 2000001 || v >= 2999999 {
		atomic.StoreInt32(&sdkActionTagSeq, 2000100)
		return 2000100
	}
	return v
}

func getOrCreateWatchState(esn string) *robotWatchState {
	esn = strings.TrimSpace(strings.ToLower(esn))
	watchersMu.Lock()
	defer watchersMu.Unlock()
	st, ok := robotWatchers[esn]
	if !ok {
		st = &robotWatchState{
			esn:              esn,
			lastMicDirection: 12,
			micDirAtWake:     12,
		}
		robotWatchers[esn] = st
	}
	return st
}

// StartRobotWatchers launches a background loop that ensures an EventStream
// watcher is running for every configured robot in vars.BotInfo.Robots.
func StartRobotWatchers() {
	watchersMu.Lock()
	if watchersStarted {
		watchersMu.Unlock()
		return
	}
	watchersStarted = true
	watchersMu.Unlock()

	go func() {
		for {
			for _, bot := range vars.BotInfo.Robots {
				if strings.TrimSpace(bot.Esn) != "" && strings.TrimSpace(bot.GUID) != "" && strings.TrimSpace(bot.IPAddress) != "" {
					EnsureRobotWatcher(bot.Esn)
				}
			}
			time.Sleep(10 * time.Second)
		}
	}()
}

// EnsureRobotWatcher starts the per-robot EventStream watcher for esn if not already running.
func EnsureRobotWatcher(esn string) {
	if strings.TrimSpace(esn) == "" {
		return
	}
	st := getOrCreateWatchState(esn)
	st.mu.Lock()
	if st.running {
		st.mu.Unlock()
		return
	}
	st.running = true
	st.mu.Unlock()

	go runRobotWatcher(st)
}

func connectRobot(esn string) (*vector.Vector, error) {
	esn = strings.TrimSpace(strings.ToLower(esn))
	for _, bot := range vars.BotInfo.Robots {
		if strings.EqualFold(strings.TrimSpace(bot.Esn), esn) {
			if strings.TrimSpace(bot.GUID) == "" || strings.TrimSpace(bot.IPAddress) == "" {
				return nil, nil
			}
			return vector.New(
				vector.WithSerialNo(bot.Esn),
				vector.WithToken(bot.GUID),
				vector.WithTarget(bot.IPAddress+":443"),
			)
		}
	}
	return nil, nil
}

func enableFaceDetectionOnRobot(ctx context.Context, robot *vector.Vector) {
	reqCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	_, _ = robot.Conn.EnableFaceDetection(reqCtx, &vectorpb.EnableFaceDetectionRequest{
		Enable:                     true,
		EnableSmileDetection:       true,
		EnableExpressionEstimation: true,
		EnableBlinkDetection:       true,
		EnableGazeDetection:        true,
	})
}

func runRobotWatcher(st *robotWatchState) {
	defer func() {
		st.mu.Lock()
		st.running = false
		st.mu.Unlock()
	}()

	for {
		robot, err := connectRobot(st.esn)
		if err != nil || robot == nil {
			time.Sleep(5 * time.Second)
			continue
		}

		ctx, cancel := context.WithCancel(context.Background())
		enableFaceDetectionOnRobot(ctx, robot)

		stream, err := robot.Conn.EventStream(ctx, &vectorpb.EventRequest{
			ListType: &vectorpb.EventRequest_WhiteList{
				WhiteList: &vectorpb.FilterList{
					List: []string{
						"robot_observed_face",
						"robot_state",
						"wake_word",
						"vision_modes_auto_disabled",
					},
				},
			},
		})
		if err != nil {
			cancel()
			time.Sleep(5 * time.Second)
			continue
		}

		logger.Println("Robot watcher connected for " + st.esn)

		for {
			resp, err := stream.Recv()
			if err != nil {
				break
			}
			if resp == nil || resp.Event == nil {
				continue
			}

			now := time.Now()

			switch resp.Event.EventType.(type) {
			case *vectorpb.Event_VisionModesAutoDisabled:
				if vars.APIConfig.Knowledge.TurnOnWakeWord {
					go enableFaceDetectionOnRobot(ctx, robot)
				}

			case *vectorpb.Event_RobotState:
				rs := resp.Event.GetRobotState()
				if rs != nil {
					st.mu.Lock()
					st.poseAngleRad = rs.GetPoseAngleRad()
					st.headAngleRad = rs.GetHeadAngleRad()
					st.status = rs.GetStatus()
					st.mu.Unlock()
				}

			case *vectorpb.Event_WakeWord:
				st.mu.Lock()
				st.lastWakeWordTime = now
				st.poseAngleAtWake = st.poseAngleRad
				st.faceSeenAtWake = !st.lastFaceSeen.IsZero() && now.Sub(st.lastFaceSeen) <= 3500*time.Millisecond
				if !st.lastMicDirTime.IsZero() && now.Sub(st.lastMicDirTime) <= 4*time.Second {
					st.micDirAtWake = st.lastMicDirection
				} else {
					st.micDirAtWake = 12
				}
				st.mu.Unlock()

			case *vectorpb.Event_RobotObservedFace:
				face := resp.Event.GetRobotObservedFace()
				if face != nil {
					st.mu.Lock()
					st.lastFaceSeen = now
					if face.GetFaceId() != 0 {
						st.lastFaceID = face.GetFaceId()
					}
					st.mu.Unlock()
				}
			}
		}

		cancel()
		time.Sleep(3 * time.Second)
	}
}

// MarkVoiceSessionStart records pose/face state at the start of a voice request.
func MarkVoiceSessionStart(esn string) {
	if strings.TrimSpace(esn) == "" {
		return
	}
	st := getOrCreateWatchState(esn)
	now := time.Now()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.lastWakeWordTime.IsZero() || now.Sub(st.lastWakeWordTime) > 6*time.Second {
		st.poseAngleAtWake = st.poseAngleRad
		st.faceSeenAtWake = !st.lastFaceSeen.IsZero() && now.Sub(st.lastFaceSeen) <= 3500*time.Millisecond
		if !st.lastMicDirTime.IsZero() && now.Sub(st.lastMicDirTime) <= 4*time.Second {
			st.micDirAtWake = st.lastMicDirection
		} else {
			st.micDirAtWake = 12
		}
	}
	st.inVoiceSession = true
	st.voiceSessionStarted = now
}

// MarkVoiceSessionEnd marks that the active voice request has completed.
func MarkVoiceSessionEnd(esn string) {
	if strings.TrimSpace(esn) == "" {
		return
	}
	st := getOrCreateWatchState(esn)
	st.mu.Lock()
	st.inVoiceSession = false
	st.mu.Unlock()
}

// ShouldTurnOnWake returns true if TurnOnWakeWord is enabled, the robot is off its charger,
// and no face was visible when the wake word was spoken.
func ShouldTurnOnWake(esn string) bool {
	if !vars.APIConfig.Knowledge.TurnOnWakeWord || strings.TrimSpace(esn) == "" {
		return false
	}
	st := getOrCreateWatchState(esn)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.status&robotStatusIsOnCharger != 0 {
		return false
	}
	if st.faceSeenAtWake {
		return false
	}
	if !st.lastFaceSeen.IsZero() && time.Since(st.lastFaceSeen) <= 3500*time.Millisecond {
		return false
	}
	return true
}

func absAngleDiff(a, b float32) float32 {
	diff := float64(a - b)
	diff = math.Mod(diff+math.Pi, 2*math.Pi)
	if diff < 0 {
		diff += 2 * math.Pi
	}
	return float32(math.Abs(diff - math.Pi))
}

// micDirToTurnAngleRad converts Vector's 12-direction microphone index (0..11, clockwise from 12 o'clock)
// to a relative TurnInPlace angle in radians (positive = counter-clockwise/left, negative = clockwise/right).
func micDirToTurnAngleRad(dir uint32) float32 {
	if dir == 0 || dir >= 12 {
		return float32(math.Pi)
	}
	if dir <= 6 {
		return -float32(dir) * float32(math.Pi/6.0)
	}
	return float32(12-dir) * float32(math.Pi/6.0)
}

// WaitForWakeTurnOrFace waits up to maxWait for Vector's native post-wake-word turn
// (e.g. from intent_imperative_lookatme) to complete or for a face to be observed.
func WaitForWakeTurnOrFace(esn string, maxWait time.Duration) bool {
	if strings.TrimSpace(esn) == "" {
		return false
	}
	st := getOrCreateWatchState(esn)
	deadline := time.Now().Add(maxWait)
	startWait := time.Now()
	sawMovement := false

	for time.Now().Before(deadline) {
		st.mu.Lock()
		faceSeenRecently := !st.lastFaceSeen.IsZero() && time.Since(st.lastFaceSeen) <= 1500*time.Millisecond
		turnedDelta := absAngleDiff(st.poseAngleRad, st.poseAngleAtWake)
		wheelsMoving := (st.status & (robotStatusAreWheelsMoving | robotStatusIsMoving)) != 0
		onCharger := (st.status & robotStatusIsOnCharger) != 0
		st.mu.Unlock()

		if onCharger {
			return false
		}
		if faceSeenRecently {
			return true
		}
		if wheelsMoving {
			sawMovement = true
		}
		if turnedDelta >= 0.35 && !wheelsMoving && (sawMovement || time.Since(startWait) > 450*time.Millisecond) {
			return true
		}
		time.Sleep(60 * time.Millisecond)
	}
	return false
}

// EnsureFacingUserUnderBControl is called while BehaviorControl (OVERRIDE_BEHAVIORS) is active.
// If Vector was facing away on wake and still hasn't turned or seen a face, it tilts his head up
// and rotates toward the detected microphone voice direction (or 180 degrees) until a face is spotted.
func EnsureFacingUserUnderBControl(ctx context.Context, robot *vector.Vector, esn string) {
	if !vars.APIConfig.Knowledge.TurnOnWakeWord || robot == nil || strings.TrimSpace(esn) == "" {
		return
	}
	st := getOrCreateWatchState(esn)
	st.mu.Lock()
	onCharger := (st.status & robotStatusIsOnCharger) != 0
	faceSeenRecently := !st.lastFaceSeen.IsZero() && time.Since(st.lastFaceSeen) <= 2500*time.Millisecond
	turnedDelta := absAngleDiff(st.poseAngleRad, st.poseAngleAtWake)
	headAngle := st.headAngleRad
	micDir := st.micDirAtWake
	st.mu.Unlock()

	if onCharger || faceSeenRecently {
		return
	}

	// Ensure head is angled up toward a human face if currently looking down
	if headAngle < 0.20 {
		headCtx, headCancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		_, _ = robot.Conn.SetHeadAngle(headCtx, &vectorpb.SetHeadAngleRequest{
			AngleRad:          0.35,
			MaxSpeedRadPerSec: 3.0,
			AccelRadPerSec2:   10.0,
			DurationSec:       0.35,
			IdTag:             nextSDKActionTag(),
		})
		headCancel()
	}

	// Re-check if tilting the head up revealed the user's face or if Vector already turned toward the mic
	st.mu.Lock()
	faceSeenRecently = !st.lastFaceSeen.IsZero() && time.Since(st.lastFaceSeen) <= 2000*time.Millisecond
	faceID := st.lastFaceID
	turnedDelta = absAngleDiff(st.poseAngleRad, st.poseAngleAtWake)
	st.mu.Unlock()

	if faceSeenRecently {
		if faceID != 0 {
			tfCtx, tfCancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			_, _ = robot.Conn.TurnTowardsFace(tfCtx, &vectorpb.TurnTowardsFaceRequest{
				FaceId:          faceID,
				MaxTurnAngleRad: 1.2,
				IdTag:           nextSDKActionTag(),
			})
			tfCancel()
		}
		return
	}

	if turnedDelta >= 0.35 {
		return
	}

	targetTurnAngle := micDirToTurnAngleRad(micDir)
	logger.Println("Turning bot " + esn + " to face user...")
	turnTag := nextSDKActionTag()
	turnDone := make(chan struct{})
	turnCtx, turnCancel := context.WithTimeout(ctx, 2600*time.Millisecond)
	defer turnCancel()

	go func() {
		defer close(turnDone)
		_, _ = robot.Conn.TurnInPlace(turnCtx, &vectorpb.TurnInPlaceRequest{
			AngleRad:        targetTurnAngle,
			SpeedRadPerSec:  2.8,
			AccelRadPerSec2: 6.0,
			TolRad:          0.1,
			IsAbsolute:      0,
			IdTag:           turnTag,
		})
	}()

	turnStart := time.Now()
	spottedFaceID := int32(0)
	for {
		select {
		case <-turnDone:
			goto doneTurning
		default:
		}
		st.mu.Lock()
		if !st.lastFaceSeen.IsZero() && st.lastFaceSeen.After(turnStart) {
			spottedFaceID = st.lastFaceID
			st.mu.Unlock()
			cancelCtx, cancelFunc := context.WithTimeout(ctx, 500*time.Millisecond)
			_, _ = robot.Conn.CancelActionByIdTag(cancelCtx, &vectorpb.CancelActionByIdTagRequest{
				IdTag: uint32(turnTag),
			})
			cancelFunc()
			break
		}
		st.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}

doneTurning:
	if spottedFaceID != 0 {
		tfCtx, tfCancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		_, _ = robot.Conn.TurnTowardsFace(tfCtx, &vectorpb.TurnTowardsFaceRequest{
			FaceId:          spottedFaceID,
			MaxTurnAngleRad: 1.2,
			IdTag:           nextSDKActionTag(),
		})
		tfCancel()
	}
}

func isNonTurnIntent(intentName string) bool {
	switch intentName {
	case "intent_imperative_lookatme",
		"intent_imperative_come",
		"intent_imperative_turnaround",
		"intent_imperative_turnleft",
		"intent_imperative_turnright",
		"intent_imperative_moveforward",
		"intent_imperative_backup",
		"intent_imperative_fetchcube",
		"intent_imperative_findcube",
		"intent_play_rollcube",
		"intent_play_popawheelie",
		"intent_system_charger",
		"intent_system_sleep",
		"intent_system_noaudio",
		"explore_start":
		return true
	}
	return false
}

// EnsureTurnedOnIntent ensures Vector turns toward the user if he was facing away when a standard
// non-movement voice command was matched.
func EnsureTurnedOnIntent(esn string, intentName string) {
	if !ShouldTurnOnWake(esn) || isNonTurnIntent(intentName) {
		return
	}
	if WaitForWakeTurnOrFace(esn, 1400*time.Millisecond) {
		return
	}
	robot, err := connectRobot(esn)
	if err != nil || robot == nil {
		return
	}
	ctx := context.Background()
	start := make(chan bool, 1)
	stop := make(chan bool, 1)
	BControl(robot, ctx, start, stop)
	select {
	case <-start:
		EnsureFacingUserUnderBControl(ctx, robot, esn)
		stop <- true
	case <-time.After(2 * time.Second):
	}
}
