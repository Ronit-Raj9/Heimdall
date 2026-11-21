package graders

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/visual"
)

// X1 vision grading: the visual_judge grade source. The pinned vision model
// is asked, through the gateway on its own virtual key (never the run's),
// whether the captured image meets the task's rubric. It is labelled
// judged: the grade carries a JudgeRecord like the text judge's, so W8's
// judge_calibration instrument keys it by model and template hash and caps
// a verdict that rests on it at INCONCLUSIVE until a calibration record
// covers it.

// PromptVisualV1 is the shipped vision rubric prompt.
const PromptVisualV1 = visual.PromptVisualV1

// Vision judge verdicts (VisualJudgeResult.Verdict and Votes).
const (
	visualVerdictPass      = "pass"
	visualVerdictFail      = "fail"
	visualVerdictUndecided = "undecided"
	visualVoteUnparsable   = "unparsable"
)

const (
	defaultVisualTokens = 300
	maxVisualRubricRune = 8000
	maxRationaleRunes   = 600
	visualKeyLetter     = "v"
	visualMeanDefault   = 0.5
	visualAggMajority   = "majority"
	visualAggMean       = "mean"
)

//go:embed visualprompts/*.txt
var visualPromptFS embed.FS

// VisualPrompt returns the template of a shipped vision prompt version and
// the sha256 of its text, the hash a judged visual grade records as
// prompt_sha256 and a calibration record must cover.
func VisualPrompt(version string) (string, string, error) {
	if version != PromptVisualV1 {
		return "", "", fmt.Errorf("vision prompt_version %q is not shipped (want %s)", version, PromptVisualV1)
	}
	b, err := visualPromptFS.ReadFile("visualprompts/" + version + ".txt")
	if err != nil {
		return "", "", err
	}
	return string(b), utils.SHA256Bytes(b), nil
}

// CheckVisualJudge validates a vision judge spec, for the grader and lint
// (visual.CheckJudge).
func CheckVisualJudge(j *schemas.VisualJudgeSpec) error { return visual.CheckJudge(j) }

// visionMessage is a chat message whose content is a list of parts, the
// OpenAI-compatible form Bifrost accepts for images.
type visionMessage struct {
	Role    string       `json:"role"`
	Content []visionPart `json:"content"`
}

type visionPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type visionRequest struct {
	Model       string          `json:"model"`
	Messages    []visionMessage `json:"messages"`
	Temperature float64         `json:"temperature"`
	MaxTokens   int             `json:"max_tokens"`
}

// judge asks the vision model for every vote and returns the report and the
// JudgeRecord the calibration instrument reads.
func (g *visualGrader) judge(ctx context.Context, rc *schemas.RunContext, spec *schemas.VisualCheck, capture, ref *visual.Image) (*schemas.VisualJudgeResult, *schemas.JudgeRecord, error) {
	j := spec.Judge
	if err := CheckVisualJudge(j); err != nil {
		return nil, nil, err
	}
	if gr := utils.Grading(rc.Suite); gr != schemas.GradingJudged {
		return nil, nil, fmt.Errorf("suite %s has grading %s; a rubric lets the vision judge decide outcomes, so the suite must declare grading: judged", rc.Suite.ID, gr)
	}
	if rc.NoPaidTraffic {
		return nil, nil, errors.New(NoPaidJudgeReason)
	}
	jc := g.cfg.Judge
	if jc.Gateway == nil || jc.URL == "" {
		return nil, nil, errors.New("vision judge is not wired to the gateway (register graders.NewVisual with the judge's gateway and URL)")
	}
	if !judgeKeyNameRe.MatchString(rc.Run.RunID) {
		return nil, nil, errors.New("run context has no usable run id for the judge's key name")
	}
	rubricPath, err := visual.TaskPath(rc.Task.Dir, spec.Rubric)
	if err != nil {
		return nil, nil, err
	}
	rubric, err := readCapped(rubricPath)
	if err != nil {
		return nil, nil, fmt.Errorf("rubric: %w", err)
	}
	if r := []rune(string(rubric)); len(r) > maxVisualRubricRune {
		return nil, nil, fmt.Errorf("rubric is %d characters, over %d", len(r), maxVisualRubricRune)
	}
	tmpl, promptSHA, err := VisualPrompt(j.PromptVersion)
	if err != nil {
		return nil, nil, err
	}
	msg, err := visionPrompt(tmpl, string(rubric), capture, ref)
	if err != nil {
		return nil, nil, err
	}
	res := &schemas.VisualJudgeResult{Model: j.Model, PromptVersion: j.PromptVersion, PromptSHA256: promptSHA,
		RubricSHA256: utils.SHA256Bytes(rubric), Aggregate: aggregateOf(j), PriceRef: jc.PriceRef}
	keyRC := &schemas.RunContext{
		Run:   &schemas.Run{RunID: rc.Run.RunID + visualKeyLetter + strings.TrimPrefix(utils.NewID("j", judgeKeySuffixLen), "j_"), ExpID: rc.Run.ExpID},
		Agent: &schemas.AgentConfig{ID: VisualType + "_judge", Model: j.Model, Gateway: schemas.GatewayPolicy{Provider: j.Provider}},
	}
	key, err := jc.Gateway.MintKey(ctx, keyRC)
	if err != nil {
		return nil, nil, fmt.Errorf("mint the vision judge's key: %w", err)
	}
	if rc.Key != nil && (key.Value == rc.Key.Value || key.ID == rc.Key.ID) {
		return nil, nil, errors.New("gateway returned the run's own key for the vision judge")
	}
	defer (&judge{cfg: jc}).revoke(ctx, key)
	ctx = utils.WithMaximHeaders(ctx, utils.MaximHeaders(rc, utils.MaximRoleVisualJudge))
	votes := max(j.Votes, 1)
	var usd float64
	priced, unpriced := true, ""
	var rationales []string
	for i := 0; i < votes; i++ {
		reply, tok, served, err := g.visionCall(ctx, key, j, msg)
		if err != nil {
			return nil, nil, fmt.Errorf("vision judge vote %d of %d: %w", i+1, votes, err)
		}
		if res.ServedModel == "" {
			res.ServedModel = served
		}
		v, score, why := ParseVisualReply(reply)
		res.Votes = append(res.Votes, v)
		if v != visualVoteUnparsable {
			res.Scores = append(res.Scores, score)
			if why != "" {
				rationales = append(rationales, why)
			}
		}
		switch {
		case tok == nil:
			priced, unpriced = false, "a vision judge response carried no usage"
		case jc.Price == nil:
			priced, unpriced = false, "no price snapshot wired to the judge"
		default:
			c, ok := jc.Price(j.Model, *tok)
			if !ok {
				priced, unpriced = false, fmt.Sprintf("vision judge model %s is not in the price snapshot", j.Model)
			}
			usd += c
		}
	}
	res.USD = schemas.Observed(usd, schemas.EvidenceGateway)
	if !priced {
		res.USD = schemas.Unknown(unpriced, schemas.EvidenceGateway)
	}
	if len(rationales) > 0 {
		res.Rationale = rationales[0]
	}
	res.Verdict, res.Score = tallyVisual(res.Votes, res.Scores, res.Aggregate, j.PassScore)
	id := visualJudgeID(j)
	g.calibrate(rc, res, id)
	rec := &schemas.JudgeRecord{Model: j.Model, PromptVersion: j.PromptVersion, PromptSHA256: promptSHA, Votes: textVotes(res.Votes),
		USD: res.USD, PriceRef: res.PriceRef, ServedModel: res.ServedModel, Calibration: res.Calibration,
		Provider: id.Provider, Aggregate: id.Aggregate, PassScore: id.PassScore}
	return res, rec, nil
}

// calibrate looks up the W8 calibration record for the judge's model that
// covers the vision template, and keeps its agreement numbers beside the
// grade. Without one the judge reports and the verdict instrument caps it.
//
// The record must have measured this judge configuration: provider, votes
// and, for aggregate mean, the pass score (QC2-25-03, QC2-25-05).
func (g *visualGrader) calibrate(rc *schemas.RunContext, res *schemas.VisualJudgeResult, id calibration.JudgeID) {
	recs, err := calibration.ReadRecords(rc.Suite.Root)
	if err != nil || rc.Suite.Root == "" {
		res.Reason = fmt.Sprintf("no calibration record for %s and %s (%s)", res.Model, res.PromptVersion, short(res.PromptSHA256))
		return
	}
	rec, _ := recs.Covering(id, []string{res.PromptSHA256})
	if rec == nil {
		res.Reason = fmt.Sprintf("no calibration record for %s and %s (%s); judge_calibration caps the verdict at INCONCLUSIVE", res.Model, res.PromptVersion, short(res.PromptSHA256))
		return
	}
	res.Calibration = calibration.Ref(rec)
	res.Decides = true
	stats := rec.Human
	res.Agreement = &stats
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// visionPrompt builds the user message: the filled template, the capture
// and, when there is one, the reference.
func visionPrompt(tmpl, rubric string, capture, ref *visual.Image) (visionMessage, error) {
	note := "There is no reference image."
	if ref != nil {
		note = "The second image is the reference: what correct work looks like."
	}
	defence := strings.NewReplacer("<<<", "< < <", ">>>", "> > >")
	text := strings.NewReplacer("{{reference_note}}", note, "{{rubric}}", defence.Replace(strings.TrimSpace(rubric))).Replace(tmpl)
	msg := visionMessage{Role: "user", Content: []visionPart{{Type: "text", Text: text}}}
	for _, img := range []*visual.Image{capture, ref} {
		if img == nil {
			continue
		}
		data, err := img.EncodePNG()
		if err != nil {
			return msg, err
		}
		msg.Content = append(msg.Content, visionPart{Type: "image_url", ImageURL: &imageURL{URL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)}})
	}
	return msg, nil
}

// visionCall sends one vote; errors are as the text judge's call.
func (g *visualGrader) visionCall(ctx context.Context, key *schemas.RunKey, j *schemas.VisualJudgeSpec, msg visionMessage) (string, *schemas.Tokens, string, error) {
	maxTokens := j.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultVisualTokens
	}
	body, err := json.Marshal(visionRequest{Model: j.Provider + "/" + j.Model, Messages: []visionMessage{msg}, Temperature: 0, MaxTokens: maxTokens})
	if err != nil {
		return "", nil, "", fmt.Errorf("encode request: %w", err)
	}
	jc := g.cfg.Judge
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(jc.URL, "/")+chatCompletionsPath, bytes.NewReader(body))
	if err != nil {
		return "", nil, "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(virtualKeyHeader, key.Value)
	for _, h := range utils.MaximHeadersFrom(ctx) { // X23: tagged as Heimdall's own call for Bifrost's Maxim plugin
		req.Header.Set(h[0], h[1])
	}
	client := jc.HTTP
	if client == nil {
		client = &http.Client{Timeout: judgeHTTPTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, "", fmt.Errorf("call gateway: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxJudgeResponseBody))
	if err != nil {
		return "", nil, "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return "", nil, "", fmt.Errorf("gateway answered %d: %s", resp.StatusCode, quote(string(raw)))
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", nil, "", fmt.Errorf("decode response: %w", err)
	}
	if !sameModel(out.Model, j.Model) {
		return "", nil, "", fmt.Errorf("gateway served model %s, the vision judge is pinned to %s", out.Model, j.Model)
	}
	reply := ""
	if len(out.Choices) > 0 {
		reply = contentText(out.Choices[0].Message.Content)
	}
	var tok *schemas.Tokens
	if u := out.Usage; u != nil {
		cached := int64(0)
		if u.PromptTokensDetails != nil {
			cached = u.PromptTokensDetails.CachedTokens
		}
		tok = &schemas.Tokens{Fresh: max(u.PromptTokens-cached, 0), CacheRead: cached, Out: u.CompletionTokens}
	}
	return reply, tok, out.Model, nil
}

// ParseVisualReply reads a vision judge reply strictly: one JSON object
// (optionally in a ```json fence) with verdict pass or fail, a score from 0
// to 1 and an optional rationale, and no other keys. Anything else is
// unparsable.
func ParseVisualReply(reply string) (vote string, score float64, rationale string) {
	s := strings.TrimSpace(reply)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(strings.TrimPrefix(s, "```json"), "```")
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
	}
	var doc struct {
		Verdict   *string  `json:"verdict"`
		Score     *float64 `json:"score"`
		Rationale string   `json:"rationale"`
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil || dec.More() {
		return visualVoteUnparsable, 0, ""
	}
	if doc.Verdict == nil || doc.Score == nil || math.IsNaN(*doc.Score) || *doc.Score < 0 || *doc.Score > 1 {
		return visualVoteUnparsable, 0, ""
	}
	v := strings.ToLower(strings.TrimSpace(*doc.Verdict))
	if v != visualVerdictPass && v != visualVerdictFail {
		return visualVoteUnparsable, 0, ""
	}
	why := strings.Join(strings.Fields(doc.Rationale), " ")
	if r := []rune(why); len(r) > maxRationaleRunes {
		why = string(r[:maxRationaleRunes]) + "..."
	}
	return v, *doc.Score, why
}

// tallyVisual aggregates the votes: majority passes or fails on more than
// half of the votes and is undecided otherwise; mean passes when the mean
// score of the parsed votes reaches passScore (default 0.5) and is
// undecided when no vote parsed.
func tallyVisual(votes []string, scores []float64, agg string, passScore float64) (string, *float64) {
	var mean *float64
	if len(scores) > 0 {
		s := 0.0
		for _, v := range scores {
			s += v
		}
		m := math.Round(s/float64(len(scores))*1e6) / 1e6
		mean = &m
	}
	if agg == visualAggMean {
		if passScore == 0 {
			passScore = visualMeanDefault
		}
		switch {
		case mean == nil:
			return visualVerdictUndecided, nil
		case *mean >= passScore:
			return visualVerdictPass, mean
		default:
			return visualVerdictFail, mean
		}
	}
	n := map[string]int{}
	for _, v := range votes {
		n[v]++
	}
	switch {
	case 2*n[visualVerdictPass] > len(votes):
		return visualVerdictPass, mean
	case 2*n[visualVerdictFail] > len(votes):
		return visualVerdictFail, mean
	}
	return visualVerdictUndecided, mean
}

// textVotes maps vision votes to the text judge's vote words, so the
// calibration instrument and the review queue read them unchanged.
func textVotes(votes []string) []string {
	out := make([]string, len(votes))
	for i, v := range votes {
		switch v {
		case visualVerdictPass:
			out[i] = VoteCorrect
		case visualVerdictFail:
			out[i] = VoteIncorrect
		default:
			out[i] = VoteUnparsable
		}
	}
	return out
}

// visualJudgeID is the calibration identity of a vision judge spec. Only
// aggregate mean is recorded (majority is the default) and only then the
// pass score, defaulted.
func visualJudgeID(j *schemas.VisualJudgeSpec) calibration.JudgeID {
	id := calibration.JudgeID{Provider: j.Provider, Model: j.Model, Votes: max(j.Votes, 1)}
	if aggregateOf(j) == visualAggMean {
		id.Aggregate, id.PassScore = visualAggMean, j.PassScore
		if id.PassScore == 0 {
			id.PassScore = visualMeanDefault
		}
	}
	return id
}

func aggregateOf(j *schemas.VisualJudgeSpec) string {
	if j.Aggregate == "" {
		return visualAggMajority
	}
	return j.Aggregate
}
