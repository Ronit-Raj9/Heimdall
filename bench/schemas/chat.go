package schemas

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Chat tasks (lane Y13): a row of the customer's data that is a
// conversation, not one question. The task's chat file holds the messages
// the agent receives as they are (system, user and assistant turns, ending
// with the user's message), the user turns a script sends after each of the
// agent's replies, and the attachments that go with the first message. The
// chat agents (remote_http, openai_agent) send the messages as a messages
// array; every other agent reads instruction.md, the conversation as text,
// and cannot be given later user turns (the planner refuses that pairing).

// ChatInput is task.yaml's chat block.
type ChatInput struct {
	// File is the chat file (ChatFile), relative to the task folder.
	File string `json:"file" yaml:"file"`
	// UserTurns is how many scripted user turns follow the first reply. It
	// repeats len(ChatFile.UserTurns) so the planner can refuse an agent
	// that cannot hold a conversation without reading the file.
	UserTurns int `json:"user_turns,omitempty" yaml:"user_turns,omitempty"`
	// GradeOn is final (default: the agent's last reply is the answer) or
	// transcript (the whole conversation, every turn labelled, is the
	// answer the verifier or the judge reads).
	GradeOn string `json:"grade_on,omitempty" yaml:"grade_on,omitempty"`
}

// Chat grading targets (ChatInput.GradeOn).
const (
	ChatGradeFinal      = "final"
	ChatGradeTranscript = "transcript"
)

// Chat message roles.
const (
	ChatRoleSystem    = "system"
	ChatRoleUser      = "user"
	ChatRoleAssistant = "assistant"
)

// ChatMessage is one turn of a conversation.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatAttachment is a file or image that goes with the first user message:
// a file stored with the task (Path, relative to the workspace, under
// attachments/) or a link the agent fetches itself (URL). The bench never
// downloads a URL.
type ChatAttachment struct {
	Name      string `json:"name"`
	MediaType string `json:"media_type,omitempty"`
	Path      string `json:"path,omitempty"`
	URL       string `json:"url,omitempty"`
}

// ChatFile is the content of the chat file.
type ChatFile struct {
	Messages    []ChatMessage    `json:"messages"`
	UserTurns   []string         `json:"user_turns,omitempty"`
	Attachments []ChatAttachment `json:"attachments,omitempty"`
	GradeOn     string           `json:"grade_on,omitempty"`
}

// Limits of a chat file.
const (
	MaxChatMessages    = 200
	MaxChatUserTurns   = 20
	MaxChatAttachments = 20
)

// Validate checks a chat file: known roles, the last message is the user's,
// at most MaxChatUserTurns scripted turns, attachments with exactly one of
// path (under attachments/) or an http(s) URL.
func (c *ChatFile) Validate() error {
	if len(c.Messages) == 0 {
		return errors.New("chat: no messages")
	}
	if len(c.Messages) > MaxChatMessages {
		return fmt.Errorf("chat: %d messages, at most %d", len(c.Messages), MaxChatMessages)
	}
	for i, m := range c.Messages {
		switch m.Role {
		case ChatRoleSystem, ChatRoleUser, ChatRoleAssistant:
		default:
			return fmt.Errorf("chat: message %d has role %q: want system, user or assistant", i+1, m.Role)
		}
		if strings.TrimSpace(m.Content) == "" {
			return fmt.Errorf("chat: message %d is empty", i+1)
		}
	}
	if last := c.Messages[len(c.Messages)-1]; last.Role != ChatRoleUser {
		return errors.New("chat: the last message must be the user's (the one the agent answers)")
	}
	if len(c.UserTurns) > MaxChatUserTurns {
		return fmt.Errorf("chat: %d scripted user turns, at most %d", len(c.UserTurns), MaxChatUserTurns)
	}
	for i, t := range c.UserTurns {
		if strings.TrimSpace(t) == "" {
			return fmt.Errorf("chat: scripted user turn %d is empty", i+1)
		}
	}
	switch c.GradeOn {
	case "", ChatGradeFinal, ChatGradeTranscript:
	default:
		return fmt.Errorf("chat: grade_on %q: want final or transcript", c.GradeOn)
	}
	if len(c.Attachments) > MaxChatAttachments {
		return fmt.Errorf("chat: %d attachments, at most %d", len(c.Attachments), MaxChatAttachments)
	}
	for i, a := range c.Attachments {
		if a.Name == "" {
			return fmt.Errorf("chat: attachment %d has no name", i+1)
		}
		if (a.Path == "") == (a.URL == "") {
			return fmt.Errorf("chat: attachment %s needs exactly one of path or url", a.Name)
		}
		if a.Path != "" {
			clean := filepath.ToSlash(filepath.Clean(a.Path))
			if clean != a.Path || !strings.HasPrefix(clean, "attachments/") || strings.Contains(clean, "..") {
				return fmt.Errorf("chat: attachment %s: path must be attachments/<name>", a.Name)
			}
		}
		if a.URL != "" && !strings.HasPrefix(a.URL, "https://") && !strings.HasPrefix(a.URL, "http://") {
			return fmt.Errorf("chat: attachment %s: url must be http or https", a.Name)
		}
	}
	return nil
}

// ReadChatFile reads and validates the chat file of t.
func ReadChatFile(t *Task) (*ChatFile, error) {
	if t == nil || t.Chat == nil || t.Chat.File == "" {
		return nil, errors.New("the task has no chat file")
	}
	p := t.Chat.File
	if !filepath.IsAbs(p) {
		p = filepath.Join(t.Dir, p)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("task %s: read %s: %w", t.ID, t.Chat.File, err)
	}
	var c ChatFile
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("task %s: %s: %w", t.ID, t.Chat.File, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("task %s: %w", t.ID, err)
	}
	if c.GradeOn == "" {
		c.GradeOn = t.Chat.GradeOn
	}
	return &c, nil
}

// RenderChat is a conversation as plain text, each turn labelled with its
// role: what an agent that reads one instruction gets, and the transcript a
// verifier grades when a chat task is graded on the whole conversation.
func RenderChat(msgs []ChatMessage) string {
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(strings.TrimSpace(m.Content))
	}
	b.WriteString("\n")
	return b.String()
}
