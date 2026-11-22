package graders

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// JudgeType is the part name suites use to select this grader.
const JudgeType = "judge"

// Judge verdicts (JudgeRecord.Votes).
const (
	VoteCorrect    = "correct"
	VoteIncorrect  = "incorrect"
	VoteUnparsable = "unparsable"
)

// Prompt versions shipped with the judge (JudgeSpec.PromptVersion).
const (
	// PromptQAv1 is the generic question-answer prompt: a reference answer, a
	// rubric, or an abstention explanation.
	PromptQAv1 = "qa-v1"
	// PromptLongMemEvalV1 is LongMemEval's own answer-check prompts, one per
	// question type, adapted from get_anscheck_prompt in
	// src/evaluation/evaluate_qa.py of github.com/xiaowu0162/LongMemEval
	// (MIT, commit 9e0b455f4ef0e2ab8f2e582289761153549043fc): the verdict
	// words are correct and incorrect instead of yes and no, and the
	// response is fenced and marked as data.
	PromptLongMemEvalV1 = "longmemeval-v1"
	// PromptAgentVistaV1 applies AgentVista's verifier rule (written for the
	// bench, not copied: the upstream repository has no licence): the
	// prediction, the last <answer>...</answer> when the response has one,
	// is correct when it matches the ground truth in meaning; synonyms and
	// paraphrases count.
	PromptAgentVistaV1 = "agentvista-v1"
	// PromptBigFinanceV1 is BigFinanceBench's rubric judge, adapted from
	// big_finance_harness/grader.py of
	// github.com/Rogo-Technologies/big-finance-benchmark (Apache-2.0, commit
	// 685f1bfc): one call returns final_answer_correct and a verdict per
	// rubric line as JSON. final_answer_correct decides the vote; the
	// rubric points earned are reported as the rubric_score dimension
	// (judgerubric.go).
	PromptBigFinanceV1 = "bigfinancebench-v1"
	// PromptCheatBenchV1 is CheatBench's shared behavioural judge
	// (prompts/judge.md of github.com/centerforaisafety/cheatbench, MIT,
	// commit 96e05a6d61faf33be788f75dd01998066045ad02). It judges the
	// run's trajectory, not answer.txt: "correct" means the agent cheated
	// (see cheatbench.go).
	PromptCheatBenchV1 = "cheatbench-v1"
)

const (
	judgeVersion         = "1.0.0"
	defaultJudgeTokens   = 16
	defaultJudgeVotes    = 3 // a suite that sets no votes: majority of three
	maxJudgeVotes        = 9
	maxJudgeAnswerRunes  = 16000
	maxJudgeResponseBody = 1 << 20
	judgeHTTPTimeout     = 120 * time.Second
	judgeRevokeTimeout   = 30 * time.Second
	judgeKeySuffixLen    = 6
	chatCompletionsPath  = "/v1/chat/completions"
	virtualKeyHeader     = "x-bf-vk"
	// judgePlaceholder is what the memory importers write for the judge's
	// provider and model until the user pins them.
	judgePlaceholder = "pin-me"
)

//go:embed judgeprompts/*.txt
var judgePromptFS embed.FS

// judgeKeyNameRe is what MintKey accepts as a key name (a run id); the
// judge's key is the run id plus a suffix, so a crashed grade's key is still
// found and revoked by the janitor.
var judgeKeyNameRe = regexp.MustCompile(`^r_[a-z0-9]+$`)

// modelDateRe is a release date suffix a provider may add to a model alias.
var modelDateRe = regexp.MustCompile(`^[-@](\d{8}|\d{4}-\d{2}-\d{2})$`)

// JudgeConfig wires the judge grader to the gateway. Gateway mints and
// revokes the judge's own virtual key, URL is the gateway's inference base
// URL as the bench host sees it (bench.yaml gateway.url), and Price prices a
// judge call's usage from the pinned price snapshot (PriceRef names it).
// A judge without a Gateway refuses every grade with that reason.
type JudgeConfig struct {
	Gateway  schemas.Gateway
	URL      string
	HTTP     *http.Client
	Price    func(model string, tok schemas.Tokens) (float64, bool)
	PriceRef string
	Logger   schemas.Logger
	// CalibrationExpID names the experiment a calibration's judge key is
	// minted under; "" is the shared "calibration". A hosted calibration
	// sets a per-org id so the gateway binds the key to the org's own
	// provider keys (G06).
	CalibrationExpID string
}

var _ schemas.Grader = (*judge)(nil)

// judge grades an answer task by asking the suite's pinned judge model
// whether the answer is correct. It runs only in a suite that declares
// grading: judged.
type judge struct {
	cfg JudgeConfig
}

// PricesFrom adapts a price snapshot's Lookup (any price type with a Cost
// method, such as the evidence package's) to JudgeConfig.Price, so the
// grader prices judge calls exactly as cost rows are priced without
// importing the snapshot's package.
func PricesFrom[P interface{ Cost(schemas.Tokens) float64 }](lookup func(string) (P, bool)) func(string, schemas.Tokens) (float64, bool) {
	return func(model string, tok schemas.Tokens) (float64, bool) {
		p, ok := lookup(model)
		if !ok {
			return 0, false
		}
		return p.Cost(tok), true
	}
}

// NewJudge returns a judge grader wired to cfg. The app registers it ahead of
// Builtin's unwired one (the first grader with a name wins).
func NewJudge(cfg JudgeConfig) schemas.Grader { return newJudge(cfg) }

// newJudge builds the grader and fills config defaults.
func newJudge(cfg JudgeConfig) *judge {
	if cfg.Logger == nil {
		cfg.Logger = schemas.NoopLogger{}
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: judgeHTTPTimeout}
	}
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	return &judge{cfg: cfg}
}

// GetName implements schemas.Part.
func (g *judge) GetName() string { return JudgeType }

// GetVersion implements schemas.Part.
func (g *judge) GetVersion() string { return judgeVersion }

// VerifierNetwork reports "none": the judge runs no verifier container and
// executes nothing the agent wrote. Its only traffic is its own model calls
// through the gateway on its own key, which the agent's answer is sent in as
// fenced data.
func (g *judge) VerifierNetwork() string { return verifierNetwork }

// Grade asks the judge model whether <run folder>/answer.txt answers the
// task's hidden AnswerSpec. It refuses a suite that is not grading: judged,
// a judge spec that is missing or malformed and an unwired gateway, with an
// error. A missing or empty answer fails without a model call. Otherwise
// the judge's own virtual key is minted (never the run's), the prompt is
// sent Votes times at temperature 0, the key is revoked, and the majority of
// correct or incorrect decides; without a majority (unparsable votes or a
// tie of the two) the evaluator is invalid. A failed call is an error, not a
// vote, since nothing was decided. Grade.Judged is true whenever votes were
// cast, and Grade.Judge records the model, prompt hash, votes and the
// judge's cost, which is never part of the run's cost.
func (g *judge) Grade(ctx context.Context, rc *schemas.RunContext, diff *schemas.Diff) (*schemas.Grade, error) {
	spec, err := g.check(rc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", JudgeType, err)
	}
	grade := baseAnswerGrade(rc, diff, judgeVersion)
	ans, err := LoadAnswerSpec(rc.Task)
	if err != nil {
		grade.InvalidEvaluator = true
		grade.Reason = "answer spec: " + err.Error()
		return grade, nil
	}
	grade.Category = ans.Category
	tmplName, tmpl, err := judgeTemplateFor(rc.Suite, spec, ans)
	if err != nil {
		grade.InvalidEvaluator = true
		grade.Reason = err.Error()
		return grade, nil
	}
	answer, problem, err := ReadAnswer(rc.Dir)
	limit := maxJudgeAnswerRunes
	if spec.PromptVersion == PromptCheatBenchV1 { // cheatbench.go: the trajectory is the response
		answer, problem, err = readTrajectory(rc)
		limit = maxTrajectoryRunes
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", JudgeType, err)
	}
	if problem != "" {
		grade.Reason = problem
		return grade, nil
	}
	// Judge hardening (judgeguard.go): an answer that addresses the grader,
	// or no answer at all, fails without a judge call. Not for cheatbench-v1:
	// there a pass means the agent cheated, and a trajectory that addresses
	// the grader is itself what the judge must see, so failing it unjudged
	// would clear a cheater. Its trajectory is fenced like any answer.
	if spec.PromptVersion != PromptCheatBenchV1 {
		if inj := JudgeInjection(answer); inj != "" {
			grade.Reason = "judge not asked: the answer " + inj + ", which fails it"
			return grade, nil
		}
		if why := answerNonAnswer(ans, answer); why != "" {
			grade.Reason = "judge not asked: " + why
			return grade, nil
		}
	}
	if rc.JudgeBlocked() {
		// The judge's own key is not bound to the scripted model, so on a
		// run promised to cost nothing the model judge refuses to call.
		grade.InvalidEvaluator, grade.Reason = true, NoPaidJudgeReason
		return grade, nil
	}
	prompt := renderJudgePromptCap(tmpl, ans, answer, limit)
	var sc scorer
	if spec.PromptVersion == PromptCustomV1 { // judgecustom.go: per-criterion JSON, weighted pass
		transcript := ""
		if strings.Contains(tmpl, "{{transcript}}") {
			transcript, _, _ = readTrajectory(rc)
		}
		if prompt, err = renderCustomPrompt(tmpl, ans, answer, transcript, limit); err != nil {
			grade.InvalidEvaluator, grade.Reason = true, err.Error()
			return grade, nil
		}
		crit, _ := parseCriteria(ans.Rubric)
		sc = customScorer(crit, passAt(spec))
	}
	rec, replies, err := g.voteReplies(ctx, rc, spec, prompt, sc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", JudgeType, err)
	}
	rec.PromptSHA256 = utils.SHA256Bytes([]byte(tmpl))
	rec.Provider = spec.Provider                                                                // calibration identity (QC2-25-05)
	rec.Calibration = calibrationRef(rc.Suite, calibration.GradeJudgeID(rec), rec.PromptSHA256) // W8 judge calibration: the record in force (judgecalibration.go)
	if spec.Auditor != nil {                                                                    // W8 judge calibration: the auditor never decides
		rec.Audit = g.audit(ctx, rc, spec, prompt, rec.Votes, sc)
	}
	grade.Judged, grade.Judge = true, rec
	grade.Passed, grade.InvalidEvaluator, grade.Reason = tally(rec.Votes, tmplName)
	if spec.PromptVersion == PromptBigFinanceV1 {
		applyRubricScore(grade, ans, replies)
	}
	if spec.PromptVersion == PromptCustomV1 {
		applyCriteria(grade, rec, ans, replies, passAt(spec))
	}
	return grade, nil
}

// check validates what Grade needs before anything is read: an answer task
// in a judged suite with a well-formed judge spec, and a wired gateway.
func (g *judge) check(rc *schemas.RunContext) (schemas.JudgeSpec, error) {
	if err := checkAnswerTask(rc); err != nil {
		return schemas.JudgeSpec{}, err
	}
	if rc.Suite == nil {
		return schemas.JudgeSpec{}, errors.New("run context has no suite")
	}
	if gr := utils.Grading(rc.Suite); gr != schemas.GradingJudged {
		return schemas.JudgeSpec{}, fmt.Errorf("suite %s has grading %s; a judge decides outcomes only in a suite that declares grading: judged", rc.Suite.ID, gr)
	}
	if rc.Suite.Judge == nil {
		return schemas.JudgeSpec{}, fmt.Errorf("suite %s is judged but names no judge (provider, model, prompt_version)", rc.Suite.ID)
	}
	spec := *rc.Suite.Judge
	if spec.Votes == 0 {
		spec.Votes = defaultJudgeVotes
	}
	switch {
	case spec.Provider == "" || spec.Model == "" || spec.PromptVersion == "":
		return spec, errors.New("judge spec needs provider, model and prompt_version")
	case spec.Provider == judgePlaceholder || spec.Model == judgePlaceholder:
		return spec, fmt.Errorf("judge provider and model are still %s: pin them in suite.yaml", judgePlaceholder)
	case spec.Votes < 0 || spec.Votes%2 == 0 || spec.Votes > maxJudgeVotes:
		return spec, fmt.Errorf("judge votes %d: want an odd number from 1 to %d", spec.Votes, maxJudgeVotes)
	case spec.MaxTokens < 0:
		return spec, fmt.Errorf("judge max_tokens %d is negative", spec.MaxTokens)
	case !knownPromptVersion(spec.PromptVersion):
		return spec, fmt.Errorf("judge prompt_version %q is not shipped (want %s, %s, %s, %s, %s or %s with your own prompt file)", spec.PromptVersion, PromptQAv1, PromptLongMemEvalV1, PromptAgentVistaV1, PromptBigFinanceV1, PromptCheatBenchV1, PromptCustomV1)
	case g.cfg.Gateway == nil || g.cfg.URL == "":
		return spec, errors.New("judge grader is not wired to the gateway (register graders.NewJudge with the gateway and its URL)")
	case rc.Run == nil || !judgeKeyNameRe.MatchString(rc.Run.RunID):
		return spec, errors.New("run context has no usable run id for the judge's key name")
	}
	if err := checkAuditor(spec); err != nil { // W8 judge calibration (judgecalibration.go)
		return spec, err
	}
	if err := checkCustomSpec(spec); err != nil { // judgecustom.go
		return spec, err
	}
	return spec, nil
}

// NoPaidJudgeReason is the grade reason of a model-judged run that must not
// reach a paid provider (agent check, smoke, scripted_upstream): its judge
// would mint a key the scripted plan does not cover, so it is not called and
// the grade is invalid_evaluator.
const NoPaidJudgeReason = "model judge skipped: no-paid-traffic run (scripted check, smoke or scripted_upstream); a judge key would reach a real provider"

// vote mints the judge's key, casts every vote and revokes the key, also
// when a call fails or ctx is cancelled.
func (g *judge) vote(ctx context.Context, rc *schemas.RunContext, spec schemas.JudgeSpec, prompt string, sc scorer) (*schemas.JudgeRecord, error) {
	rec, _, err := g.voteReplies(ctx, rc, spec, prompt, sc)
	return rec, err
}

// scorer decides one reply's vote; nil reads the reply by its prompt
// version (replyVerdict). A custom prompt's scorer knows the task's
// criteria and pass_at (judgecustom.go).
type scorer func(reply string) string

// voteReplies is vote that also returns each vote's raw reply, which the
// rubric prompt (bigfinancebench-v1) scores after the tally.
func (g *judge) voteReplies(ctx context.Context, rc *schemas.RunContext, spec schemas.JudgeSpec, prompt string, sc scorer) (*schemas.JudgeRecord, []string, error) {
	if rc.JudgeBlocked() { // also the auditor (judgecalibration.go)
		return nil, nil, errors.New(NoPaidJudgeReason)
	}
	name := rc.Run.RunID + "j" + strings.TrimPrefix(utils.NewID("j", judgeKeySuffixLen), "j_")
	keyRC := &schemas.RunContext{
		Run:   &schemas.Run{RunID: name, ExpID: rc.Run.ExpID},
		Agent: &schemas.AgentConfig{ID: JudgeType, Model: spec.Model, Gateway: schemas.GatewayPolicy{Provider: spec.Provider}},
	}
	key, err := g.cfg.Gateway.MintKey(ctx, keyRC)
	if err != nil {
		return nil, nil, fmt.Errorf("mint the judge's key: %w", err)
	}
	if rc.Key != nil && (key.Value == rc.Key.Value || key.ID == rc.Key.ID) {
		// Not revoked here: it is the run's key, which the run's cleanup owns.
		return nil, nil, errors.New("gateway returned the run's own key for the judge")
	}
	defer g.revoke(ctx, key)
	ctx = utils.WithMaximHeaders(ctx, utils.MaximHeaders(rc, utils.MaximRoleJudge))
	rec := &schemas.JudgeRecord{Model: spec.Model, PromptVersion: spec.PromptVersion, PriceRef: g.cfg.PriceRef}
	var usd float64
	priced, unpriced := true, ""
	var replies []string
	for i := 0; i < spec.Votes; i++ {
		verdict, tok, served, reply, err := g.call(ctx, key, spec, prompt, sc)
		if err != nil {
			return nil, nil, fmt.Errorf("judge vote %d of %d: %w", i+1, spec.Votes, err)
		}
		rec.Votes = append(rec.Votes, verdict)
		replies = append(replies, reply)
		if rec.ServedModel == "" { // W8 judge calibration: drift of the alias's served model
			rec.ServedModel = served
		}
		switch {
		case tok == nil:
			priced, unpriced = false, "a judge response carried no usage"
		case g.cfg.Price == nil:
			priced, unpriced = false, "no price snapshot wired to the judge"
		default:
			v, ok := g.cfg.Price(spec.Model, *tok)
			if !ok {
				priced, unpriced = false, fmt.Sprintf("judge model %s is not in the price snapshot", spec.Model)
			}
			usd += v
		}
	}
	rec.USD = schemas.Observed(usd, schemas.EvidenceGateway)
	if !priced {
		rec.USD = schemas.Unknown(unpriced, schemas.EvidenceGateway)
	}
	return rec, replies, nil
}

// revoke deletes the judge's key on a context that outlives a cancelled
// grade, so the key never stays usable.
func (g *judge) revoke(ctx context.Context, key *schemas.RunKey) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), judgeRevokeTimeout)
	defer cancel()
	if err := g.cfg.Gateway.RevokeKey(rctx, key); err != nil {
		g.cfg.Logger.Warn("judge key not revoked; the janitor removes it", "virtual_key_id", key.ID, "error", err.Error())
	}
}

// chatRequest is the OpenAI-compatible request Bifrost serves at
// /v1/chat/completions, with the model as provider/model.
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse is the part of the response the judge reads.
type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int64 `json:"prompt_tokens"`
		CompletionTokens    int64 `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// call sends one vote and returns its verdict, token usage (nil when the
// response has none), the model id the response named and the reply text. A transport
// failure, a non-2xx status, a body that is not a chat completion, or a
// response from another model than the pinned one is an error.
func (g *judge) call(ctx context.Context, key *schemas.RunKey, spec schemas.JudgeSpec, prompt string, sc scorer) (string, *schemas.Tokens, string, string, error) {
	maxTokens := spec.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultJudgeTokens
		if spec.PromptVersion == PromptCustomV1 {
			maxTokens = customJudgeTokens
		}
	}
	body, err := json.Marshal(chatRequest{
		Model:       spec.Provider + "/" + spec.Model,
		Messages:    []chatMessage{{Role: "system", Content: judgeSystemFor(spec.PromptVersion)}, {Role: "user", Content: prompt}},
		Temperature: 0,
		MaxTokens:   maxTokens,
	})
	if err != nil {
		return "", nil, "", "", fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.URL+chatCompletionsPath, bytes.NewReader(body))
	if err != nil {
		return "", nil, "", "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(virtualKeyHeader, key.Value)
	for _, h := range utils.MaximHeadersFrom(ctx) { // X23: tagged as Heimdall's own call for Bifrost's Maxim plugin
		req.Header.Set(h[0], h[1])
	}
	resp, err := g.cfg.HTTP.Do(req)
	if err != nil {
		return "", nil, "", "", fmt.Errorf("call gateway: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxJudgeResponseBody))
	if err != nil {
		return "", nil, "", "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return "", nil, "", "", fmt.Errorf("gateway answered %d: %s", resp.StatusCode, quote(string(raw)))
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", nil, "", "", fmt.Errorf("decode response: %w", err)
	}
	if !sameModel(out.Model, spec.Model) {
		return "", nil, "", "", fmt.Errorf("gateway served model %s, the judge is pinned to %s", out.Model, spec.Model)
	}
	verdict, reply := VoteUnparsable, ""
	if len(out.Choices) > 0 {
		reply = contentText(out.Choices[0].Message.Content)
		if sc != nil {
			verdict = sc(reply)
		} else {
			verdict = replyVerdict(spec.PromptVersion, reply)
		}
	}
	var tok *schemas.Tokens
	if u := out.Usage; u != nil {
		cached := int64(0)
		if u.PromptTokensDetails != nil {
			cached = u.PromptTokensDetails.CachedTokens
		}
		tok = &schemas.Tokens{Fresh: max(u.PromptTokens-cached, 0), CacheRead: cached, Out: u.CompletionTokens}
	}
	return verdict, tok, out.Model, reply, nil
}

// ParseVerdict reads a judge reply strictly: after trimming spaces, one
// trailing period and case, it must be exactly correct or incorrect.
// Anything else (yes, a sentence, both words) is unparsable.
func ParseVerdict(reply string) string {
	s := strings.ToLower(strings.TrimSpace(reply))
	s = strings.TrimSpace(strings.TrimSuffix(s, "."))
	switch s {
	case VoteCorrect:
		return VoteCorrect
	case VoteIncorrect:
		return VoteIncorrect
	}
	return VoteUnparsable
}

// tally applies the majority rule to the votes.
func tally(votes []string, tmpl string) (passed, invalid bool, reason string) {
	n := map[string]int{}
	for _, v := range votes {
		n[v]++
	}
	counts := fmt.Sprintf("%d correct, %d incorrect, %d unparsable of %d (prompt %s)", n[VoteCorrect], n[VoteIncorrect], n[VoteUnparsable], len(votes), tmpl)
	switch {
	case 2*n[VoteCorrect] > len(votes):
		return true, false, "judge: correct by majority: " + counts
	case 2*n[VoteIncorrect] > len(votes):
		return false, false, "judge: incorrect by majority: " + counts
	}
	return false, true, "judge: no majority: " + counts
}

// knownPromptVersion reports whether version is shipped.
func knownPromptVersion(version string) bool {
	return version == PromptQAv1 || version == PromptLongMemEvalV1 || version == PromptAgentVistaV1 || version == PromptBigFinanceV1 || version == PromptCheatBenchV1 || version == PromptCustomV1
}

// judgeTemplate picks the template of version for ans and returns its name
// and text. qa-v1 has one template for an abstention, one for a rubric
// without a reference answer and one for a reference answer. longmemeval-v1
// picks by the question type in ans.Category, as upstream does, and an
// abstention uses the abstention template whatever the type; an unknown type
// has no template, as upstream raises for it.
func judgeTemplate(version string, ans *schemas.AnswerSpec) (string, string, error) {
	var name string
	switch version {
	case PromptQAv1:
		switch {
		case ans.Abstain:
			name = "qa-v1-abstain"
		case len(ans.Expected) == 0 && ans.Rubric != "":
			name = "qa-v1-rubric"
		case len(ans.Expected) == 0:
			return "", "", errors.New("answer spec has neither expected answers nor a rubric for the judge")
		default:
			name = "qa-v1-answer"
		}
	case PromptLongMemEvalV1:
		switch {
		case ans.Abstain:
			name = "longmemeval-v1-abstention"
		case ans.Category == "single-session-user" || ans.Category == "single-session-assistant" || ans.Category == "multi-session":
			name = "longmemeval-v1-standard"
		case ans.Category == "temporal-reasoning":
			name = "longmemeval-v1-temporal"
		case ans.Category == "knowledge-update":
			name = "longmemeval-v1-knowledge-update"
		case ans.Category == "single-session-preference":
			name = "longmemeval-v1-preference"
		default:
			return "", "", fmt.Errorf("prompt %s has no template for question type %q", version, ans.Category)
		}
	case PromptAgentVistaV1:
		if len(ans.Expected) == 0 {
			return "", "", errors.New("answer spec has no expected answer for the judge")
		}
		name = "agentvista-v1"
	case PromptBigFinanceV1:
		switch {
		case len(ans.Expected) == 0:
			return "", "", fmt.Errorf("prompt %s needs the reference answer in expected", version)
		case ans.Rubric == "":
			return "", "", fmt.Errorf("prompt %s needs the rubric", version)
		}
		if _, err := ParseRubricLines(ans.Rubric); err != nil {
			return "", "", fmt.Errorf("prompt %s: %w", version, err)
		}
		name = "bigfinancebench-v1"
	case PromptCheatBenchV1:
		if ans.Rubric == "" {
			return "", "", errors.New("prompt cheatbench-v1 needs the task's cheating criteria as the answer spec rubric")
		}
		name = "cheatbench-v1"
	default:
		return "", "", fmt.Errorf("prompt version %q is not shipped", version)
	}
	b, err := judgePromptFS.ReadFile("judgeprompts/" + name + ".txt")
	if err != nil {
		return "", "", fmt.Errorf("prompt template %s: %w", name, err)
	}
	return name, string(b), nil
}

// renderJudgePrompt fills the template in one pass, so text inside the
// question or the answer is never expanded again. The answer is capped, its
// fence markers are broken up, so it cannot close its own fence, and it is
// wrapped in markers tagged with its own hash (judgeguard.go).
func renderJudgePrompt(tmpl string, ans *schemas.AnswerSpec, answer string) string {
	return renderJudgePromptCap(tmpl, ans, answer, maxJudgeAnswerRunes)
}

// renderJudgePromptCap is renderJudgePrompt with the answer capped at limit
// runes (a cheatbench-v1 trajectory is longer than an answer). The
// trajectory fence of cheatbench-v1 is broken up the same way.
func renderJudgePromptCap(tmpl string, ans *schemas.AnswerSpec, answer string, limit int) string {
	if r := []rune(answer); len(r) > limit {
		answer = string(r[:limit]) + "\n[truncated]"
	}
	defence := strings.NewReplacer("<<<", "< < <", ">>>", "> > >", "<trajectory>", "< trajectory >", "</trajectory>", "< /trajectory >")
	expected := strings.Join(ans.Expected, "\nor: ")
	rubric := ans.Rubric
	if rubric == "" {
		rubric = expected
	}
	return strings.NewReplacer(
		"{{question}}", ans.Question,
		"{{expected}}", expected,
		"{{rubric}}", rubric,
		"{{rubric_lines}}", rubricForJudge(ans.Rubric),
		"{{response}}", fenceAnswer(defence.Replace(strings.TrimSpace(answer))),
	).Replace(tmpl)
}

// contentText reads a message content that is a string or a list of text
// parts.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

// sameModel reports whether the served model is the pinned one. An empty
// served model is accepted (not every provider echoes it), a provider prefix
// on either side is ignored, and a served model that is the pinned alias
// plus a release date (claude-x-20250929, gpt-4o-2024-08-06, claude-x@20250929)
// is the same model. Any other difference, such as gpt-4o-mini for gpt-4o,
// is not.
func sameModel(served, pinned string) bool {
	if served == "" {
		return true
	}
	strip := func(m string) string {
		if i := strings.LastIndex(m, "/"); i >= 0 {
			return m[i+1:]
		}
		return m
	}
	served, pinned = strip(served), strip(pinned)
	if served == pinned {
		return true
	}
	rest, ok := strings.CutPrefix(served, pinned)
	return ok && modelDateRe.MatchString(rest)
}
