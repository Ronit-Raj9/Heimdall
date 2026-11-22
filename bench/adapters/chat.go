package adapters

import (
	"encoding/json"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Chat tasks (lane Y13, schemas/chat.go): the chat adapters (remote_http,
// openai_agent) send a conversation task's messages as a messages array
// and its scripted user turns one by one; chatFile is where their launcher
// reads the conversation.
const chatFile = "/tmp/bench/chat.json"

// chatPlanFile is the task's chat file as the launcher reads it, "" for a
// task without one.
func chatPlanFile(rc *schemas.RunContext) (string, error) {
	if rc.Task == nil || rc.Task.Chat == nil {
		return "", nil
	}
	cf, err := schemas.ReadChatFile(rc.Task)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(cf)
	if err != nil {
		return "", fmt.Errorf("task %s: chat file: %w", rc.Task.ID, err)
	}
	return string(b), nil
}

// HoldsConversation reports whether adapter can be given a conversation
// task's scripted user turns.
func HoldsConversation(adapter string) bool {
	return adapter == AdapterRemoteHTTP || adapter == AdapterOpenAIAgent
}
