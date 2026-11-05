package wirepod_ttr

import "strings"

// Reasoning ("thinking") models such as Qwen3, DeepSeek-R1, GLM and Magistral wrap
// their chain-of-thought in tags before giving the final answer. Vector should only
// speak the answer, so everything inside these tags is removed from the stream.
var reasoningTags = [][2]string{
	{"<think>", "</think>"},
	{"<thinking>", "</thinking>"},
	{"<reasoning>", "</reasoning>"},
	{"[THINK]", "[/THINK]"},
}

// Some chat templates put the opening <think> tag into the prompt, so the model's
// output starts in the middle of its reasoning and only contains the closing tag.
// For models whose name suggests they reason, output is held back until a tag is
// seen (or the stream ends), so this "orphaned" reasoning can be discarded too.
var reasoningModelHints = []string{
	"think", "reason", "qwen3", "qwq", "r1", "glm", "magistral", "deepseek", "muse-glimmer", "glimmer",
}

// extractSentences splits buffered streamed text into complete sentences and returns
// the slices ready to speak along with any trailing partial sentence. It scans for the
// earliest sentence terminator so multiple sentences in a single chunk are never lost,
// and avoids splitting inside decimal numbers like "3.14".
func extractSentences(text string) (sentences []string, rest string) {
	rest = text
	for {
		idx, sep := findSentenceEnd(rest)
		if idx < 0 {
			return sentences, rest
		}
		head := strings.TrimSpace(rest[:idx])
		rest = rest[idx+len(sep):]
		if head != "" {
			sentences = append(sentences, head+sep)
		}
	}
}

func findSentenceEnd(s string) (int, string) {
	inCmd := false
	for i := 0; i < len(s); i++ {
		if !inCmd && strings.HasPrefix(s[i:], "{{") {
			inCmd = true
			i++
			continue
		}
		if inCmd {
			if strings.HasPrefix(s[i:], "}}") {
				inCmd = false
				i++
			}
			continue
		}
		if strings.HasPrefix(s[i:], "...") {
			return i, "..."
		}
		if strings.HasPrefix(s[i:], ".'") {
			return i, ".'"
		}
		if strings.HasPrefix(s[i:], `."`) {
			return i, `."`
		}
		switch s[i] {
		case '.':
			// Don't split inside decimal numbers (e.g. "3.14")
			if i > 0 && i+1 < len(s) && s[i-1] >= '0' && s[i-1] <= '9' && s[i+1] >= '0' && s[i+1] <= '9' {
				continue
			}
			return i, "."
		case '?':
			return i, "?"
		case '!':
			return i, "!"
		}
	}
	return -1, ""
}

func modelMayReason(model string) bool {
	m := strings.ToLower(model)
	for _, hint := range reasoningModelHints {
		if strings.Contains(m, hint) {
			return true
		}
	}
	return false
}

// reasoningFilter removes reasoning blocks from streamed LLM output. Feed it each
// streamed chunk, then call Flush once the stream ends.
type reasoningFilter struct {
	buf      string
	closeTag string // set while inside a reasoning block
	holding  bool   // true until we know whether output starts mid-reasoning
}

func newReasoningFilter(model string) *reasoningFilter {
	return &reasoningFilter{holding: modelMayReason(model)}
}

// Feed takes the next raw chunk and returns the text that is safe to speak.
func (f *reasoningFilter) Feed(chunk string) string {
	f.buf += chunk
	var out strings.Builder
	for {
		if f.closeTag != "" {
			i := strings.Index(f.buf, f.closeTag)
			if i < 0 {
				// keep only what could be the start of the closing tag
				f.buf = f.buf[len(f.buf)-partialSuffixLen(f.buf, f.closeTag):]
				return out.String()
			}
			f.buf = f.buf[i+len(f.closeTag):]
			f.closeTag = ""
			continue
		}

		openIdx, openTag, closeTag := firstTag(f.buf, 0)
		closeIdx, orphanClose, _ := firstTag(f.buf, 1)

		// a closing tag with no opening tag: everything before it was reasoning
		if closeIdx >= 0 && (openIdx < 0 || closeIdx < openIdx) {
			f.buf = f.buf[closeIdx+len(orphanClose):]
			f.holding = false
			continue
		}
		if openIdx >= 0 {
			f.holding = false
			out.WriteString(f.buf[:openIdx])
			f.buf = f.buf[openIdx+len(openTag):]
			f.closeTag = closeTag
			continue
		}
		if f.holding {
			return out.String()
		}
		keep := 0
		for _, pair := range reasoningTags {
			for _, tag := range pair {
				if k := partialSuffixLen(f.buf, tag); k > keep {
					keep = k
				}
			}
		}
		out.WriteString(f.buf[:len(f.buf)-keep])
		f.buf = f.buf[len(f.buf)-keep:]
		return out.String()
	}
}

// Flush returns any remaining speakable text once the stream has ended.
// Reasoning that never closed (e.g. the model hit its token limit) is dropped.
func (f *reasoningFilter) Flush() string {
	out := f.buf
	if f.closeTag != "" {
		out = ""
	}
	f.buf, f.closeTag, f.holding = "", "", false
	return out
}

// firstTag finds the earliest open (side 0) or close (side 1) reasoning tag in s.
func firstTag(s string, side int) (idx int, tag string, partner string) {
	idx = -1
	for _, pair := range reasoningTags {
		if i := strings.Index(s, pair[side]); i >= 0 && (idx < 0 || i < idx) {
			idx, tag, partner = i, pair[side], pair[1-side]
		}
	}
	return idx, tag, partner
}

// partialSuffixLen returns the length of the longest suffix of s that is a proper
// prefix of tag, i.e. a tag that may be completed by the next chunk.
func partialSuffixLen(s, tag string) int {
	for k := len(tag) - 1; k > 0; k-- {
		if strings.HasSuffix(s, tag[:k]) {
			return k
		}
	}
	return 0
}
