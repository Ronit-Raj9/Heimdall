package schedule

// Weekly issue sync: issue-sync builds new tasks from the merged fixes of
// one connected repository. Its schedule is named IssueSyncPrefix plus the
// sync id; the sync keeps its own weekly spending cap.
const JobIssueSync = "issue-sync"

// IssueSyncPrefix starts the name of a weekly issue sync schedule.
const IssueSyncPrefix = "issue-sync-"

func init() { Jobs = append(Jobs, JobIssueSync) }
