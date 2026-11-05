package wirepod_ttr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/fforchino/vector-go-sdk/pkg/vector"
	"github.com/fforchino/vector-go-sdk/pkg/vectorpb"
	"github.com/kercre123/wire-pod/chipper/pkg/logger"
	"github.com/kercre123/wire-pod/chipper/pkg/vars"
	"github.com/sashabaranov/go-openai"
)

func GetChat(esn string) vars.RememberedChat {
	for _, chat := range vars.RememberedChats {
		if chat.ESN == esn {
			return chat
		}
	}
	return vars.RememberedChat{
		ESN: esn,
	}
}

func PlaceChat(chat vars.RememberedChat) {
	for i, achat := range vars.RememberedChats {
		if achat.ESN == chat.ESN {
			vars.RememberedChats[i] = chat
			return
		}
	}
	vars.RememberedChats = append(vars.RememberedChats, chat)
}

// remember last 16 lines of chat
func Remember(user, ai openai.ChatCompletionMessage, esn string) {
	vars.RememberActivePersonaChat(user, ai, esn)
	chatAppend := []openai.ChatCompletionMessage{
		user,
		ai,
	}
	currentChat := GetChat(esn)
	if len(currentChat.Chats) == 16 {
		var newChat vars.RememberedChat
		newChat.ESN = currentChat.ESN
		for i, chat := range currentChat.Chats {
			if i < 2 {
				continue
			}
			newChat.Chats = append(newChat.Chats, chat)
		}
		currentChat = newChat
	}
	currentChat.ESN = esn
	currentChat.Chats = append(currentChat.Chats, chatAppend...)
	PlaceChat(currentChat)
}

func isMn(r rune) bool {
	// Remove the characters that are not related to Vietnamese.
	// Retain the tonal marks and diacritics such as the circumflex, ơ, and ư in Vietnamese.
	keepMarks := []rune{
		'\u0300', // Dấu huyền
		'\u0301', // Dấu sắc
		'\u0303', // Dấu ngã
		'\u0309', // Dấu hỏi
		'\u0323', // Dấu nặng
		'\u0302', // Dấu mũ (â, ê, ô)
		'\u031B', // Dấu ơ và ư
		'\u0306', // Dấu trầm
	}
	if unicode.Is(unicode.Mn, r) {
		for _, mark := range keepMarks {
			if r == mark {
				return false
			}
		}
		return true
	}
	return false
}

func removeSpecialCharacters(str string) string {

	// these two lines create a transformation that decomposes characters, removes non-spacing marks (like diacritics), and then recomposes the characters, effectively removing special characters
	t := transform.Chain(norm.NFD, transform.RemoveFunc(isMn), norm.NFC)
	result, _, _ := transform.String(t, str)

	// Define the regular expression to match special characters
	re := regexp.MustCompile(`[&^*#@]`)

	// Replace special characters with an empty string
	result = removeEmojis(re.ReplaceAllString(result, ""))

	// Replace special characters with ASCII
	// * COPY/PASTE TO ADD MORE CHARACTERS:
	//   result = strings.ReplaceAll(result, "", "")
	result = strings.ReplaceAll(result, "‘", "'")
	result = strings.ReplaceAll(result, "’", "'")
	result = strings.ReplaceAll(result, "“", "\"")
	result = strings.ReplaceAll(result, "”", "\"")
	result = strings.ReplaceAll(result, "—", "-")
	result = strings.ReplaceAll(result, "–", "-")
	result = strings.ReplaceAll(result, "…", "...")
	result = strings.ReplaceAll(result, "\u00A0", " ")
	result = strings.ReplaceAll(result, "•", "*")
	result = strings.ReplaceAll(result, "¼", "1/4")
	result = strings.ReplaceAll(result, "½", "1/2")
	result = strings.ReplaceAll(result, "¾", "3/4")
	result = strings.ReplaceAll(result, "×", "x")
	result = strings.ReplaceAll(result, "÷", "/")
	result = strings.ReplaceAll(result, "ç", "c")
	result = strings.ReplaceAll(result, "©", "(c)")
	result = strings.ReplaceAll(result, "®", "(r)")
	result = strings.ReplaceAll(result, "™", "(tm)")
	result = strings.ReplaceAll(result, "@", "(a)")
	result = strings.ReplaceAll(result, " AI ", " A. I. ")
	return result
}

func removeEmojis(input string) string {
	// a mess, but it works!
	re := regexp.MustCompile(`[\x{1F600}-\x{1F64F}]|[\x{1F300}-\x{1F5FF}]|[\x{1F680}-\x{1F6FF}]|[\x{1F1E0}-\x{1F1FF}]|[\x{2600}-\x{26FF}]|[\x{2700}-\x{27BF}]|[\x{1F900}-\x{1F9FF}]|[\x{1F004}]|[\x{1F0CF}]|[\x{1F18E}]|[\x{1F191}-\x{1F251}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]|[\x{1F004}-\x{1F0CF}]|[\x{1F191}-\x{1F251}]|[\x{2B50}]`)
	result := re.ReplaceAllString(input, "")
	return result
}

func CreateAIReq(transcribedText, esn string, gpt3tryagain, isKG bool) openai.ChatCompletionRequest {
	defaultPrompt := "You are a helpful, animated robot called Vector. Keep the response concise yet informative."

	var nChat []openai.ChatCompletionMessage

	smsg := openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleSystem,
	}
	if strings.TrimSpace(vars.APIConfig.Knowledge.OpenAIPrompt) != "" {
		smsg.Content = strings.TrimSpace(vars.APIConfig.Knowledge.OpenAIPrompt)
	} else {
		smsg.Content = defaultPrompt
	}

	var model string

	if gpt3tryagain {
		model = openai.GPT3Dot5Turbo
	} else if vars.APIConfig.Knowledge.Provider == "openai" {
		model = openai.GPT4oMini
		if m := strings.TrimSpace(vars.APIConfig.Knowledge.Model); m != "" {
			model = m
		}
		logger.Println("Using " + model)
	} else {
		logger.Println("Using " + vars.APIConfig.Knowledge.Model)
		model = vars.APIConfig.Knowledge.Model
	}

	if !gpt3tryagain {
		vars.RecordPersonaQuestion(esn, transcribedText)
	}

	smsg.Content = CreatePrompt(smsg.Content, model, isKG) + vars.BuildPersonaPromptContext(esn)

	nChat = append(nChat, smsg)
	if vars.APIConfig.Knowledge.SaveChat {
		if personaChats, personaName := vars.GetActivePersonaChat(esn); personaName != "" {
			logger.Println("Using remembered chats for persona " + personaName + ", length of " + fmt.Sprint(len(personaChats)) + " messages")
			nChat = append(nChat, personaChats...)
		} else {
			rchat := GetChat(esn)
			logger.Println("Using remembered chats, length of " + fmt.Sprint(len(rchat.Chats)) + " messages")
			nChat = append(nChat, rchat.Chats...)
		}
	}
	nChat = append(nChat, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: transcribedText,
	})

	temp := float32(1)
	if vars.APIConfig.Knowledge.Temperature > 0 {
		temp = vars.APIConfig.Knowledge.Temperature
	}
	topP := float32(1)
	if vars.APIConfig.Knowledge.TopP > 0 {
		topP = vars.APIConfig.Knowledge.TopP
	}

	aireq := openai.ChatCompletionRequest{
		Model:            model,
		Temperature:      temp,
		TopP:             topP,
		FrequencyPenalty: 0,
		PresencePenalty:  0,
		Messages:         nChat,
		Stream:           true,
	}
	setMaxTokens(&aireq)
	return aireq
}

func newLLMClient() *openai.Client {
	switch vars.APIConfig.Knowledge.Provider {
	case "together":
		if vars.APIConfig.Knowledge.Model == "" {
			vars.APIConfig.Knowledge.Model = "meta-llama/Llama-3-70b-chat-hf"
			vars.WriteConfigToDisk()
		}
		conf := openai.DefaultConfig(vars.APIConfig.Knowledge.Key)
		conf.BaseURL = "https://api.together.xyz/v1"
		return openai.NewClientWithConfig(conf)
	case "custom":
		key := vars.APIConfig.Knowledge.Key
		if strings.TrimSpace(key) == "" {
			key = "lm-studio"
		}
		conf := openai.DefaultConfig(key)
		ep := strings.TrimRight(strings.TrimSpace(vars.APIConfig.Knowledge.Endpoint), "/")
		if ep != "" && !strings.HasSuffix(ep, "/v1") {
			ep = ep + "/v1"
		}
		conf.BaseURL = ep
		return openai.NewClientWithConfig(conf)
	default:
		return openai.NewClient(vars.APIConfig.Knowledge.Key)
	}
}

// setMaxTokens applies the configured response budget. Reasoning models spend
// most of it thinking (often 1000+ tokens for a simple question), so local and
// together providers get a larger default. They are sent max_tokens, which every
// OpenAI-compatible server understands; OpenAI itself gets max_completion_tokens.
func setMaxTokens(req *openai.ChatCompletionRequest) {
	limit := vars.APIConfig.Knowledge.MaxTokens
	if vars.APIConfig.Knowledge.Provider == "openai" {
		if limit <= 0 {
			limit = 2048
		}
		req.MaxCompletionTokens = limit
		return
	}
	if limit <= 0 {
		limit = 8192
	}
	req.MaxTokens = limit
}

func StreamingKGSim(req interface{}, esn string, transcribedText string, isKG bool) (string, error) {
	start := make(chan bool, 1)
	stop := make(chan bool, 1)
	stopStop := make(chan bool, 1)
	kgReadyToAnswer := make(chan bool, 1)
	kgStopLooping := false

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	watchdog := NewWatchdog("StreamingKGSim:"+esn, 90*time.Second, nil)
	defer watchdog.Stop()

	matched := false
	var robot *vector.Vector
	var guid string
	var target string
	for _, bot := range vars.BotInfo.Robots {
		if esn == bot.Esn {
			guid = bot.GUID
			target = bot.IPAddress + ":443"
			matched = true
			break
		}
	}
	if matched {
		var err error
		robot, err = vector.New(vector.WithSerialNo(esn), vector.WithToken(guid), vector.WithTarget(target))
		if err != nil {
			return err.Error(), err
		}
	}
	_, err := robot.Conn.BatteryState(ctx, &vectorpb.BatteryStateRequest{})
	if err != nil {
		return "", err
	}
	needTurn := ShouldTurnOnWake(esn)
	if !isKG {
		if needTurn {
			IntentPass(req, "intent_imperative_lookatme", transcribedText, map[string]string{}, false)
			WaitForWakeTurnOrFace(esn, 1600*time.Millisecond)
		} else {
			IntentPass(req, "intent_greeting_hello", transcribedText, map[string]string{}, false)
		}
		time.Sleep(time.Millisecond * 200)
	}

	BControl(robot, ctx, start, stop)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if kgStopLooping {
				SafeSendBool(kgReadyToAnswer, true)
				break
			}
			robot.Conn.PlayAnimation(ctx, &vectorpb.PlayAnimationRequest{
				Animation: &vectorpb.Animation{
					Name: "anim_knowledgegraph_searching_01",
				},
				Loops: 1,
			})
			time.Sleep(time.Second / 3)
		}
	}()
	var fullRespText string
	var fullfullRespText string
	var fullRespSlice []string
	var isDone bool
	c := newLLMClient()
	// buffered so a "new content" signal is never lost if the speaker isn't waiting yet
	speakReady := make(chan string, 1)
	successIntent := make(chan bool, 1)

	aireq := CreateAIReq(transcribedText, esn, false, isKG)
	// strips <think>...</think> etc. so Vector doesn't speak a reasoning model's thoughts
	reasoning := newReasoningFilter(aireq.Model)

	stream, err := c.CreateChatCompletionStream(ctx, aireq)
	if err != nil {
		log.Printf("Error creating chat completion stream: %v", err)
		if strings.Contains(err.Error(), "does not exist") && vars.APIConfig.Knowledge.Provider == "openai" {
			logger.Println("GPT-4 model cannot be accessed with this API key. You likely need to add more than $5 dollars of funds to your OpenAI account.")
			logger.LogUI("GPT-4 model cannot be accessed with this API key. You likely need to add more than $5 dollars of funds to your OpenAI account.")
			aireq := CreateAIReq(transcribedText, esn, true, isKG)
			logger.Println("Falling back to " + aireq.Model)
			logger.LogUI("Falling back to " + aireq.Model)
			stream, err = c.CreateChatCompletionStream(ctx, aireq)
			if err != nil {
				logger.Println("OpenAI still not returning a response even after falling back. Erroring.")
				return "", err
			}
		} else {
			kgStopLooping = true
			WaitSignalWithTimeout(ctx, kgReadyToAnswer, 2*time.Second, "kgReadyToAnswer")
			SafeSendBool(stop, true)
			time.Sleep(time.Second / 3)
			KGSim(esn, "There was an error getting data from the L. L. M.")
			return "", err
		}
	}
	nChat := aireq.Messages
	nChat = append(nChat, openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleAssistant,
	})
	fmt.Println("LLM stream response: ")
	go func() {
		for {
			response, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				// speak whatever is left once reasoning has been removed
				tail := removeSpecialCharacters(reasoning.Flush())
				fullfullRespText = fullfullRespText + tail
				fullRespText = fullRespText + tail
				if len(fullRespSlice) == 0 && strings.TrimSpace(fullRespText) != "" {
					// the whole answer had no sentence-ending punctuation
					fullRespSlice = append(fullRespSlice, strings.TrimSpace(fullRespText))
					fullfullRespText = fullRespSlice[0]
					fullRespText = ""
					isDone = true
					select {
					case successIntent <- true:
					default:
					}
				}
				// prevents a crash
				if len(fullRespSlice) == 0 {
					logger.Println("LLM returned no response")
					successIntent <- false
					kgStopLooping = true
					for range kgReadyToAnswer {
						break
					}
					stop <- true
					time.Sleep(time.Second / 3)
					KGSim(esn, "There was an error getting data from the L. L. M.")
					break
				}
				isDone = true
				// if fullRespSlice != fullRespText, add that missing bit to fullRespSlice
				newStr := fullRespSlice[0]
				for i, str := range fullRespSlice {
					if i == 0 {
						continue
					}
					newStr = newStr + " " + str
				}
				if strings.TrimSpace(newStr) != strings.TrimSpace(fullfullRespText) {
					logger.Println("LLM debug: there is content after the last punctuation mark")
					extraBit := strings.TrimPrefix(fullRespText, newStr)
					fullRespSlice = append(fullRespSlice, extraBit)
				}
				isDone = true
				select {
				case speakReady <- "":
				default:
				}
				if vars.APIConfig.Knowledge.SaveChat {
					Remember(openai.ChatCompletionMessage{
						Role:    openai.ChatMessageRoleUser,
						Content: transcribedText,
					},
						openai.ChatCompletionMessage{
							Role:    openai.ChatMessageRoleAssistant,
							Content: newStr,
						},
						esn)
				}
				logger.LogUI("LLM response for " + esn + ": " + newStr)
				logger.Println("LLM stream finished")
				return
			}

			if err != nil {
				logger.Println("Stream error: " + err.Error())
				isDone = true
				select {
				case successIntent <- false:
				default:
				}
				select {
				case speakReady <- "":
				default:
				}
				return
			}

			if len(response.Choices) == 0 {
				// some servers send keep-alive/usage chunks with no choices
				continue
			}

			content := removeSpecialCharacters(reasoning.Feed(response.Choices[0].Delta.Content))
			fullfullRespText = fullfullRespText + content
			fullRespText = fullRespText + content
			if sents, rest := extractSentences(fullRespText); len(sents) > 0 {
				fullRespSlice = append(fullRespSlice, sents...)
				fullRespText = rest
				select {
				case successIntent <- true:
				default:
				}
				select {
				case speakReady <- sents[len(sents)-1]:
				default:
				}
			}
		}
	}()
	select {
	case is := <-successIntent:
		if !is {
			kgStopLooping = true
			WaitSignalWithTimeout(ctx, kgReadyToAnswer, 2*time.Second, "kgReadyToAnswer")
			SafeSendBool(stop, true)
			return "", errors.New("llm returned no response")
		}
	case <-time.After(60 * time.Second):
		kgStopLooping = true
		WaitSignalWithTimeout(ctx, kgReadyToAnswer, 2*time.Second, "kgReadyToAnswer")
		SafeSendBool(stop, true)
		return "", errors.New("timeout waiting for llm response")
	case <-ctx.Done():
		kgStopLooping = true
		SafeSendBool(stop, true)
		return "", ctx.Err()
	}

	interrupted := false
	go func() {
		interrupted = InterruptKGSimWhenTouchedOrWaked(robot, stop, stopStop)
	}()
	TTSLoopAnimation := "anim_tts_loop_02"
	TTSGetinAnimation := "anim_knowledgegraph_searching_getout_01"

	var stopTTSLoop bool
	TTSLoopStopped := make(chan bool, 1)

	var controlGranted bool
	select {
	case controlGranted = <-start:
		if !controlGranted {
			logger.Println("[DeadlockGuard] Behavior control was denied or failed")
			kgStopLooping = true
			SafeSendBool(stop, true)
			return "", errors.New("behavior control was not granted")
		}
	case <-time.After(8 * time.Second):
		logger.Println("[DeadlockGuard] Timeout waiting for behavior control")
		kgStopLooping = true
		SafeSendBool(stop, true)
		return "", errors.New("timeout waiting for behavior control")
	case <-ctx.Done():
		kgStopLooping = true
		SafeSendBool(stop, true)
		return "", ctx.Err()
	}

	kgStopLooping = true
	WaitSignalWithTimeout(ctx, kgReadyToAnswer, 2*time.Second, "kgReadyToAnswer")

	if needTurn {
		EnsureFacingUserUnderBControl(ctx, robot, esn)
	}
	robot.Conn.PlayAnimation(
		ctx,
		&vectorpb.PlayAnimationRequest{
			Animation: &vectorpb.Animation{
				Name: TTSGetinAnimation,
			},
			Loops: 1,
		},
	)
	if !vars.APIConfig.Knowledge.CommandsEnable {
		go func() {
			for {
				if stopTTSLoop {
					SafeSendBool(TTSLoopStopped, true)
					break
				}
				robot.Conn.PlayAnimation(
					ctx,
					&vectorpb.PlayAnimationRequest{
						Animation: &vectorpb.Animation{
							Name: TTSLoopAnimation,
						},
						Loops: 1,
					},
				)
			}
		}()
	}
	var disconnect bool
	numInResp := 0
	for {
		respSlice := fullRespSlice
		if len(respSlice)-1 < numInResp {
			if !isDone {
				logger.Println("Waiting for more content from LLM...")
				<-speakReady
				// re-check: the signal may mean "done" rather than "new sentence"
				continue
			}
			break
		}
		if interrupted {
			break
		}
		logger.Println(respSlice[numInResp])
		acts := GetActionsFromString(respSlice[numInResp])
		nChat[len(nChat)-1].Content = fullRespText
		disconnect = PerformActions(nChat, acts, robot, stopStop)
		if disconnect {
			break
		}
		numInResp = numInResp + 1
	}
	if !vars.APIConfig.Knowledge.CommandsEnable {
		stopTTSLoop = true
		WaitSignalWithTimeout(ctx, TTSLoopStopped, 2*time.Second, "TTSLoopStopped")
	}
	time.Sleep(time.Millisecond * 100)
	if !interrupted {
		SafeSendBool(stopStop, true)
		SafeSendBool(stop, true)
	}
	return "", nil
}

func KGSim(esn string, textToSay string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	matched := false
	var robot *vector.Vector
	var guid string
	var target string
	for _, bot := range vars.BotInfo.Robots {
		if esn == bot.Esn {
			guid = bot.GUID
			target = bot.IPAddress + ":443"
			matched = true
			break
		}
	}
	if matched {
		var err error
		robot, err = vector.New(vector.WithSerialNo(esn), vector.WithToken(guid), vector.WithTarget(target))
		if err != nil {
			return err
		}
	} else {
		return errors.New("robot not found")
	}

	start := make(chan bool, 1)
	stop := make(chan bool, 1)
	BControl(robot, ctx, start, stop)

	select {
	case granted := <-start:
		if !granted {
			return errors.New("behavior control was not granted")
		}
	case <-time.After(8 * time.Second):
		SafeSendBool(stop, true)
		return errors.New("timeout waiting for behavior control")
	case <-ctx.Done():
		SafeSendBool(stop, true)
		return ctx.Err()
	}

	robot.Conn.PlayAnimation(
		ctx,
		&vectorpb.PlayAnimationRequest{
			Animation: &vectorpb.Animation{
				Name: "anim_getin_tts_01",
			},
			Loops: 1,
		},
	)

	var stopTTSLoop bool
	TTSLoopStopped := make(chan bool, 1)
	go func() {
		for {
			if stopTTSLoop {
				SafeSendBool(TTSLoopStopped, true)
				break
			}
			robot.Conn.PlayAnimation(
				ctx,
				&vectorpb.PlayAnimationRequest{
					Animation: &vectorpb.Animation{
						Name: "anim_tts_loop_02",
					},
					Loops: 1,
				},
			)
		}
	}()

	textToSaySplit := strings.Split(textToSay, ". ")
	for _, str := range textToSaySplit {
		trimmed := strings.TrimSpace(str)
		if trimmed == "" {
			continue
		}
		if !strings.HasSuffix(trimmed, ".") {
			trimmed = trimmed + "."
		}
		_, err := robot.Conn.SayText(
			ctx,
			&vectorpb.SayTextRequest{
				Text:           trimmed,
				UseVectorVoice: false,
				DurationScalar: 1.0,
			},
		)
		if err != nil {
			logger.Println("KG SayText error:", err)
			break
		}
	}

	stopTTSLoop = true
	WaitSignalWithTimeout(ctx, TTSLoopStopped, 2*time.Second, "TTSLoopStopped")
	time.Sleep(time.Millisecond * 100)
	SafeSendBool(stop, true)
	return nil
}
