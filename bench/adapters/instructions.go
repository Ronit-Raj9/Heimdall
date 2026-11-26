package adapters

import "strings"

// instructionText puts the owner's standing instructions before the task.
func instructionText(standing, task string) string {
	if strings.TrimSpace(standing) == "" {
		return task
	}
	return "Standing instructions from the agent's owner:\n\n" + strings.TrimRight(standing, "\n") + "\n\n---\n\n" + task
}
