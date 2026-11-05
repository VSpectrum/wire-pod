package wirepod_ttr

import (
	"context"
	"time"

	"github.com/fforchino/vector-go-sdk/pkg/vector"
	"github.com/fforchino/vector-go-sdk/pkg/vectorpb"
	"github.com/kercre123/wire-pod/chipper/pkg/logger"
)

func InterruptKGSimWhenTouchedOrWaked(rob *vector.Vector, stop chan bool, stopStop chan bool) bool {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		select {
		case <-stopStop:
			logger.Println("KG Interrupter has been stopped")
			cancel()
		case <-ctx.Done():
		}
	}()

	strm, err := rob.Conn.EventStream(
		ctx,
		&vectorpb.EventRequest{
			ListType: &vectorpb.EventRequest_WhiteList{
				WhiteList: &vectorpb.FilterList{
					List: []string{"robot_state", "wake_word"},
				},
			},
		},
	)
	if err != nil {
		logger.Println("Couldn't make an event stream: " + err.Error())
		return false
	}

	var origTouchValue uint32
	var origValueGotten bool
	var valsAboveValue int
	var valsAboveValueMax int = 5
	var stopResponse bool

	for {
		resp, err := strm.Recv()
		if err != nil {
			return false
		}
		switch resp.Event.EventType.(type) {
		case *vectorpb.Event_RobotState:
			origTouchValue = resp.Event.GetRobotState().TouchData.GetRawTouchValue()
			origValueGotten = true
		default:
		}
		if origValueGotten {
			break
		}
	}

	if origValueGotten {
		for {
			resp, err := strm.Recv()
			if err != nil {
				return false
			}
			switch resp.Event.EventType.(type) {
			case *vectorpb.Event_RobotState:
				if resp.Event.GetRobotState().TouchData.GetRawTouchValue() > origTouchValue+50 {
					valsAboveValue++
				} else {
					valsAboveValue = 0
				}
			case *vectorpb.Event_WakeWord:
				logger.Println("Interrupting LLM response (source: wake word)")
				stopResponse = true
			default:
			}
			if valsAboveValue > valsAboveValueMax {
				logger.Println("Interrupting LLM response (source: touch sensor)")
				stopResponse = true
			}
			if stopResponse {
				SafeSendBool(stop, true)
				time.Sleep(time.Second / 4)
				return true
			}
		}
	}
	return false
}
