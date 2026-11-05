package wirepod_ttr

import (
	"strings"
)

// IsConversationDismissal checks if the user's spoken phrase is an intent to dismiss,
// cancel, or end the conversation. Returns (intentName, spokenText, isDismissal).
// If isDismissal is true, wire-pod should end the voice session immediately without
// calling the LLM or triggering new voice requests.
func IsConversationDismissal(text string) (string, string, bool) {
	clean := strings.ToLower(strings.TrimSpace(text))
	clean = strings.Trim(clean, ".!?,;:'\"")

	// Exact matches for single-word / short dismissals
	switch clean {
	case "goodbye", "good bye", "good by", "good buy", "bye", "bye bye", "see ya", "see you", "see you later":
		return "intent_greeting_goodbye", "Goodbye!", true
	case "never mind", "nevermind", "stop", "stop it", "cancel", "exit", "quit", "abort":
		return "intent_global_stop_extend", "", true
	case "that's all", "thats all", "that is all", "that's it", "thats it", "nothing", "nothing else":
		return "intent_global_stop_extend", "", true
	case "i'm good", "im good", "i'm done", "im done", "all done", "we're done", "were done", "done":
		return "intent_global_stop_extend", "", true
	case "no", "no thanks", "no thank you", "nope", "nah":
		return "intent_global_stop_extend", "", true
	case "shut up", "be quiet", "quiet", "silence", "shush":
		return "intent_imperative_shutup", "", true
	case "go to sleep", "sleep":
		return "intent_system_sleep", "", true
	}

	// Prefix / phrase matches for natural variations
	if strings.HasPrefix(clean, "goodbye") || strings.HasPrefix(clean, "bye bye") {
		return "intent_greeting_goodbye", "Goodbye!", true
	}
	if strings.HasPrefix(clean, "never mind") || strings.HasPrefix(clean, "nevermind") {
		return "intent_global_stop_extend", "", true
	}
	if strings.HasPrefix(clean, "no thank") || strings.HasPrefix(clean, "no thanks") {
		return "intent_global_stop_extend", "", true
	}
	if strings.HasPrefix(clean, "that's all") || strings.HasPrefix(clean, "that is all") || strings.HasPrefix(clean, "thats all") {
		return "intent_global_stop_extend", "", true
	}
	if strings.HasPrefix(clean, "stop talking") || strings.HasPrefix(clean, "stop it") {
		return "intent_global_stop_extend", "", true
	}

	return "", "", false
}

