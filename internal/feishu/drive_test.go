package feishu

import (
	"strings"
	"testing"
)

// The screens of one OpenCode pane before and after a turn, as it drew
// them: a viewport of a screen of its own, no rows longer than before.
const (
	viewportBefore = `  ┃  hi
  ┃

     ┃ Thought: 528ms

     ┃ The user keeps saying "hi" — sixth time now. This is almost certainly a connectivity/responsiveness test, not a real conversation
     ┃ opener. The right response is minimal acknowledgment. I already noted the new rules last time; no need to repeat that. Just a
     ┃ super brief reply.

     你好！我在线，随时可以开始。

     Build · GLM-5.3-Flash · glm · 10.7s · 6.4 tok/s

     + Tokens: 1 step · latest: 201 new · 40,704 cached · 40,905 total

  ┃
  ┃  1+1=？
  ┃

     ┃ Thought: 235ms

     ┃ The user asks "1+1=?" — a simple arithmetic question. The answer is 2. Keep it brief and playful, no need for elaboration.

     1+1=2 😄 有正事随时安排～

     Build · GLM-5.3-Flash · glm · 8.6s · 5.6 tok/s

     + Tokens: 1 step · latest: 256 new · 40,704 cached · 40,960 total

  ┃
  ┃
  ┃
  ┃  Build auto · GLM-5.3-Flash · glm magpie
  ╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
  ~/presence                                                                                          41.0K (4%) · $0.01  ctrl+p commands`
	viewportAfter = `  ┃  1+1=？
  ┃

     ┃ Thought: 235ms

     ┃ The user asks "1+1=?" — a simple arithmetic question. The answer is 2. Keep it brief and playful, no need for elaboration.

     1+1=2 😄 有正事随时安排～

     Build · GLM-5.3-Flash · glm · 8.6s · 5.6 tok/s

     + Tokens: 1 step · latest: 256 new · 40,704 cached · 40,960 total

  ┃
  ┃  2+2=?
  ┃

     ┃ Thought: 295ms

     ┃ The user is asking "2+2=?" — another trivial arithmetic question, following the pattern of "1+1=?" They seem to be testing
     ┃ responsiveness or just playing around. The answer is simply 4. Keep the reply light and brief, matching the tone, and maybe gently
     ┃ nudge toward real work since this is the second such question.

     2+2=4 😄 测试完毕的话，随时开工～

     Build · GLM-5.3-Flash · glm · 12.4s · 7.1 tok/s

     + Tokens: 1 step · latest: 350 new · 40,704 cached · 41,054 total

  ┃
  ┃
  ┃
  ┃  Build auto · GLM-5.3-Flash · glm magpie
  ╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
  ~/presence                                                                                          41.1K (4%) · $0.02  ctrl+p commands`
)

// A pane that draws its viewport anew, as a TUI does, keeps a turn's text
// in the rows below the prompt, which the rows of the screen never grow
// for.
func TestTurnTailOfAViewport(t *testing.T) {
	got := turnTail(viewportBefore, viewportAfter, "2+2=?")
	if !strings.Contains(got, "2+2=4") {
		t.Fatalf("the turn's text is %q", got)
	}
	for _, away := range []string{"ctrl+p commands", "Build auto", "+ Tokens:", "Build ·"} {
		if strings.Contains(got, away) {
			t.Errorf("the turn's text holds %q: %q", away, got)
		}
	}
	if got := turnTail(viewportBefore, viewportAfter, ""); got != "" {
		t.Errorf("a turn with no prompt took rows of the screen: %q", got)
	}
	// A screen that grew has its turn in the rows after its start, which
	// since tells, and no prompt to find.
	grown := viewportBefore + "\n  ┃\n  ┃  2+2=?\n  ┃\n\n  2+2=4\n"
	if got := turnTail(viewportBefore, grown, "2+2=?"); got != "" {
		t.Errorf("a screen that grew took the prompt's rows: %q", got)
	}
}
