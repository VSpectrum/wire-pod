package vars

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kercre123/wire-pod/chipper/pkg/logger"
	"github.com/sashabaranov/go-openai"
)

var PersonasPath string = "./jdocs/personas.json"
var VoskSpkModelPath string = "../vosk/models/spk/model"

type PersonaQuestion struct {
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
}

type VoicePersona struct {
	ID          string                         `json:"id"`
	Name        string                         `json:"name"`
	Named       bool                           `json:"named"`
	Embedding   []float64                      `json:"embedding"`
	SampleCount int                            `json:"sample_count"`
	Facts       []string                       `json:"facts"`
	Questions   []PersonaQuestion              `json:"questions"`
	Chats       []openai.ChatCompletionMessage `json:"chats"`
	CreatedAt   string                         `json:"created_at"`
	LastSeenAt  string                         `json:"last_seen_at"`
}

type ActiveSpeakerInfo struct {
	PersonaID  string    `json:"persona_id"`
	PersonaName string   `json:"persona_name"`
	Similarity float64   `json:"similarity"`
	SpkFrames  int       `json:"spk_frames"`
	IsNew      bool      `json:"is_new"`
	UpdatedAt  time.Time `json:"updated_at"`
}

var (
	personasMu        sync.RWMutex
	VoicePersonas     []VoicePersona
	lastActiveSpeaker = make(map[string]ActiveSpeakerInfo)
)

const (
	defaultSpeakerSimThreshold = 0.68
	shortUtteranceSimThreshold = 0.63
	maxPersonaFacts            = 60
	maxPersonaQuestions        = 50
	maxPersonaChats            = 16
)

func InitPersonas() {
	personasMu.Lock()
	defer personasMu.Unlock()

	if _, err := os.Stat(PersonasPath); err == nil {
		data, err := os.ReadFile(PersonasPath)
		if err == nil {
			if err := json.Unmarshal(data, &VoicePersonas); err != nil {
				logger.Println("Error parsing personas.json: " + err.Error())
			} else {
				logger.Println(fmt.Sprintf("Loaded %d voice personas from %s", len(VoicePersonas), PersonasPath))
			}
		}
	}
}

func savePersonasLocked() {
	os.MkdirAll(JdocsDir, 0755)
	data, err := json.MarshalIndent(VoicePersonas, "", "  ")
	if err != nil {
		logger.Println("Error marshaling personas: " + err.Error())
		return
	}
	if err := os.WriteFile(PersonasPath, data, 0644); err != nil {
		logger.Println("Error writing personas.json: " + err.Error())
	}
}

func SavePersonas() {
	personasMu.Lock()
	defer personasMu.Unlock()
	savePersonasLocked()
}

func normalizeVec(v []float64) []float64 {
	var sumSq float64
	for _, x := range v {
		sumSq += x * x
	}
	if sumSq == 0 {
		return v
	}
	norm := math.Sqrt(sumSq)
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = x / norm
	}
	return out
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func nextSpeakerNumberLocked() int {
	maxNum := len(VoicePersonas)
	for _, p := range VoicePersonas {
		var n int
		if _, err := fmt.Sscanf(p.ID, "speaker_%d", &n); err == nil && n > maxNum {
			maxNum = n
		}
		if _, err := fmt.Sscanf(p.Name, "Speaker %d", &n); err == nil && n > maxNum {
			maxNum = n
		}
	}
	return maxNum + 1
}

// IdentifySpeaker matches a 128-d Vosk speaker X-vector against known personas
// or creates a new voice cluster if unrecognized.
func IdentifySpeaker(esn string, spkVec []float64, spkFrames int, transcribedText string) ActiveSpeakerInfo {
	if strings.TrimSpace(esn) == "" {
		esn = "default"
	}
	nowStr := time.Now().Format("2006-01-02 15:04:05")
	normVec := normalizeVec(spkVec)

	personasMu.Lock()
	defer personasMu.Unlock()

	if len(normVec) == 0 {
		if prev, ok := lastActiveSpeaker[esn]; ok {
			return prev
		}
		return ActiveSpeakerInfo{}
	}

	bestIdx := -1
	bestSim := -1.0
	prevActive, hasPrev := lastActiveSpeaker[esn]

	for i, p := range VoicePersonas {
		if len(p.Embedding) != len(normVec) {
			continue
		}
		sim := cosineSimilarity(normVec, p.Embedding)
		// Small continuity bonus if the same person was just speaking within the last 90 seconds
		effectiveSim := sim
		if hasPrev && p.ID == prevActive.PersonaID && time.Since(prevActive.UpdatedAt) < 90*time.Second {
			effectiveSim += 0.05
		}
		if effectiveSim > bestSim {
			bestSim = effectiveSim
			bestIdx = i
		}
	}

	threshold := defaultSpeakerSimThreshold
	if spkFrames < 45 {
		threshold = shortUtteranceSimThreshold
	}

	var matchedPersona *VoicePersona
	isNew := false
	actualSim := bestSim

	if bestIdx >= 0 && bestSim >= threshold {
		matchedPersona = &VoicePersonas[bestIdx]
		actualSim = cosineSimilarity(normVec, matchedPersona.Embedding)
		matchedPersona.LastSeenAt = nowStr

		// Update voice centroid when the utterance has enough voiced frames for a clean sample
		if spkFrames >= 30 {
			weight := 1.0 / float64(minInt(matchedPersona.SampleCount+1, 20))
			updated := make([]float64, len(normVec))
			for j := range normVec {
				updated[j] = matchedPersona.Embedding[j]*(1.0-weight) + normVec[j]*weight
			}
			matchedPersona.Embedding = normalizeVec(updated)
			matchedPersona.SampleCount++
		}
	} else if spkFrames < 22 && hasPrev && time.Since(prevActive.UpdatedAt) < 3*time.Minute {
		// Very short utterance (e.g. "yes", "stop"): reuse recent speaker without drifting their centroid
		for i := range VoicePersonas {
			if VoicePersonas[i].ID == prevActive.PersonaID {
				matchedPersona = &VoicePersonas[i]
				matchedPersona.LastSeenAt = nowStr
				actualSim = cosineSimilarity(normVec, matchedPersona.Embedding)
				break
			}
		}
	}

	if matchedPersona == nil {
		num := nextSpeakerNumberLocked()
		newP := VoicePersona{
			ID:          fmt.Sprintf("speaker_%d", num),
			Name:        fmt.Sprintf("Speaker %d", num),
			Named:       false,
			Embedding:   normVec,
			SampleCount: 1,
			Facts:       []string{},
			Questions:   []PersonaQuestion{},
			Chats:       []openai.ChatCompletionMessage{},
			CreatedAt:   nowStr,
			LastSeenAt:  nowStr,
		}
		VoicePersonas = append(VoicePersonas, newP)
		matchedPersona = &VoicePersonas[len(VoicePersonas)-1]
		actualSim = 1.0
		isNew = true
		logger.Println(fmt.Sprintf("(Voice Persona) Created new voice profile: %s (%s, frames=%d)", matchedPersona.Name, matchedPersona.ID, spkFrames))
	} else {
		logger.Println(fmt.Sprintf("(Voice Persona) Identified speaker: %s (%s, similarity=%.2f, frames=%d)", matchedPersona.Name, matchedPersona.ID, actualSim, spkFrames))
	}

	info := ActiveSpeakerInfo{
		PersonaID:   matchedPersona.ID,
		PersonaName: matchedPersona.Name,
		Similarity:  actualSim,
		SpkFrames:   spkFrames,
		IsNew:       isNew,
		UpdatedAt:   time.Now(),
	}
	lastActiveSpeaker[esn] = info

	// Check if the user naturally introduced themselves in this utterance
	if name := extractNaturalNameIntroduction(transcribedText); name != "" {
		merged := setPersonaNameLocked(matchedPersona.ID, name)
		if merged != nil {
			info.PersonaID = merged.ID
			info.PersonaName = merged.Name
			lastActiveSpeaker[esn] = info
		}
	}

	savePersonasLocked()
	return info
}

var (
	nameIntroPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bmy name is\s+([a-zA-Z]{2,20})\b`),
		regexp.MustCompile(`(?i)\bmy name's\s+([a-zA-Z]{2,20})\b`),
		regexp.MustCompile(`(?i)\bcall me\s+([a-zA-Z]{2,20})\b`),
		regexp.MustCompile(`(?i)^(?:hey vector\s+)?(?:hi\s+|hello\s+)?this is\s+([a-zA-Z]{2,20})(?:\s+speaking|\s+here)?$`),
		regexp.MustCompile(`(?i)^(?:hey vector\s+)?(?:hi\s+|hello\s+)?i am\s+([a-zA-Z]{2,20})$`),
		regexp.MustCompile(`(?i)^(?:hey vector\s+)?(?:hi\s+|hello\s+)?i'm\s+([a-zA-Z]{2,20})$`),
	}

	nonNameStopwords = map[string]bool{
		"good": true, "great": true, "fine": true, "okay": true, "ok": true, "well": true,
		"sad": true, "happy": true, "tired": true, "hungry": true, "thirsty": true, "bored": true,
		"back": true, "home": true, "here": true, "there": true, "ready": true, "done": true,
		"going": true, "doing": true, "asking": true, "looking": true, "talking": true, "trying": true,
		"not": true, "just": true, "very": true, "so": true, "too": true, "still": true,
		"your": true, "a": true, "an": true, "the": true, "sorry": true, "sure": true, "afraid": true,
		"glad": true, "mad": true, "angry": true, "excited": true, "sick": true, "cold": true, "hot": true,
	}
)

func extractNaturalNameIntroduction(text string) string {
	cleaned := strings.TrimSpace(text)
	if cleaned == "" {
		return ""
	}
	for _, re := range nameIntroPatterns {
		m := re.FindStringSubmatch(cleaned)
		if len(m) >= 2 {
			candidate := strings.ToLower(strings.TrimSpace(m[1]))
			if nonNameStopwords[candidate] {
				continue
			}
			return titleCaseName(candidate)
		}
	}
	return ""
}

func titleCaseName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	return strings.Join(words, " ")
}

func setPersonaNameLocked(personaID, rawName string) *VoicePersona {
	newName := titleCaseName(rawName)
	if newName == "" {
		return nil
	}
	srcIdx := -1
	targetIdx := -1
	for i := range VoicePersonas {
		if VoicePersonas[i].ID == personaID {
			srcIdx = i
		} else if strings.EqualFold(VoicePersonas[i].Name, newName) {
			targetIdx = i
		}
	}
	if srcIdx < 0 {
		return nil
	}

	// If another persona already has this exact name, merge src into target
	if targetIdx >= 0 && targetIdx != srcIdx {
		src := VoicePersonas[srcIdx]
		dst := &VoicePersonas[targetIdx]
		logger.Println(fmt.Sprintf("(Voice Persona) Merging %s (%s) into existing persona %s (%s)", src.Name, src.ID, dst.Name, dst.ID))

		if len(src.Embedding) == len(dst.Embedding) && len(dst.Embedding) > 0 {
			totalSamples := dst.SampleCount + src.SampleCount
			if totalSamples > 0 {
				wDst := float64(dst.SampleCount) / float64(totalSamples)
				wSrc := float64(src.SampleCount) / float64(totalSamples)
				mergedEmb := make([]float64, len(dst.Embedding))
				for j := range dst.Embedding {
					mergedEmb[j] = dst.Embedding[j]*wDst + src.Embedding[j]*wSrc
				}
				dst.Embedding = normalizeVec(mergedEmb)
				dst.SampleCount = totalSamples
			}
		}
		for _, f := range src.Facts {
			addFactToSlice(&dst.Facts, f)
		}
		for _, q := range src.Questions {
			dst.Questions = append(dst.Questions, q)
		}
		if len(dst.Questions) > maxPersonaQuestions {
			dst.Questions = dst.Questions[len(dst.Questions)-maxPersonaQuestions:]
		}
		dst.Chats = append(dst.Chats, src.Chats...)
		if len(dst.Chats) > maxPersonaChats {
			dst.Chats = dst.Chats[len(dst.Chats)-maxPersonaChats:]
		}
		dst.Named = true
		dst.LastSeenAt = time.Now().Format("2006-01-02 15:04:05")

		// Remove srcIdx
		VoicePersonas = append(VoicePersonas[:srcIdx], VoicePersonas[srcIdx+1:]...)
		for i := range VoicePersonas {
			if strings.EqualFold(VoicePersonas[i].Name, newName) {
				return &VoicePersonas[i]
			}
		}
		return nil
	}

	VoicePersonas[srcIdx].Name = newName
	VoicePersonas[srcIdx].Named = true
	logger.Println(fmt.Sprintf("(Voice Persona) Bound persona %s to name '%s'", VoicePersonas[srcIdx].ID, newName))
	return &VoicePersonas[srcIdx]
}

// SetActivePersonaName binds or updates the name of the active speaker for `esn`.
func SetActivePersonaName(esn, newName string) string {
	if strings.TrimSpace(esn) == "" {
		esn = "default"
	}
	personasMu.Lock()
	defer personasMu.Unlock()

	active, ok := lastActiveSpeaker[esn]
	if !ok && len(VoicePersonas) > 0 {
		active = ActiveSpeakerInfo{
			PersonaID:   VoicePersonas[len(VoicePersonas)-1].ID,
			PersonaName: VoicePersonas[len(VoicePersonas)-1].Name,
		}
	}
	if active.PersonaID == "" {
		return ""
	}
	updated := setPersonaNameLocked(active.PersonaID, newName)
	if updated != nil {
		active.PersonaID = updated.ID
		active.PersonaName = updated.Name
		lastActiveSpeaker[esn] = active
		savePersonasLocked()
		return updated.Name
	}
	return ""
}

// RenamePersonaByID renames a specific persona by ID (or merges if target name exists).
func RenamePersonaByID(personaID, newName string) (*VoicePersona, error) {
	personasMu.Lock()
	defer personasMu.Unlock()

	updated := setPersonaNameLocked(personaID, newName)
	if updated == nil {
		return nil, fmt.Errorf("persona not found or name empty")
	}
	for esn, info := range lastActiveSpeaker {
		if info.PersonaID == personaID {
			info.PersonaID = updated.ID
			info.PersonaName = updated.Name
			lastActiveSpeaker[esn] = info
		}
	}
	savePersonasLocked()
	cp := *updated
	return &cp, nil
}

func addFactToSlice(facts *[]string, fact string) bool {
	fact = strings.TrimSpace(fact)
	if fact == "" {
		return false
	}
	for _, existing := range *facts {
		if strings.EqualFold(strings.TrimSpace(existing), fact) {
			return false
		}
	}
	*facts = append(*facts, fact)
	if len(*facts) > maxPersonaFacts {
		*facts = (*facts)[len(*facts)-maxPersonaFacts:]
	}
	return true
}

// AddActivePersonaFact adds a learned fact to the active speaker for `esn`.
func AddActivePersonaFact(esn, fact string) {
	if strings.TrimSpace(esn) == "" {
		esn = "default"
	}
	personasMu.Lock()
	defer personasMu.Unlock()

	active, ok := lastActiveSpeaker[esn]
	if !ok || active.PersonaID == "" {
		if len(VoicePersonas) == 0 {
			return
		}
		active.PersonaID = VoicePersonas[len(VoicePersonas)-1].ID
	}
	for i := range VoicePersonas {
		if VoicePersonas[i].ID == active.PersonaID {
			if addFactToSlice(&VoicePersonas[i].Facts, fact) {
				logger.Println(fmt.Sprintf("(Voice Persona) Stored fact for %s (%s): %s", VoicePersonas[i].Name, VoicePersonas[i].ID, fact))
				savePersonasLocked()
			}
			return
		}
	}
}

func AddPersonaFactByID(personaID, fact string) error {
	personasMu.Lock()
	defer personasMu.Unlock()
	for i := range VoicePersonas {
		if VoicePersonas[i].ID == personaID {
			if addFactToSlice(&VoicePersonas[i].Facts, fact) {
				savePersonasLocked()
			}
			return nil
		}
	}
	return fmt.Errorf("persona not found")
}

func DeletePersonaFactByID(personaID string, factIndex int) error {
	personasMu.Lock()
	defer personasMu.Unlock()
	for i := range VoicePersonas {
		if VoicePersonas[i].ID == personaID {
			if factIndex < 0 || factIndex >= len(VoicePersonas[i].Facts) {
				return fmt.Errorf("fact index out of range")
			}
			VoicePersonas[i].Facts = append(VoicePersonas[i].Facts[:factIndex], VoicePersonas[i].Facts[factIndex+1:]...)
			savePersonasLocked()
			return nil
		}
	}
	return fmt.Errorf("persona not found")
}

func DeletePersonaByID(personaID string) error {
	personasMu.Lock()
	defer personasMu.Unlock()
	for i := range VoicePersonas {
		if VoicePersonas[i].ID == personaID {
			VoicePersonas = append(VoicePersonas[:i], VoicePersonas[i+1:]...)
			for esn, info := range lastActiveSpeaker {
				if info.PersonaID == personaID {
					delete(lastActiveSpeaker, esn)
				}
			}
			savePersonasLocked()
			return nil
		}
	}
	return fmt.Errorf("persona not found")
}

// RecordPersonaQuestion logs a question/request asked by the active speaker on `esn`.
func RecordPersonaQuestion(esn, question string) {
	question = strings.TrimSpace(question)
	if question == "" {
		return
	}
	if strings.TrimSpace(esn) == "" {
		esn = "default"
	}
	personasMu.Lock()
	defer personasMu.Unlock()

	active, ok := lastActiveSpeaker[esn]
	if !ok || active.PersonaID == "" {
		return
	}
	for i := range VoicePersonas {
		if VoicePersonas[i].ID == active.PersonaID {
			qs := VoicePersonas[i].Questions
			// Avoid duplicate logging if CreateAIReq is called twice for the same turn
			if len(qs) > 0 && strings.EqualFold(qs[len(qs)-1].Text, question) {
				return
			}
			VoicePersonas[i].Questions = append(VoicePersonas[i].Questions, PersonaQuestion{
				Text:      question,
				Timestamp: time.Now().Format("2006-01-02 15:04"),
			})
			if len(VoicePersonas[i].Questions) > maxPersonaQuestions {
				VoicePersonas[i].Questions = VoicePersonas[i].Questions[len(VoicePersonas[i].Questions)-maxPersonaQuestions:]
			}
			savePersonasLocked()
			return
		}
	}
}

// GetActivePersonaChat returns the remembered chat messages for the currently active speaker on `esn`.
func GetActivePersonaChat(esn string) ([]openai.ChatCompletionMessage, string) {
	if strings.TrimSpace(esn) == "" {
		esn = "default"
	}
	personasMu.RLock()
	defer personasMu.RUnlock()

	active, ok := lastActiveSpeaker[esn]
	if !ok || active.PersonaID == "" {
		return nil, ""
	}
	for _, p := range VoicePersonas {
		if p.ID == active.PersonaID {
			out := make([]openai.ChatCompletionMessage, len(p.Chats))
			copy(out, p.Chats)
			return out, p.Name
		}
	}
	return nil, ""
}

// RememberActivePersonaChat appends a user+assistant exchange to the active persona's chat history.
func RememberActivePersonaChat(user, ai openai.ChatCompletionMessage, esn string) bool {
	if strings.TrimSpace(esn) == "" {
		esn = "default"
	}
	personasMu.Lock()
	defer personasMu.Unlock()

	active, ok := lastActiveSpeaker[esn]
	if !ok || active.PersonaID == "" {
		return false
	}
	for i := range VoicePersonas {
		if VoicePersonas[i].ID == active.PersonaID {
			VoicePersonas[i].Chats = append(VoicePersonas[i].Chats, user, ai)
			if len(VoicePersonas[i].Chats) > maxPersonaChats {
				VoicePersonas[i].Chats = VoicePersonas[i].Chats[len(VoicePersonas[i].Chats)-maxPersonaChats:]
			}
			savePersonasLocked()
			return true
		}
	}
	return false
}

// ClearAllPersonaChats clears conversation history across all personas while preserving their voiceprints & facts.
func ClearAllPersonaChats() {
	personasMu.Lock()
	defer personasMu.Unlock()
	for i := range VoicePersonas {
		VoicePersonas[i].Chats = []openai.ChatCompletionMessage{}
	}
	savePersonasLocked()
}

// GetPersonasSnapshot returns a safe copy of all personas and the most recent active speaker info.
func GetPersonasSnapshot() ([]VoicePersona, map[string]ActiveSpeakerInfo) {
	personasMu.RLock()
	defer personasMu.RUnlock()

	list := make([]VoicePersona, len(VoicePersonas))
	for i, p := range VoicePersonas {
		list[i] = p
		list[i].Facts = append([]string{}, p.Facts...)
		list[i].Questions = append([]PersonaQuestion{}, p.Questions...)
		list[i].Chats = append([]openai.ChatCompletionMessage{}, p.Chats...)
	}
	activeMap := make(map[string]ActiveSpeakerInfo, len(lastActiveSpeaker))
	for k, v := range lastActiveSpeaker {
		activeMap[k] = v
	}
	return list, activeMap
}

// BuildPersonaPromptContext constructs the speaker profile and household memory block for the LLM prompt.
func BuildPersonaPromptContext(esn string) string {
	if strings.TrimSpace(esn) == "" {
		esn = "default"
	}
	personasMu.RLock()
	defer personasMu.RUnlock()

	if len(VoicePersonas) == 0 {
		return ""
	}

	active, hasActive := lastActiveSpeaker[esn]
	var current *VoicePersona
	if hasActive {
		for i := range VoicePersonas {
			if VoicePersonas[i].ID == active.PersonaID {
				current = &VoicePersonas[i]
				break
			}
		}
	}

	var b strings.Builder
	b.WriteString("\n\nVOICE PERSONA & USER PROFILING SYSTEM:\n")
	b.WriteString("Wire-Pod identifies different household speakers by their voiceprint and keeps a persistent profile of facts and past questions for each person.\n")

	if current != nil {
		confPct := int(math.Round(active.Similarity * 100))
		if confPct > 100 {
			confPct = 100
		}
		if current.Named {
			b.WriteString(fmt.Sprintf("Current Speaker: %s (ID: %s, Voice match confidence: %d%%).\n", current.Name, current.ID, confPct))
		} else {
			b.WriteString(fmt.Sprintf("Current Speaker: %s (unregistered name, ID: %s, Voice match confidence: %d%%). If they tell you their name, bind it using the setSpeakerName command.\n", current.Name, current.ID, confPct))
		}
		if len(current.Facts) > 0 {
			b.WriteString(fmt.Sprintf("Known Facts about %s: %s.\n", current.Name, strings.Join(current.Facts, "; ")))
		} else {
			b.WriteString(fmt.Sprintf("Known Facts about %s: None recorded yet.\n", current.Name))
		}
		if len(current.Questions) > 0 {
			start := 0
			if len(current.Questions) > 8 {
				start = len(current.Questions) - 8
			}
			var qList []string
			for _, q := range current.Questions[start:] {
				qList = append(qList, fmt.Sprintf("\"%s\" (%s)", q.Text, q.Timestamp))
			}
			b.WriteString(fmt.Sprintf("Recent Questions/Things %s has said: %s.\n", current.Name, strings.Join(qList, "; ")))
		}
	}

	// Include brief summary of other household profiles so Vector can answer questions about them too
	var others []string
	for i := range VoicePersonas {
		p := &VoicePersonas[i]
		if current != nil && p.ID == current.ID {
			continue
		}
		summary := p.Name
		var details []string
		if len(p.Facts) > 0 {
			details = append(details, "facts: "+strings.Join(p.Facts, "; "))
		}
		if len(p.Questions) > 0 {
			start := 0
			if len(p.Questions) > 5 {
				start = len(p.Questions) - 5
			}
			var rq []string
			for _, q := range p.Questions[start:] {
				rq = append(rq, fmt.Sprintf("\"%s\"", q.Text))
			}
			details = append(details, "recent questions: "+strings.Join(rq, ", "))
		}
		if len(details) > 0 {
			summary += " (" + strings.Join(details, " | ") + ")"
		}
		others = append(others, summary)
	}
	if len(others) > 0 {
		b.WriteString("Other Household Voice Profiles on File: " + strings.Join(others, " / ") + ".\n")
	}

	return b.String()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

