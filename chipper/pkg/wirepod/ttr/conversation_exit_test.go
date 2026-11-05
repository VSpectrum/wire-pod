package wirepod_ttr

import (
	"testing"
)

func TestIsConversationDismissal(t *testing.T) {
	testCases := []struct {
		input       string
		wantDismiss bool
		wantIntent  string
		wantSpoken  string
	}{
		{"goodbye", true, "intent_greeting_goodbye", "Goodbye!"},
		{"Goodbye.", true, "intent_greeting_goodbye", "Goodbye!"},
		{"bye bye", true, "intent_greeting_goodbye", "Goodbye!"},
		{"never mind", true, "intent_global_stop_extend", ""},
		{"nevermind", true, "intent_global_stop_extend", ""},
		{"stop", true, "intent_global_stop_extend", ""},
		{"stop talking", true, "intent_global_stop_extend", ""},
		{"that's all", true, "intent_global_stop_extend", ""},
		{"that is all", true, "intent_global_stop_extend", ""},
		{"i'm good", true, "intent_global_stop_extend", ""},
		{"no thanks", true, "intent_global_stop_extend", ""},
		{"no thank you", true, "intent_global_stop_extend", ""},
		{"shut up", true, "intent_imperative_shutup", ""},
		{"go to sleep", true, "intent_system_sleep", ""},
		{"what is the weather", false, "", ""},
		{"tell me about dogs", false, "", ""},
		{"who are you", false, "", ""},
	}

	for _, tc := range testCases {
		intent, spoken, isDismiss := IsConversationDismissal(tc.input)
		if isDismiss != tc.wantDismiss {
			t.Errorf("input '%s': got isDismiss=%v, want %v", tc.input, isDismiss, tc.wantDismiss)
		}
		if tc.wantDismiss {
			if intent != tc.wantIntent {
				t.Errorf("input '%s': got intent '%s', want '%s'", tc.input, intent, tc.wantIntent)
			}
			if spoken != tc.wantSpoken {
				t.Errorf("input '%s': got spoken '%s', want '%s'", tc.input, spoken, tc.wantSpoken)
			}
		}
	}
}

