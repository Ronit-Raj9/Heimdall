package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// GradeSourceReviewed is schemas.GradeSourceReviewed: the sealed original
// grades with human reviews applied on top.
const GradeSourceReviewed = schemas.GradeSourceReviewed

const (
	reviewsDir     = "reviews"
	reviewFile     = "review.json"
	reviewsLogFile = "reviews.log"

	maxReviewReason   = 4000 // characters
	maxReviewer       = 200
	maxReviewEvidence = 50 // items
	maxEvidenceItem   = 500
)

// ReviewSet is an experiment's reviews as read from reviews.log and checked:
// Valid holds every review whose record is present, hashes to the hash its
// log line names and chains to the line before, in log order (oldest
// first). Every review that failed a check is left out of Valid and named in
// Problems; Verified is true only when there is no problem at all. A review
// is checked against its run's seal (parent_seal) where it is applied, since
// that needs the run.
type ReviewSet struct {
	Valid    []schemas.Review
	Problems []string
	Verified bool
}

// AddReview writes a human review of one sealed run: the record at
// reviews/<review_id>/review.json (made read-only) and the line "<review_id>
// <run_id> <hash>" appended to the experiment's reviews.log with fsync, under
// a lock, so the chain is linear even with several reviewers at once. The run
// must be sealed and must verify against its seal; nothing in the run folder
// is written. A review never replaces another: a later review of the same run
// supersedes it under grade source reviewed and both stay in the log.
//
// The record is written before its log line, so a crash between the two
// leaves a record that is not in the log; readers report and ignore it.
func (s *FSStore) AddReview(ctx context.Context, expID string, req *schemas.ReviewRequest) (*schemas.Review, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateID("exp_id", expID); err != nil {
		return nil, fmt.Errorf("add review: %w", err)
	}
	if err := ValidateReviewRequest(req); err != nil {
		return nil, fmt.Errorf("add review: %w", err)
	}
	dir, err := s.findRunDir(expID, req.RunID)
	if err != nil {
		return nil, fmt.Errorf("add review: %w", err)
	}
	run, seal, err := s.reviewableRun(expID, req.RunID, dir)
	if err != nil {
		return nil, fmt.Errorf("add review of run %s: %w", req.RunID, err)
	}
	log, unlock, err := s.openReviewsLog(expID)
	if err != nil {
		return nil, fmt.Errorf("add review of run %s: %w", req.RunID, err)
	}
	defer unlock()
	b, err := io.ReadAll(log)
	if err != nil {
		return nil, fmt.Errorf("add review of run %s: read reviews.log: %w", req.RunID, err)
	}
	set, last := s.checkReviews(expID, b)
	rv := &schemas.Review{
		ReviewID: utils.NewID("rv", 10), ExpID: expID, RunID: req.RunID, ParentSeal: seal.Hash,
		Decision: req.Decision, Reason: strings.TrimSpace(req.Reason), Reviewer: strings.TrimSpace(req.Reviewer),
		CreatedAt: s.now().UTC(), SealedOutcome: run.Outcome, PreviousOutcome: run.Outcome,
		Evidence: cleanEvidence(req.Evidence), PrevHash: last,
	}
	if cur, _ := EffectiveReview(set.ForRun(req.RunID), seal.Hash); cur != nil {
		rv.PreviousOutcome, _ = cur.Decision.Outcome()
		rv.Supersedes = cur.ReviewID
	}
	if rv.Hash, err = reviewHash(rv); err != nil {
		return nil, fmt.Errorf("add review of run %s: %w", req.RunID, err)
	}
	if err := s.writeReviewRecord(expID, rv); err != nil {
		return nil, fmt.Errorf("add review of run %s: %w", req.RunID, err)
	}
	if err := appendLineTo(log, []byte(rv.ReviewID+" "+rv.RunID+" "+rv.Hash)); err != nil {
		return nil, fmt.Errorf("add review of run %s: append reviews.log: %w", req.RunID, err)
	}
	s.logger.Info("review recorded", "review_id", rv.ReviewID, "run_id", rv.RunID, "decision", string(rv.Decision))
	return rv, nil
}

// Reviews reads and checks an experiment's reviews (see ReviewSet). An
// experiment without reviews has an empty, verified set. reviews.log is
// append-only, so it is read without the lock.
func (s *FSStore) Reviews(ctx context.Context, expID string) (*ReviewSet, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateID("exp_id", expID); err != nil {
		return nil, fmt.Errorf("reviews: %w", err)
	}
	if _, err := os.Stat(filepath.Join(s.expDir(expID), manifestFile)); err != nil {
		return nil, fmt.Errorf("reviews: experiment %s: %w", expID, schemas.ErrNotFound)
	}
	b, err := os.ReadFile(filepath.Join(s.expDir(expID), reviewsLogFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("reviews %s: read reviews.log: %w", expID, err)
	}
	set, _ := s.checkReviews(expID, b)
	return set, nil
}

// RunExp returns the experiment a run belongs to, from the run id alone, so a
// reviewer can name a run without its experiment. A run id found in no
// experiment is schemas.ErrNotFound, and one found in two is an error.
func (s *FSStore) RunExp(runID string) (string, error) {
	if err := validateID("run_id", runID); err != nil {
		return "", fmt.Errorf("find run: %w", err)
	}
	matches, err := filepath.Glob(filepath.Join(s.root, "*", slotsDir, "*", runsDir, runID))
	if err != nil {
		return "", fmt.Errorf("find run %s: %w", runID, err)
	}
	exps := map[string]bool{}
	for _, p := range matches {
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			continue
		}
		exps[strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]] = true
	}
	switch len(exps) {
	case 0:
		return "", fmt.Errorf("run %s: %w", runID, schemas.ErrNotFound)
	case 1:
		for id := range exps {
			return id, nil
		}
	}
	return "", fmt.Errorf("run %s is in %d experiments; name the experiment", runID, len(exps))
}

// ForRun returns the valid reviews of one run, in log order.
func (rs *ReviewSet) ForRun(runID string) []schemas.Review {
	var out []schemas.Review
	for _, r := range rs.Valid {
		if r.RunID == runID {
			out = append(out, r)
		}
	}
	return out
}

// EffectiveReview returns the review that decides a run's outcome under grade
// source reviewed: the newest (last in log order) pass, fail or invalid
// review whose parent_seal is the run's seal hash. flag_only reviews never
// decide. A decision review written against another seal is ignored and
// named in the returned problems.
func EffectiveReview(reviews []schemas.Review, sealHash string) (*schemas.Review, []string) {
	var problems []string
	for i := len(reviews) - 1; i >= 0; i-- {
		r := reviews[i]
		if _, decides := r.Decision.Outcome(); !decides {
			continue
		}
		if r.ParentSeal != sealHash {
			problems = append(problems, fmt.Sprintf("review %s of run %s ignored: parent_seal %s is not the run seal %s",
				r.ReviewID, r.RunID, r.ParentSeal, sealHash))
			continue
		}
		return &r, problems
	}
	return nil, problems
}

// ValidateReviewRequest checks a review request before anything is read:
// a run id, one of the four decisions, a plain-text reason and a reviewer
// name, within their lengths. Errors wrap schemas.ErrReviewInput.
func ValidateReviewRequest(req *schemas.ReviewRequest) error {
	switch {
	case req == nil:
		return fmt.Errorf("nil request: %w", schemas.ErrReviewInput)
	case validateID("run_id", req.RunID) != nil:
		return fmt.Errorf("run_id %q is not a valid id: %w", req.RunID, schemas.ErrReviewInput)
	case !req.Decision.Valid():
		return fmt.Errorf("decision %q is not pass, fail, invalid or flag_only: %w", req.Decision, schemas.ErrReviewInput)
	case strings.TrimSpace(req.Reason) == "":
		return fmt.Errorf("reason is required: %w", schemas.ErrReviewInput)
	case utf8.RuneCountInString(req.Reason) > maxReviewReason || !utf8.ValidString(req.Reason):
		return fmt.Errorf("reason must be valid text of at most %d characters: %w", maxReviewReason, schemas.ErrReviewInput)
	case strings.TrimSpace(req.Reviewer) == "":
		return fmt.Errorf("reviewer is required: %w", schemas.ErrReviewInput)
	case utf8.RuneCountInString(req.Reviewer) > maxReviewer || strings.ContainsAny(req.Reviewer, "\r\n") || !utf8.ValidString(req.Reviewer):
		return fmt.Errorf("reviewer must be one line of at most %d characters: %w", maxReviewer, schemas.ErrReviewInput)
	case len(req.Evidence) > maxReviewEvidence:
		return fmt.Errorf("at most %d evidence items: %w", maxReviewEvidence, schemas.ErrReviewInput)
	}
	for _, e := range req.Evidence {
		if utf8.RuneCountInString(e) > maxEvidenceItem || !utf8.ValidString(e) {
			return fmt.Errorf("evidence items must be valid text of at most %d characters: %w", maxEvidenceItem, schemas.ErrReviewInput)
		}
	}
	return nil
}

// reviewableRun reads the run a review is about and checks it is sealed and
// verifies against its seal.json and seals.log.
func (s *FSStore) reviewableRun(expID, runID, dir string) (*schemas.Run, *schemas.SealRecord, error) {
	sealed, err := isSealed(dir)
	if err != nil {
		return nil, nil, err
	}
	if !sealed {
		return nil, nil, fmt.Errorf("run is not sealed; only sealed runs are reviewed: %w", schemas.ErrReviewInput)
	}
	logged, err := s.loggedSeals(expID)
	if err != nil {
		return nil, nil, err
	}
	seal, err := s.verifySealedRun(dir, runID, logged)
	if err != nil {
		return nil, nil, fmt.Errorf("seal does not verify, so the run cannot be reviewed: %w", err)
	}
	var run schemas.Run
	if err := readJSON(filepath.Join(dir, runFile), &run); err != nil {
		return nil, nil, err
	}
	return &run, seal, nil
}

// openReviewsLog opens <exp>/reviews.log for read and append under the
// experiment's review mutex and flock. The returned function unlocks and
// closes it.
func (s *FSStore) openReviewsLog(expID string) (*os.File, func(), error) {
	unlockMu := s.expLocks.lock("reviews/" + expID)
	f, err := os.OpenFile(filepath.Join(s.expDir(expID), reviewsLogFile), os.O_RDWR|os.O_APPEND|os.O_CREATE, filePerm)
	if err != nil {
		unlockMu()
		return nil, nil, fmt.Errorf("open reviews.log: %w", err)
	}
	unlockFile, err := lockFile(f)
	if err != nil {
		unlockMu()
		return nil, nil, errors.Join(err, f.Close())
	}
	return f, func() {
		if err := unlockFile(); err != nil {
			s.logger.Warn("reviews.log unlock failed", "exp_id", expID, "error", err.Error())
		}
		unlockMu()
	}, nil
}

// writeReviewRecord writes reviews/<id>/review.json once and makes it
// read-only.
func (s *FSStore) writeReviewRecord(expID string, rv *schemas.Review) error {
	dir := filepath.Join(s.expDir(expID), reviewsDir, rv.ReviewID)
	if err := os.MkdirAll(filepath.Dir(dir), dirPerm); err != nil {
		return err
	}
	if err := os.Mkdir(dir, dirPerm); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("review %s: %w", rv.ReviewID, schemas.ErrExists)
		}
		return err
	}
	b, err := marshalIndent(rv)
	if err != nil {
		return err
	}
	p := filepath.Join(dir, reviewFile)
	if err := writeFileExclusive(p, b); err != nil {
		return err
	}
	return os.Chmod(p, sealedFilePerm)
}

// checkReviews parses reviews.log and checks every record it names (see
// ReviewSet). It also returns the hash on the last well-formed line, which a
// new review chains to. A malformed line breaks the chain: no record after
// it can chain to it, so every later record is reported and ignored. A
// record whose content was changed is ignored alone, since later records
// chain to its log line, not to its content.
func (s *FSStore) checkReviews(expID string, log []byte) (*ReviewSet, string) {
	set := &ReviewSet{}
	prev, broken := "", false
	logged := map[string]bool{}
	for i, line := range bytes.Split(log, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		f := strings.Fields(string(line))
		if len(f) != 3 || validateID("review_id", f[0]) != nil || validateID("run_id", f[1]) != nil || !isHexSHA256(f[2]) {
			set.Problems = append(set.Problems, fmt.Sprintf("reviews.log line %d is malformed; the chain is broken there", i+1))
			broken = true
			continue
		}
		id, runID, hash := f[0], f[1], f[2]
		if logged[id] {
			set.Problems = append(set.Problems, fmt.Sprintf("reviews.log line %d: review %s logged twice; the later line is ignored", i+1, id))
			continue
		}
		logged[id] = true
		rv, err := s.checkReviewRecord(expID, id, runID, hash, prev, broken)
		if err != nil {
			set.Problems = append(set.Problems, fmt.Sprintf("review %s of run %s (reviews.log line %d) ignored: %v", id, runID, i+1, err))
		} else {
			set.Valid = append(set.Valid, *rv)
		}
		prev = hash
	}
	set.Problems = append(set.Problems, s.unloggedReviews(expID, logged)...)
	set.Verified = len(set.Problems) == 0
	return set, prev
}

// checkReviewRecord checks one logged review's record: present, about the
// logged run and experiment, hashing to the logged hash, and chained to the
// previous line.
func (s *FSStore) checkReviewRecord(expID, id, runID, hash, prev string, broken bool) (*schemas.Review, error) {
	rv, err := s.readReviewRecord(expID, id)
	if err != nil {
		return nil, err
	}
	h, err := reviewHash(rv)
	if err != nil {
		return nil, err
	}
	if err := reviewMismatch(rv, expID, id, runID, hash, h, prev, broken); err != nil {
		return nil, err
	}
	return rv, nil
}

// reviewMismatch names the first check a logged review record fails, h being
// the hash of its content.
func reviewMismatch(rv *schemas.Review, expID, id, runID, hash, h, prev string, broken bool) error {
	switch {
	case rv.ReviewID != id || rv.RunID != runID || rv.ExpID != expID:
		return fmt.Errorf("record names review %s of run %s in %s, log says review %s of run %s", rv.ReviewID, rv.RunID, rv.ExpID, id, runID)
	case h != rv.Hash:
		return fmt.Errorf("content hashes to %s, record says %s (the record was changed)", h, rv.Hash)
	case rv.Hash != hash:
		return fmt.Errorf("record hash %s, reviews.log says %s", rv.Hash, hash)
	case broken:
		return errors.New("an earlier reviews.log line is malformed, so the chain cannot be checked")
	case rv.PrevHash != prev:
		return fmt.Errorf("prev_hash %q, the line before says %q (the chain is broken)", rv.PrevHash, prev)
	case !rv.Decision.Valid():
		return fmt.Errorf("decision %q is not known", rv.Decision)
	}
	return nil
}

// readReviewRecord reads reviews/<id>/review.json.
func (s *FSStore) readReviewRecord(expID, id string) (*schemas.Review, error) {
	var rv schemas.Review
	found, err := readOptionalJSON(filepath.Join(s.expDir(expID), reviewsDir, id, reviewFile), &rv)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("review.json is missing")
	}
	return &rv, nil
}

// unloggedReviews names review folders that reviews.log does not list: a
// write interrupted before its log line, or a line removed.
func (s *FSStore) unloggedReviews(expID string, logged map[string]bool) []string {
	entries, err := os.ReadDir(filepath.Join(s.expDir(expID), reviewsDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !logged[e.Name()] {
			out = append(out, fmt.Sprintf("review %s ignored: it is not in reviews.log (interrupted before it was logged, or its line was removed)", e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// reviewHash is the sha256 of the review's canonical JSON with Hash empty.
func reviewHash(rv *schemas.Review) (string, error) {
	c := *rv
	c.Hash = ""
	return utils.HashJSON(&c)
}

// cleanEvidence trims the cited evidence and drops empty items.
func cleanEvidence(in []string) []string {
	var out []string
	for _, e := range in {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}
