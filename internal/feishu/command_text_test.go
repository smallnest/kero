package feishu

import "testing"

// A Claude Code answer drops the agent's own reports to the terminal,
// which its JSON's lines do not hold, and its warning of no input.
func TestCommandTextClaude(t *testing.T) {
	raw := `[claude-code:unrecognized_model] {"model":"group/smart-split[1m]","query_source":"sdk"}
Warning: no stdin data received in 3s, proceeding without it. If piping from a slow command, redirect stdin explicitly: < /dev/null to skip, or wait longer.
{"type":"result","result":"Hi! What do you need help with?","session_id":"x"}
`
	if got := commandText("claude", raw); got != "Hi! What do you need help with?" {
		t.Errorf("the answer %q", got)
	}
}
