package wirepod_ttr

import (
	"context"
	"log"
	"time"

	"github.com/fforchino/vector-go-sdk/pkg/vector"
	"github.com/fforchino/vector-go-sdk/pkg/vectorpb"
	"github.com/kercre123/wire-pod/chipper/pkg/logger"
)

func sayText(robot *vector.Vector, text string) {
	controlRequest := &vectorpb.BehaviorControlRequest{
		RequestType: &vectorpb.BehaviorControlRequest_ControlRequest{
			ControlRequest: &vectorpb.ControlRequest{
				Priority: vectorpb.ControlRequest_OVERRIDE_BEHAVIORS,
			},
		},
	}
	go func() {
		start := make(chan bool, 1)
		stop := make(chan bool, 1)
		go func() {
			// * begin - modified from official vector-go-sdk
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			r, err := robot.Conn.BehaviorControl(ctx)
			if err != nil {
				log.Println("[DeadlockGuard:sayText] BehaviorControl error:", err)
				SafeSendBool(start, false)
				return
			}

			if err := r.Send(controlRequest); err != nil {
				log.Println("[DeadlockGuard:sayText] Send error:", err)
				SafeSendBool(start, false)
				return
			}

			for {
				ctrlresp, err := r.Recv()
				if err != nil {
					log.Println("[DeadlockGuard:sayText] Recv error:", err)
					SafeSendBool(start, false)
					return
				}
				if ctrlresp.GetControlGrantedResponse() != nil {
					SafeSendBool(start, true)
					break
				}
			}

			select {
			case <-stop:
			case <-ctx.Done():
			}

			if err := r.Send(
				&vectorpb.BehaviorControlRequest{
					RequestType: &vectorpb.BehaviorControlRequest_ControlRelease{
						ControlRelease: &vectorpb.ControlRelease{},
					},
				},
			); err != nil {
				log.Println("[DeadlockGuard:sayText] Release error:", err)
				return
			}
			// * end - modified from official vector-go-sdk
		}()

		select {
		case granted := <-start:
			if !granted {
				return
			}
			_, _ = robot.Conn.SayText(
				context.Background(),
				&vectorpb.SayTextRequest{
					Text:           text,
					UseVectorVoice: false,
					DurationScalar: 1.0,
				},
			)
			SafeSendBool(stop, true)
		case <-time.After(8 * time.Second):
			logger.Println("[DeadlockGuard:sayText] Timed out waiting for behavior control")
			SafeSendBool(stop, true)
		}
	}()
}

func BControl(robot *vector.Vector, ctx context.Context, start, stop chan bool) {
	controlRequest := &vectorpb.BehaviorControlRequest{
		RequestType: &vectorpb.BehaviorControlRequest_ControlRequest{
			ControlRequest: &vectorpb.ControlRequest{
				Priority: vectorpb.ControlRequest_OVERRIDE_BEHAVIORS,
			},
		},
	}

	go func() {
		// * begin - modified from official vector-go-sdk
		r, err := robot.Conn.BehaviorControl(ctx)
		if err != nil {
			logger.Println("[DeadlockGuard:BControl] BehaviorControl error:", err)
			SafeSendBool(start, false)
			return
		}

		if err := r.Send(controlRequest); err != nil {
			logger.Println("[DeadlockGuard:BControl] Send error:", err)
			SafeSendBool(start, false)
			return
		}

		for {
			ctrlresp, err := r.Recv()
			if err != nil {
				logger.Println("[DeadlockGuard:BControl] Recv error:", err)
				SafeSendBool(start, false)
				return
			}
			if ctrlresp.GetControlGrantedResponse() != nil {
				SafeSendBool(start, true)
				break
			}
		}

		select {
		case <-stop:
			logger.Println("KGSim: releasing behavior control (interrupt/stop)")
		case <-ctx.Done():
			logger.Println("KGSim: releasing behavior control (context cancelled)")
		}

		if err := r.Send(
			&vectorpb.BehaviorControlRequest{
				RequestType: &vectorpb.BehaviorControlRequest_ControlRelease{
					ControlRelease: &vectorpb.ControlRelease{},
				},
			},
		); err != nil {
			logger.Println("[DeadlockGuard:BControl] ControlRelease error:", err)
			return
		}
		// * end - modified from official vector-go-sdk
	}()
}
