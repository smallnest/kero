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

// A Codex turn's events give its messages as the answer, and name the
// session a chat continues.
func TestCommandTextCodex(t *testing.T) {
	raw := `{"type":"thread.started","thread_id":"0199-th-1"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"reasoning","text":"thinking"}}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"Hello."}}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"What next?"}}
{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":2}}
`
	if got := commandText("codex", raw); got != "Hello.\n\nWhat next?" {
		t.Errorf("the answer %q", got)
	}
	if sid := conversation("codex", raw, "x"); sid != "0199-th-1" {
		t.Errorf("codex continues %q", sid)
	}
	// A turn that failed says why, rather than its events.
	failed := `{"type":"turn.started"}
{"type":"turn.failed","error":{"message":"invalid session id"}}
`
	if got := commandText("codex", failed); got != "invalid session id" {
		t.Errorf("the failure %q", got)
	}
}
