package wirepod_vosk

import (
	"archive/zip"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	vosk "github.com/kercre123/vosk-api/go"
	"github.com/kercre123/wire-pod/chipper/pkg/logger"
	"github.com/kercre123/wire-pod/chipper/pkg/vars"
	sr "github.com/kercre123/wire-pod/chipper/pkg/wirepod/speechrequest"
)

var GrammerEnable bool = false

var Name string = "vosk"

var model *vosk.VoskModel
var spkModel *vosk.VoskSpkModel
var recsmu sync.Mutex

var grmRecs []ARec
var gpRecs []ARec

var modelLoaded bool

type ARec struct {
	InUse bool
	Rec   *vosk.VoskRecognizer
}

var Grammer string

const voskSpkModelURL = "https://alphacephei.com/vosk/models/vosk-model-spk-0.4.zip"

func ensureSpkModel() string {
	spkBaseDir := filepath.Join(vars.VoskModelPath, "spk")
	spkModelDir := filepath.Join(spkBaseDir, "model")
	if _, err := os.Stat(spkModelDir); err == nil {
		return spkModelDir
	}

	logger.Println("Downloading VOSK Speaker Identification model (vosk-model-spk-0.4)...")
	if err := os.MkdirAll(spkBaseDir, 0755); err != nil {
		logger.Println("Failed to create spk model directory: " + err.Error())
		return ""
	}

	zipTmp := filepath.Join(os.TempDir(), "vosk-model-spk-0.4.zip")
	client := &http.Client{
		Timeout: 2 * time.Minute,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	resp, err := client.Get(voskSpkModelURL)
	if err != nil {
		logger.Println("Could not download VOSK speaker model: " + err.Error())
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Println(fmt.Sprintf("Could not download VOSK speaker model: HTTP %d", resp.StatusCode))
		return ""
	}

	out, err := os.Create(zipTmp)
	if err != nil {
		logger.Println("Could not create temp zip for speaker model: " + err.Error())
		return ""
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		logger.Println("Error saving speaker model zip: " + err.Error())
		return ""
	}
	out.Close()
	defer os.Remove(zipTmp)

	if err := unzipSpkArchive(zipTmp, spkBaseDir); err != nil {
		logger.Println("Error unpacking speaker model zip: " + err.Error())
		return ""
	}

	extracted := filepath.Join(spkBaseDir, "vosk-model-spk-0.4")
	if _, err := os.Stat(extracted); err == nil {
		_ = os.Rename(extracted, spkModelDir)
	}
	if _, err := os.Stat(spkModelDir); err != nil {
		logger.Println("Speaker model directory missing after unpack: " + err.Error())
		return ""
	}
	logger.Println("VOSK Speaker Identification model downloaded and installed to " + spkModelDir)
	return spkModelDir
}

func unzipSpkArchive(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(fpath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path: %s", fpath)
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}
		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}
		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func Init() error {
	if os.Getenv("VOSK_WITH_GRAMMER") == "true" {
		fmt.Println("Initializing vosk with grammer optimizations")
		GrammerEnable = true
	}
	if vars.APIConfig.PastInitialSetup {
		vosk.SetLogLevel(-1)
		if modelLoaded {
			logger.Println("A model was already loaded, freeing all recognizers and model")
			for ind := range grmRecs {
				grmRecs[ind].Rec.Free()
			}
			for ind := range gpRecs {
				gpRecs[ind].Rec.Free()
			}
			gpRecs = []ARec{}
			grmRecs = []ARec{}
			if spkModel != nil {
				spkModel.Free()
				spkModel = nil
			}
			model.Free()
		}
		sttLanguage := vars.APIConfig.STT.Language
		if len(sttLanguage) == 0 {
			sttLanguage = "en-US"
		}
		modelPath := filepath.Join(vars.VoskModelPath, sttLanguage, "model")
		if _, err := os.Stat(modelPath); err != nil {
			fmt.Println("Path does not exist: " + modelPath)
			return err
		}
		logger.Println("Opening VOSK model (" + modelPath + ")")
		aModel, err := vosk.NewModel(modelPath)
		if err != nil {
			log.Fatal(err)
			return err
		}
		model = aModel

		if spkDir := ensureSpkModel(); spkDir != "" {
			logger.Println("Opening VOSK speaker model (" + spkDir + ")")
			sModel, spkErr := vosk.NewSpkModel(spkDir)
			if spkErr != nil {
				logger.Println("Warning: failed to load VOSK speaker model: " + spkErr.Error())
			} else {
				spkModel = sModel
				logger.Println("VOSK speaker model loaded successfully")
			}
		}

		if GrammerEnable {
			logger.Println("Initializing grammer list")
			Grammer = GetGrammerList(vars.APIConfig.STT.Language)
		}

		logger.Println("Initializing VOSK recognizers")
		if GrammerEnable {
			grmRecognizer, err := vosk.NewRecognizerGrm(aModel, 16000.0, Grammer)
			if err != nil {
				log.Fatal(err)
			}
			if spkModel != nil {
				grmRecognizer.SetSpkModel(spkModel)
			}
			var grmrec ARec
			grmrec.Rec = grmRecognizer
			grmrec.InUse = false
			grmRecs = append(grmRecs, grmrec)
		}
		gpRecognizer, err := vosk.NewRecognizer(aModel, 16000.0)
		if err != nil {
			log.Fatal(err)
		}
		if spkModel != nil {
			gpRecognizer.SetSpkModel(spkModel)
		}
		var gprec ARec
		gprec.Rec = gpRecognizer
		gprec.InUse = false
		gpRecs = append(gpRecs, gprec)

		modelLoaded = true
		logger.Println("VOSK initiated successfully")
		runTest()
	}
	return nil
}

type voskWakeWord struct {
	Word string  `json:"word"`
	Conf float64 `json:"conf"`
}

type voskWakeResult struct {
	Text    string         `json:"text"`
	Partial string         `json:"partial"`
	Result  []voskWakeWord `json:"result"`
}

func pcmRMS(pcm []byte) float64 {
	n := len(pcm) / 2
	if n == 0 {
		return 0
	}
	var sumSq float64
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int16(uint16(pcm[i]) | (uint16(pcm[i+1]) << 8))
		v := float64(sample)
		sumSq += v * v
	}
	return sumSq / float64(n)
}

func checkWakeResultJSON(raw string) bool {
	var res voskWakeResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return false
	}
	txt := strings.TrimSpace(res.Text)
	if txt != "vector" && txt != "hey vector" {
		return false
	}
	for _, w := range res.Result {
		if w.Word == "vector" && w.Conf >= 0.80 {
			return true
		}
	}
	return false
}

func createWakeWordRecognizer() (func(pcm []byte) bool, func()) {
	recsmu.Lock()
	defer recsmu.Unlock()
	if !modelLoaded || model == nil {
		return nil, nil
	}
	rec, err := vosk.NewRecognizerGrm(model, 16000.0, `["vector", "hey vector", "[unk]"]`)
	if err != nil || rec == nil {
		return nil, nil
	}
	rec.SetWords(1)
	var mu sync.Mutex
	partialHold := 0
	quietAfterPartial := 0

	feed := func(pcm []byte) bool {
		if len(pcm) == 0 {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		if rec.AcceptWaveform(pcm) != 0 {
			raw := rec.Result()
			partialHold = 0
			quietAfterPartial = 0
			if checkWakeResultJSON(raw) {
				rec.Reset()
				return true
			}
			return false
		}
		var pRes voskWakeResult
		_ = json.Unmarshal([]byte(rec.PartialResult()), &pRes)
		p := strings.TrimSpace(pRes.Partial)
		if p == "vector" || p == "hey vector" {
			partialHold++
			// Mean-square < 90000 corresponds to RMS < 300 (quiet pause after speaking "Vector")
			if pcmRMS(pcm) < 90000 {
				quietAfterPartial++
			} else {
				quietAfterPartial = 0
			}
			if partialHold >= 2 && quietAfterPartial >= 2 {
				raw := rec.FinalResult()
				partialHold = 0
				quietAfterPartial = 0
				rec.Reset()
				if checkWakeResultJSON(raw) {
					return true
				}
			}
		} else {
			partialHold = 0
			quietAfterPartial = 0
		}
		return false
	}

	closeFn := func() {
		mu.Lock()
		defer mu.Unlock()
		if rec != nil {
			rec.Free()
			rec = nil
		}
	}

	return feed, closeFn
}

func runTest() {
	// make sure recognizer is all loaded into RAM
	logger.Println("Running recognizer test")
	var withGrm bool
	if GrammerEnable {
		logger.Println("Using grammer-optimized recognizer")
		withGrm = true
	} else {
		logger.Println("Using general recognizer")
		withGrm = false
	}
	rec, recind := getRec(withGrm)
	sttTestPath := "./stttest.pcm"
	if runtime.GOOS == "android" {
		sttTestPath = vars.AndroidPath + "/static/stttest.pcm"
	}
	pcmBytes, _ := os.ReadFile(sttTestPath)
	var micData [][]byte
	cTime := time.Now()
	micData = sr.SplitVAD(pcmBytes)
	for _, sample := range micData {
		rec.AcceptWaveform(sample)
	}
	var jres map[string]interface{}
	json.Unmarshal([]byte(rec.FinalResult()), &jres)
	releaseRec(withGrm, recind)
	transcribedText, _ := jres["text"].(string)
	tTime := time.Since(cTime)
	logger.Println("Text (from test):", transcribedText)
	if tTime.Seconds() > 3 {
		logger.Println("Vosk test took a while, performance may be degraded. (" + fmt.Sprint(tTime) + ")")
	}
	logger.Println("Vosk test successful! (Took " + fmt.Sprint(tTime) + ")")
}

func releaseRec(withGrm bool, recind int) {
	recsmu.Lock()
	defer recsmu.Unlock()
	if withGrm && GrammerEnable {
		if recind >= 0 && recind < len(grmRecs) {
			grmRecs[recind].InUse = false
		}
	} else {
		if recind >= 0 && recind < len(gpRecs) {
			gpRecs[recind].InUse = false
		}
	}
}

func getRec(withGrm bool) (*vosk.VoskRecognizer, int) {
	recsmu.Lock()
	if withGrm && GrammerEnable {
		for ind, rec := range grmRecs {
			if !rec.InUse {
				grmRecs[ind].InUse = true
				r := grmRecs[ind].Rec
				recsmu.Unlock()
				r.Reset()
				return r, ind
			}
		}
	} else {
		for ind, rec := range gpRecs {
			if !rec.InUse {
				gpRecs[ind].InUse = true
				r := gpRecs[ind].Rec
				recsmu.Unlock()
				r.Reset()
				return r, ind
			}
		}
	}
	recsmu.Unlock()

	var newrec ARec
	var newRec *vosk.VoskRecognizer
	var err error
	newrec.InUse = true
	if withGrm {
		newRec, err = vosk.NewRecognizerGrm(model, 16000.0, Grammer)
	} else {
		newRec, err = vosk.NewRecognizer(model, 16000.0)
	}
	if err != nil {
		log.Fatal(err)
	}
	if spkModel != nil {
		newRec.SetSpkModel(spkModel)
	}
	newrec.Rec = newRec
	recsmu.Lock()
	defer recsmu.Unlock()
	if withGrm {
		grmRecs = append(grmRecs, newrec)
		return grmRecs[len(grmRecs)-1].Rec, len(grmRecs) - 1
	} else {
		gpRecs = append(gpRecs, newrec)
		return gpRecs[len(gpRecs)-1].Rec, len(gpRecs) - 1
	}
}

func STT(req sr.SpeechRequest) (string, error) {
	logger.Println("(Bot " + req.Device + ", Vosk) Processing...")
	var withGrm bool
	if (vars.APIConfig.Knowledge.IntentGraph || req.IsKG) || !GrammerEnable {
		logger.Println("Using general recognizer")
		withGrm = false
	} else {
		logger.Println("Using grammer-optimized recognizer")
		withGrm = true
	}
	rec, recind := getRec(withGrm)
	defer releaseRec(withGrm, recind)

	rec.SetWords(1)
	rec.AcceptWaveform(req.FirstReq)
	req.DetectEndOfSpeech()
	for {
		chunk, err := req.GetNextStreamChunk()
		if err != nil {
			return "", err
		}
		speechIsDone, doProcess := req.DetectEndOfSpeech()
		if doProcess {
			rec.AcceptWaveform(chunk)
		}
		if speechIsDone {
			break
		}
	}
	var jres map[string]interface{}
	json.Unmarshal([]byte(rec.FinalResult()), &jres)
	transcribedText, _ := jres["text"].(string)
	logger.Println("Bot " + req.Device + " Transcribed text: " + transcribedText)

	if strings.TrimSpace(transcribedText) != "" {
		if spkRaw, ok := jres["spk"].([]interface{}); ok && len(spkRaw) > 0 {
			spkVec := make([]float64, 0, len(spkRaw))
			for _, v := range spkRaw {
				if f, ok := v.(float64); ok {
					spkVec = append(spkVec, f)
				}
			}
			spkFrames := 0
			if sf, ok := jres["spk_frames"].(float64); ok {
				spkFrames = int(sf)
			}
			if len(spkVec) > 0 {
				vars.IdentifySpeaker(req.Device, spkVec, spkFrames, transcribedText)
			}
		}
	}

	return transcribedText, nil
}
