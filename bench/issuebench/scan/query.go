package scan

import "time"

// metaQuery reads the repository's default branch and main language;
// repoQuery one page of merged pull requests into the default branch,
// newest first, with the issues GitHub links to each one.
const metaQuery = `query($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    nameWithOwner
    defaultBranchRef { name }
    primaryLanguage { name }
  }
}`

const repoQuery = `query($owner: String!, $name: String!, $base: String!, $first: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequests(states: MERGED, baseRefName: $base, first: $first, after: $after, orderBy: {field: CREATED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number
        title
        body
        createdAt
        mergedAt
        baseRefName
        baseRefOid
        author { __typename login }
        mergeCommit { oid parents(first: 2) { nodes { oid } } }
        commits(last: 20) { totalCount nodes { commit { message } } }
        files(first: 100) { totalCount nodes { path additions deletions changeType } }
        closingIssuesReferences(first: 5) {
          totalCount
          nodes { number title body createdAt author { __typename login } repository { nameWithOwner } }
        }
      }
    }
  }
}`

// issueQuery reads one issue (or pull request) by number, for links found
// by the keyword fallback.
const issueQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issueOrPullRequest(number: $number) {
      __typename
      ... on Issue { number title body createdAt author { __typename login } repository { nameWithOwner } }
    }
  }
}`

type ghActor struct {
	Typename string `json:"__typename"`
	Login    string `json:"login"`
}

type ghIssue struct {
	Typename   string    `json:"__typename"`
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
	Author     *ghActor  `json:"author"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
}

type ghFile struct {
	Path       string `json:"path"`
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
	ChangeType string `json:"changeType"` // ADDED, MODIFIED, DELETED, RENAMED, COPIED, CHANGED
}

type ghPR struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"createdAt"`
	MergedAt    time.Time `json:"mergedAt"`
	BaseRefName string    `json:"baseRefName"`
	BaseRefOid  string    `json:"baseRefOid"`
	Author      *ghActor  `json:"author"`
	MergeCommit *struct {
		Oid     string `json:"oid"`
		Parents struct {
			Nodes []struct {
				Oid string `json:"oid"`
			} `json:"nodes"`
		} `json:"parents"`
	} `json:"mergeCommit"`
	Commits struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			Commit struct {
				Message string `json:"message"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	Files struct {
		TotalCount int      `json:"totalCount"`
		Nodes      []ghFile `json:"nodes"`
	} `json:"files"`
	Closing struct {
		TotalCount int       `json:"totalCount"`
		Nodes      []ghIssue `json:"nodes"`
	} `json:"closingIssuesReferences"`
}

type ghRepoPage struct {
	Repository *struct {
		NameWithOwner    string `json:"nameWithOwner"`
		DefaultBranchRef *struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
		PrimaryLanguage *struct {
			Name string `json:"name"`
		} `json:"primaryLanguage"`
		PullRequests struct {
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []ghPR `json:"nodes"`
		} `json:"pullRequests"`
	} `json:"repository"`
}

type ghIssueAnswer struct {
	Repository *struct {
		IssueOrPullRequest *ghIssue `json:"issueOrPullRequest"`
	} `json:"repository"`
}
