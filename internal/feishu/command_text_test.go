package feishu

import "testing"

// A Claude Code answer drops the agent's own reports to the terminal,
// which its JSON's lines do not hold.
func TestCommandTextClaude(t *testing.T) {
	raw := `[claude-code:unrecognized_model] {"model":"group/smart-split[1m]","query_source":"sdk"}
{"type":"result","result":"Hi! What do you need help with?","session_id":"x"}
`
	if got := commandText("claude", raw); got != "Hi! What do you need help with?" {
		t.Errorf("the answer %q", got)
	}
}
