package wirepod_ttr

import "testing"

func runFilter(model string, chunks ...string) string {
	f := newReasoningFilter(model)
	out := ""
	for _, c := range chunks {
		out += f.Feed(c)
	}
	return out + f.Flush()
}

func TestReasoningFilter(t *testing.T) {
	cases := []struct {
		name   string
		model  string
		chunks []string
		want   string
	}{
		{"plain answer", "supergemma4-26b", []string{"Hello ", "there."}, "Hello there."},
		{"think block", "qwen3-30b", []string{"<think>hmm, the user", " wants a joke</think>", "Why did the robot cross the road?"}, "Why did the robot cross the road?"},
		{"tag split across chunks", "some-model", []string{"<thi", "nk>secret</th", "ink>Answer."}, "Answer."},
		{"orphan close tag", "qwen3.6-27b", []string{"Okay the user asks...", " I'll answer.</think>\n\n", "The sky is blue."}, "\n\nThe sky is blue."},
		{"thinking tag", "llama-reasoning", []string{"<thinking>plan</thinking>Sure!"}, "Sure!"},
		{"mistral think", "magistral", []string{"[THINK]plan[/THINK]Yes."}, "Yes."},
		{"empty think", "qwen3.5-9b", []string{"<think>\n\n</think>\n\nHi."}, "\n\nHi."},
		{"unterminated reasoning dropped", "qwen3", []string{"<think>still thinking"}, ""},
		{"reasoning model with no tags", "qwen3.5-9b", []string{"Just an ", "answer."}, "Just an answer."},
		{"lone angle bracket", "gemma", []string{"5 <", " 6 is true."}, "5 < 6 is true."},
		{"muse-glimmer orphan close", "meta/muse-glimmer", []string{"Let me think...", "</think>Hi I am Vector!"}, "Hi I am Vector!"},
	}
	for _, c := range cases {
		if got := runFilter(c.model, c.chunks...); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestExtractSentences(t *testing.T) {
	sents, rest := extractSentences("First sentence. Second sentence! Value is 3.14 right now. Trailing")
	if len(sents) != 3 {
		t.Fatalf("expected 3 sentences, got %d: %v (rest=%q)", len(sents), sents, rest)
	}
	if sents[0] != "First sentence." || sents[1] != "Second sentence!" || sents[2] != "Value is 3.14 right now." {
		t.Errorf("unexpected sentences: %v", sents)
	}
	if rest != " Trailing" {
		t.Errorf("unexpected rest: %q", rest)
	}

	// Ensure periods inside {{rememberFact||...}} don't prematurely split sentences
	sents2, rest2 := extractSentences("{{rememberFact||Works at Inc. as Sr. Dev}} Nice to meet you, Chris!")
	if len(sents2) != 1 || sents2[0] != "{{rememberFact||Works at Inc. as Sr. Dev}} Nice to meet you, Chris!" || rest2 != "" {
		t.Errorf("command with period split unexpectedly: sents=%v rest=%q", sents2, rest2)
	}

	acts := GetActionsFromString("I will remember that {{rememberFact||Likes espresso}}.")
	if len(acts) != 2 || acts[0].Action != ActionSayText || acts[1].Action != ActionRememberFact || acts[1].Parameter != "Likes espresso" {
		t.Errorf("unexpected actions: %+v", acts)
	}
}

