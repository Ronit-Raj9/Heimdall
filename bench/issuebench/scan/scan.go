// Package scan finds merged pull requests that close one GitHub issue and
// turn them into issuebench candidates. It is a dry run: GitHub API calls
// only, no model calls, no cost.
package scan

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
)

// Drop reasons, one per filtered pull request (Funnel.Reasons keys).
const (
	ReasonNoIssue       = "no_linked_issue"
	ReasonManyIssues    = "many_linked_issues"
	ReasonIssueAfterPR  = "issue_after_pr"
	ReasonBot           = "bot_author"
	ReasonNoTests       = "no_test_change"
	ReasonNoCode        = "no_code_change"
	ReasonTooManyFiles  = "too_many_files"
	ReasonTooBig        = "fix_too_big"
	ReasonBinary        = "binary_file"
	ReasonIssueShort    = "issue_too_short"
	ReasonIssueLong     = "issue_too_long"
	ReasonNoMergeCommit = "no_merge_commit"
	ReasonBaseMismatch  = "base_mismatch"
	ReasonDiffTooBig    = "diff_too_big"
	ReasonDiffFailed    = "diff_failed"
	ReasonNetwork       = "needs_network"
	ReasonSecret        = "needs_secret"
	ReasonLeak          = "fix_in_issue"
	ReasonLanguage      = "other_language"
)

// Options bound one scan.
type Options struct {
	Repo          string    // owner/name
	MaxPRs        int       // merged pull requests to read; 0 = 200, cap 2000
	Since         time.Time // zero = no bound; else stop at the first PR opened before it
	MaxFiles      int       // 0 = 40
	MaxFixLines   int       // changed lines in code files; 0 = 800
	MinIssueChars int       // 0 = 40
	MaxIssueChars int       // 0 = 20000
	Language      string    // "" = any; else keep only this language
	Now           func() time.Time
}

func (o *Options) defaults() {
	if o.MaxPRs <= 0 {
		o.MaxPRs = 200
	}
	if o.MaxPRs > 2000 {
		o.MaxPRs = 2000
	}
	if o.MaxFiles <= 0 {
		o.MaxFiles = 40
	}
	if o.MaxFixLines <= 0 {
		o.MaxFixLines = 800
	}
	if o.MinIssueChars <= 0 {
		o.MinIssueChars = 40
	}
	if o.MaxIssueChars <= 0 {
		o.MaxIssueChars = 20000
	}
}

// Result is a scan's answer.
type Result struct {
	Repo          string                 `json:"repo"`
	DefaultBranch string                 `json:"default_branch"`
	Language      string                 `json:"language"`
	Candidates    []issuebench.Candidate `json:"candidates"`
	Dropped       []issuebench.Candidate `json:"dropped"` // Skip says why; no patches
	Funnel        issuebench.Funnel      `json:"funnel"`
	Truncated     bool                   `json:"truncated,omitempty"` // more merged pull requests exist
}

// ErrBadRepo is a repo that is not owner/name.
var ErrBadRepo = errors.New("repo must be owner/name")

var repoRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)

// SplitRepo checks and splits owner/name.
func SplitRepo(repo string) (string, string, error) {
	repo = strings.TrimSpace(repo)
	repo = strings.TrimPrefix(repo, "https://github.com/")
	repo = strings.TrimSuffix(strings.TrimSuffix(repo, "/"), ".git")
	if !repoRE.MatchString(repo) {
		return "", "", ErrBadRepo
	}
	i := strings.IndexByte(repo, '/')
	return repo[:i], repo[i+1:], nil
}

// Scan reads merged pull requests into the default branch, newest first,
// and keeps those that qualify as benchmark tasks.
func Scan(ctx context.Context, c *Client, opt Options) (*Result, error) {
	opt.defaults()
	owner, name, err := SplitRepo(opt.Repo)
	if err != nil {
		return nil, err
	}
	var meta ghRepoPage
	if err := c.GraphQL(ctx, metaQuery, map[string]any{"owner": owner, "name": name}, &meta); err != nil {
		return nil, err
	}
	if meta.Repository == nil {
		return nil, &APIError{Status: 404}
	}
	res := &Result{Repo: meta.Repository.NameWithOwner, Funnel: issuebench.Funnel{Reasons: map[string]int{}}}
	if res.Repo == "" {
		res.Repo = owner + "/" + name
	}
	if meta.Repository.DefaultBranchRef == nil {
		return res, nil // empty repository
	}
	res.DefaultBranch = meta.Repository.DefaultBranchRef.Name
	lang := pickLanguage(meta.Repository.PrimaryLanguage)
	res.Language = langName(lang, meta.Repository.PrimaryLanguage)

	s := &scanner{c: c, opt: opt, res: res, lang: lang, owner: owner, name: name, issues: map[int]*ghIssue{}}
	var after any
	read := 0
	for read < opt.MaxPRs {
		first := opt.MaxPRs - read
		if first > 50 {
			first = 50
		}
		var page ghRepoPage
		vars := map[string]any{"owner": owner, "name": name, "base": res.DefaultBranch, "first": first, "after": after}
		if err := c.GraphQL(ctx, repoQuery, vars, &page); err != nil {
			return nil, err
		}
		if page.Repository == nil {
			break
		}
		prs := page.Repository.PullRequests
		stop := false
		for i := range prs.Nodes {
			pr := &prs.Nodes[i]
			if !opt.Since.IsZero() && pr.CreatedAt.Before(opt.Since) {
				// Newest first by creation: everything after this is older.
				stop = true
				break
			}
			read++
			if err := s.one(ctx, pr); err != nil {
				return nil, err
			}
			if read >= opt.MaxPRs {
				stop = true
				break
			}
		}
		if !prs.PageInfo.HasNextPage || len(prs.Nodes) == 0 {
			break
		}
		if stop {
			res.Truncated = read >= opt.MaxPRs
			break
		}
		after = prs.PageInfo.EndCursor
	}
	res.Funnel.Qualified = len(res.Candidates)
	return res, nil
}

type scanner struct {
	c           *Client
	opt         Options
	res         *Result
	lang        issuebench.Language
	owner, name string
	issues      map[int]*ghIssue // keyword fallback cache
}

func (s *scanner) drop(cd issuebench.Candidate, reason string) {
	cd.Skip = reason
	cd.TestPatch, cd.FixPatch = "", ""
	s.res.Funnel.Reasons[reason]++
	s.res.Dropped = append(s.res.Dropped, cd)
}

// one judges one merged pull request. Only API failures that end the scan
// (auth, rate limit) come back as errors; per-PR trouble is a drop.
func (s *scanner) one(ctx context.Context, pr *ghPR) error {
	s.res.Funnel.MergedPRs++
	cd := issuebench.Candidate{Repo: s.res.Repo, PR: pr.Number, PRMerged: pr.MergedAt, Language: s.res.Language}
	if pr.MergeCommit != nil {
		cd.MergeSHA = pr.MergeCommit.Oid
	}

	issues, err := s.linked(ctx, pr)
	if err != nil {
		return err
	}
	if len(issues) == 0 {
		s.drop(cd, ReasonNoIssue)
		return nil
	}
	s.res.Funnel.Linked++
	is := issues[0]
	cd.Issue, cd.IssueTitle, cd.IssueBody, cd.IssueCreated = is.Number, is.Title, is.Body, is.CreatedAt
	if len(issues) > 1 {
		s.drop(cd, ReasonManyIssues)
		return nil
	}
	if s.opt.Language != "" && !strings.EqualFold(s.opt.Language, s.res.Language) {
		s.drop(cd, ReasonLanguage)
		return nil
	}
	if !is.CreatedAt.Before(pr.CreatedAt) {
		s.drop(cd, ReasonIssueAfterPR)
		return nil
	}
	if isBot(pr.Author) {
		s.drop(cd, ReasonBot)
		return nil
	}
	text := strings.TrimSpace(is.Title + "\n" + is.Body)
	if len(strings.TrimSpace(is.Body)) < s.opt.MinIssueChars {
		s.drop(cd, ReasonIssueShort)
		return nil
	}
	if len(text) > s.opt.MaxIssueChars {
		s.drop(cd, ReasonIssueLong)
		return nil
	}
	if pr.Files.TotalCount > len(pr.Files.Nodes) || len(pr.Files.Nodes) > s.opt.MaxFiles {
		s.drop(cd, ReasonTooManyFiles)
		return nil
	}
	fixLines := 0
	for _, f := range pr.Files.Nodes {
		if binaryPath(f.Path) {
			s.drop(cd, ReasonBinary)
			return nil
		}
		if s.isTest(f.Path) {
			cd.TestFiles = append(cd.TestFiles, f.Path)
			continue
		}
		cd.CodeFiles = append(cd.CodeFiles, f.Path)
		if sourcePath(f.Path) {
			fixLines += f.Additions + f.Deletions
		}
	}
	if len(cd.TestFiles) == 0 {
		s.drop(cd, ReasonNoTests)
		return nil
	}
	if !anySource(cd.CodeFiles) {
		s.drop(cd, ReasonNoCode)
		return nil
	}
	if fixLines > s.opt.MaxFixLines {
		s.drop(cd, ReasonTooBig)
		return nil
	}
	if cd.MergeSHA == "" {
		s.drop(cd, ReasonNoMergeCommit)
		return nil
	}

	// Base commit: the merge commit's first parent on the default branch,
	// checked against the PR's own file list. A rebase merge puts the PR's
	// commits in a row, so walk back that many first parents.
	want := map[string]bool{}
	for _, f := range pr.Files.Nodes {
		want[f.Path] = true
	}
	var diff string
	base := cd.MergeSHA
	steps := []int{1}
	if n := pr.Commits.TotalCount; n > 1 && n <= 50 {
		steps = append(steps, n-1)
	}
	ok := false
	for _, k := range steps {
		for i := 0; i < k; i++ {
			p, err := s.c.FirstParent(ctx, s.res.Repo, base)
			if err != nil {
				if fatal(err) {
					return err
				}
				s.drop(cd, ReasonDiffFailed)
				return nil
			}
			if p == "" {
				break
			}
			base = p
		}
		d, err := s.c.Diff(ctx, s.res.Repo, base, cd.MergeSHA)
		if errors.Is(err, ErrDiffTooBig) {
			s.drop(cd, ReasonDiffTooBig)
			return nil
		}
		if err != nil {
			if fatal(err) {
				return err
			}
			s.drop(cd, ReasonDiffFailed)
			return nil
		}
		if sameFiles(diffFiles(d), want) {
			diff, ok = d, true
			break
		}
	}
	if !ok {
		s.drop(cd, ReasonBaseMismatch)
		return nil
	}
	cd.BaseSHA = base
	if strings.Contains(diff, "\nBinary files ") || strings.Contains(diff, "GIT binary patch") {
		s.drop(cd, ReasonBinary)
		return nil
	}
	cd.TestPatch, cd.FixPatch = splitDiff(diff, s.isTest)
	if r := envReason(cd.TestPatch, cd.FixPatch); r != "" {
		s.drop(cd, r)
		return nil
	}
	if Leaks(text, cd.FixPatch) {
		s.drop(cd, ReasonLeak)
		return nil
	}
	s.res.Candidates = append(s.res.Candidates, cd)
	return nil
}

// fatal is an error that ends the whole scan: auth or rate limit.
func fatal(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status == 401 || ae.Status == 403 || ae.Status == 429
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// keywordRE is the SWE-bench style link: a closing keyword then #N or
// owner/repo#N.
var keywordRE = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\b:?\s+(?:([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+))?#(\d+)\b`)

// KeywordIssues returns issue numbers in text linked with a closing
// keyword to repo (owner/name); other repositories are ignored.
func KeywordIssues(text, repo string) []int {
	var out []int
	seen := map[int]bool{}
	for _, m := range keywordRE.FindAllStringSubmatch(text, -1) {
		if m[1] != "" && !strings.EqualFold(m[1], repo) {
			continue
		}
		n, err := strconv.Atoi(m[2])
		if err != nil || n <= 0 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// linked returns the PR's issues in this repository: GitHub's closing
// references, else the keyword fallback over title, body and commits.
func (s *scanner) linked(ctx context.Context, pr *ghPR) ([]*ghIssue, error) {
	var out []*ghIssue
	for i := range pr.Closing.Nodes {
		is := &pr.Closing.Nodes[i]
		if is.Repository.NameWithOwner == "" || strings.EqualFold(is.Repository.NameWithOwner, s.res.Repo) {
			out = append(out, is)
		}
	}
	if len(out) > 0 || pr.Closing.TotalCount > len(pr.Closing.Nodes) {
		if pr.Closing.TotalCount > len(out) && len(out) == 1 {
			out = append(out, out[0]) // more beyond the page: many
		}
		return out, nil
	}
	parts := []string{pr.Title, pr.Body}
	for _, c := range pr.Commits.Nodes {
		parts = append(parts, c.Commit.Message)
	}
	for _, n := range KeywordIssues(strings.Join(parts, "\n"), s.res.Repo) {
		if n == pr.Number {
			continue
		}
		is, ok := s.issues[n]
		if !ok {
			var a ghIssueAnswer
			err := s.c.GraphQL(ctx, issueQuery, map[string]any{"owner": s.owner, "name": s.name, "number": n}, &a)
			if err != nil {
				if fatal(err) {
					return nil, err
				}
			} else if a.Repository != nil && a.Repository.IssueOrPullRequest != nil && a.Repository.IssueOrPullRequest.Typename == "Issue" {
				is = a.Repository.IssueOrPullRequest
			}
			s.issues[n] = is
		}
		if is != nil {
			out = append(out, is)
		}
	}
	return out, nil
}

var botLogins = map[string]bool{"dependabot": true, "renovate": true, "pre-commit-ci": true, "github-actions": true, "greenkeeper": true, "snyk-bot": true, "pyup-bot": true, "allcontributors": true}

func isBot(a *ghActor) bool {
	if a == nil {
		return false // ghost user: a deleted human account
	}
	l := strings.ToLower(a.Login)
	return a.Typename == "Bot" || strings.HasSuffix(l, "[bot]") || strings.HasSuffix(l, "-bot") || botLogins[strings.TrimSuffix(l, "[bot]")]
}

// pickLanguage maps GitHub's primary language to a registered Language.
func pickLanguage(pl *struct {
	Name string `json:"name"`
}) issuebench.Language {
	if pl == nil {
		return nil
	}
	for _, l := range issuebench.Languages() {
		if strings.EqualFold(l.Name(), pl.Name) {
			return l
		}
	}
	return nil
}

func langName(l issuebench.Language, pl *struct {
	Name string `json:"name"`
}) string {
	if l != nil {
		return l.Name()
	}
	if pl == nil {
		return ""
	}
	return strings.ToLower(pl.Name)
}

func (s *scanner) isTest(p string) bool {
	if s.lang != nil {
		return s.lang.IsTestFile(p)
	}
	return HeuristicTestFile(p)
}

func sameFiles(got []string, want map[string]bool) bool {
	if len(got) != len(want) {
		return false
	}
	for _, g := range got {
		if !want[g] {
			return false
		}
	}
	return true
}
