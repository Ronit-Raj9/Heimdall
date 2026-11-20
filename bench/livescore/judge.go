package livescore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/traceimport"
)

// Judge scores one redacted session from 0 (bad) to 1 (good). calibrated
// says whether a judge calibration record covers this judge; an
// uncalibrated score is reported and never decides.
type Judge interface {
	Score(ctx context.Context, s *traceimport.Session) (score float64, calibrated bool, err error)
}

// DefaultJudgePrompt asks for one number. {{transcript}} is replaced with
// the redacted transcript.
const DefaultJudgePrompt = `You review one conversation between a user and an AI agent, taken from production traffic.
Rate how well the agent served the user's request, from 0 (failed or harmful) to 1 (fully served).
Judge only what the transcript shows. Answer with one line: SCORE: <number between 0 and 1>.

{{transcript}}`

// ChatJudge calls an OpenAI-compatible chat completions endpoint (the
// gateway, with the judge's own virtual key). The prompt's sha256 is the
// prompt version a calibration record must match.
type ChatJudge struct {
	URL, Key, Model, Prompt string
	HTTP                    *http.Client
	Calibrated              bool   // set by NewChatJudge from the calibration records
	CalibrationNote         string // why it is or is not calibrated
}

// JudgeTemplate is the template name a live judge's prompt is hashed
// under, as a calibration record's templates name theirs.
const JudgeTemplate = "live_session"

// NewChatJudge builds the judge of a config. calibrationDir is the judged
// suite folder (judge.calibration, resolved); a record there covering the
// model and this prompt's hash marks the judge calibrated.
func NewChatJudge(j *JudgeConfig, key, prompt, calibrationDir string, hc *http.Client) *ChatJudge {
	if prompt == "" {
		prompt = DefaultJudgePrompt
	}
	cj := &ChatJudge{URL: strings.TrimRight(j.URL, "/"), Key: key, Model: j.Model, Prompt: prompt, HTTP: hc,
		CalibrationNote: "no calibration record covers this judge and prompt; its scores are reported and never decide"}
	if hc == nil {
		cj.HTTP = &http.Client{Timeout: 60 * time.Second}
	}
	if calibrationDir != "" {
		if set, err := calibration.ReadRecords(calibrationDir); err == nil && set.Verified {
			sum := sha256.Sum256([]byte(prompt))
			ph := calibration.PromptHash(map[string]string{JudgeTemplate: hex.EncodeToString(sum[:])})
			if rec := set.ForPrompt(j.Model, ph); rec != nil {
				cj.Calibrated = true
				cj.CalibrationNote = "calibration record " + rec.RecordID + " covers this judge and prompt"
			}
		}
	}
	return cj
}

// scorePattern captures the whole token after "SCORE:" so a prefix of an
// invalid number (for example "10" or "1e-2") is never accepted as a score.
var scorePattern = regexp.MustCompile(`(?i)score\s*[:=]\s*([^\s,;)]+)`)

// scoreToken is the only accepted number shape: a plain decimal in [0, 1]
// without sign or exponent.
var scoreToken = regexp.MustCompile(`^(?:0(?:\.\d+)?|1(?:\.0+)?|\.\d+)$`)

// parseScore reads the judge's score from its answer text.
func parseScore(content string) (float64, error) {
	m := scorePattern.FindStringSubmatch(content)
	if m == nil {
		return 0, errors.New("judge answer holds no SCORE line")
	}
	tok := strings.TrimSuffix(m[1], ".")
	if !scoreToken.MatchString(tok) {
		return 0, errors.New("judge score is not between 0 and 1")
	}
	v, err := strconv.ParseFloat(tok, 64)
	if err != nil || v < 0 || v > 1 {
		return 0, errors.New("judge score is not between 0 and 1")
	}
	return v, nil
}

// Score implements Judge. The transcript sent is the redacted one.
func (j *ChatJudge) Score(ctx context.Context, s *traceimport.Session) (float64, bool, error) {
	text := strings.ReplaceAll(j.Prompt, "{{transcript}}", renderTranscript(s))
	body, _ := json.Marshal(map[string]any{"model": j.Model, "temperature": 0,
		"messages": []map[string]string{{"role": "user", "content": text}}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.URL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+j.Key)
	resp, err := j.HTTP.Do(req)
	if err != nil {
		return 0, false, errors.New("judge request failed")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return 0, false, fmt.Errorf("judge answered status %d", resp.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		return 0, false, errors.New("judge answer did not parse")
	}
	v, err := parseScore(out.Choices[0].Message.Content)
	if err != nil {
		return 0, false, err
	}
	return v, j.Calibrated, nil
}

// renderTranscript is a plain-text transcript of a redacted session, capped.
func renderTranscript(s *traceimport.Session) string {
	var b strings.Builder
	for _, t := range s.Turns {
		if t.Role == "system" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", strings.ToUpper(t.Role), oneLine(t.Text, 2000))
		if b.Len() > 24<<10 {
			b.WriteString("[transcript cut]\n")
			break
		}
	}
	for _, c := range s.ToolCalls {
		status := "ok"
		if c.IsError {
			status = "error"
		}
		fmt.Fprintf(&b, "TOOL %s (%s)\n", c.Name, status)
	}
	return b.String()
}
