---
title: Feishu
description: Drive the coding agents in Kero's sessions from Feishu or Lark, in a private chat or a group.
status: draft
---

# Feishu

::: warning Draft
This page says what Feishu support does and how it fits into Kero. Stages M1 to M6 are built. The Stages table lists what each stage added.
:::

Feishu support connects one Feishu app to a machine's kerod. You send a message to the bot, or @-mention it in a group, and the coding agent in a Kero session receives it. The answer comes back as a card in the chat. The session stays in Kero's sidebar, so you can look at it, or take over from the terminal, while the conversation continues in Feishu.

The idea comes from [MetaBot](https://github.com/xvirobotics/metabot), which bridges Feishu and other chat channels to coding agents. Kero does it another way. MetaBot runs its own engines. Kero **uses the sessions it already has**: its processes, agent detection, conversation history, status, and access to other hosts.

## Architecture

```text
Feishu Open Platform
   |  a long-lived WebSocket (im.message.receive_v1 events)
   v
kerod -- internal/feishu
   |-- IM adapter      messages  <->  commands, reply cards
   |-- Bridge          the binding of a chat to a session, the replies to Feishu
   |-- Prompt host     the pane.prompt RPC methods
   +-- existing code   detect / resume / pane.report / notify
   ^
   |  the same RPC
Kero app (the session is visible, and can be taken over)
kero CLI
other hosts
```

The bot holds a long-lived WebSocket to Feishu. Feishu calls this "persistent connection". A short connection needs a public HTTPS endpoint for webhooks. The long connection needs no open port.

### Why kerod hosts it

| Choice | What you get | What you lose |
| --- | --- | --- |
| A separate program, as MetaBot does | Its features are ready | A second runtime and a second set of sessions; it cannot drive or take over Kero's panes |
| In kerod, as designed here | The agent detector, conversation resume, status, hosts, and one binary; you see the chat's work in the app | Some code in kerod |
| In the app | The bot stops when the app quits | The app keeps an App Secret in the background, and has no help from kerod when it is closed |

`pane.prompt` belongs in kerod in every case. A later choice can move the IM adapter to the app, which calls `pane.prompt` through the connection to kerod that it already has. kerod stays the same.

## Commands in Feishu

| Command | What it does |
| --- | --- |
| `/new [project] [agent]` | Starts a session in the project, runs the agent in its pane, and binds the chat to it |
| `/use <session>` | Binds the chat to an open session, by name or id prefix |
| `/ls` | Lists the open sessions, with a dot for the state of each |
| `/status` | Shows the state, branch, changes and directory of the bound session |
| `/stop` | Interrupts the current turn (Ctrl-C) |
| `/answer <text>` | Answers a question the agent asks |
| `/close` | Ends the bound session, after a confirmation |

Text that is not a command goes to the bound session as a prompt. In a group, only messages that @-mention the bot do this. Each group conversation gets its own thread, or "topic".

One prompt runs in a chat at a time. A prompt that comes while a turn runs waits its turn, one deep, and runs as that turn ends; a command does not wait, so `/stop` reaches a turn that runs. A prompt beyond the one that waits is refused. `/stop` drops the prompt that waits, with the turn it waited for.

### Command mode

With `feishu-run = command`, kerod runs the agent's own command for each prompt, without a terminal:

```text
claude -p --output-format text --session-id <id> "<prompt>"    # Claude Code
pi -p --session-id <id> "<prompt>"                              # Pi
gemini -p "<prompt>"                                            # Gemini CLI
opencode run "<prompt>"
codex exec --json "<prompt>"
```

Each command runs in the chat's session, which has no terminal and no shell: it shows in the sidebar as working while the command runs, and idle once it ends. It runs in the directory `feishu-dir` names, under the daemon's environment, as a session's pane is. The command's output goes to a file in kero's directory, under `feishu-run`, and its text is the reply. The chat continues the conversation. The first turn starts a session with an ID kerod makes, and the next turns continue it:

| Agent | First turn | Next turns |
| --- | --- | --- |
| Claude Code | `--session-id <id>` | `--resume <id>`, or the ID its result names |
| Pi | `--session-id <id>` | `--session-id <id>` |
| Gemini | `--session-id <id>` | `--resume <id>` |
| OpenCode | `run "<prompt>"` | `run --session <id>`; the output names the session |
| Codex | `exec --json "<prompt>"` | `exec --json resume <id> --skip-git-repo-check`; its events name the session | `/new <project> <agent>` starts a new conversation with another agent.

The reply is the command's output. For Claude Code, a command that uses `--output-format stream-json` (in `feishu-agent-command`) gives the text of its messages, not the whole stream. For Codex, its events name the session, and their messages are the reply.

A background command has no pane input: `/answer` in such a session says so.

### Permissions of a turn

A Claude Code turn in command mode runs with the stream of its messages. When the turn needs a permission for a tool, Claude Code asks for it on that stream, and kerod puts the ask in a file beside the turn's output. The bot sends the chat a card with the tool and the command, file, or address that it would use. The card has three buttons:

| Button | Effect |
| --- | --- |
| Allow once | The turn runs the tool this time only. |
| Always allow | The turn runs the tool, and the chat's next turns do not ask for that tool. |
| Refuse | The turn does not run the tool. A reason under the card's heading goes to the agent. |

The turn waits until you answer. A turn that times out (`feishu-timeout`) or ends without an answer refuses the rest of its permissions.

The other agents in command mode run their tools without a permission prompt.

### Session mode

The bot types the prompt in the pane, presses Enter, and follows the pane until the turn ends: the agent waits for the user, or its screen and status are quiet after it worked. It can also end a turn whose agent's status never says that it works, once its screen is still:

- If the screen is as it was before the prompt, and no answer appears, the bot replies "Kero cannot tell the turn's text from what came before."
- If the pane keeps "working" for more than 5 s, and no sign of work shows, the bot takes the turn as over.

A turn whose agent still works after `feishu-timeout` gets a reply that says so.

### The answer of each turn

The terminal screen is not a clean answer: it holds the TUI's frames and status bars. For Claude Code, the `Stop` hook that `kero skill setup` installs (`kero answer claude`) writes the agent's final message to the file that the bot gives the pane, under `feishu-answers` in Kero's directory, as each turn ends. The bot replies with that message, not with the screen's text. The other agents have no Stop hook the bot can use, so the bot uses the terminal's text.

Without the file that the bot gives the pane, `kero answer` does nothing. Outside Kero, and on any error, it does nothing as well: it never fails the agent.

## The Chat view

The chat shows in a session's pane, in place of its terminal. To open it, click **Chat** on the session's menu, or the chat button in the pane's header. The pane shows alone, so that the chat has its place. The button in the header turns the pane back to its terminal. The chat shows the chat that the session is in:

- the prompts that the user wrote, to the right, on the accent's bubbles;
- the agent's thoughts, in faint text;
- the tools that the agent ran, and their output, each on a faint line: click a tool to open its arguments and its full output;
- the agent's messages, to the left, with the turn's answer on a stronger bubble.

The messages are Markdown. The chat shows headings, lists, quotes, tables, code blocks, and links; a link opens in the browser.

The conversation reads in a column of a fixed width, in the middle of the pane. The column gets smaller with a narrow pane.

The view only shows the conversation. It has no field: write your prompts in Feishu. The session's host reads the turns with `feishu.feed` as the chat opens, and again each time that the host's state changes.

In Feishu, each turn shows on the user's message and in an answer (`feishu-cards`). When the turn starts, the message takes the `OnIt` reaction, and while the agent works, it takes a plain, grey card. When the turn ends, the card becomes one short line, and the reaction goes. The answer is a card that follows the message: its text is the answer as Markdown. Its footer is a small line: the state in a colored tag (Done in green, Waiting for you in orange, Error in red), then the model, the usage (↑ input, ↓ output, cache, and ctx used of the limit in percent) and the time the turn took, as `1m24s`. The card does not show a value that it does not know. Only the Claude Code command mode reports the usage and the time.

### Questions with options

When the agent asks you to choose, the turn's card shows the question, and each option is a button. A field under the buttons takes an answer of your own. The bot shows the options for a list of two or more options at the end of the agent's text, where the question before it ends in `:` or `?`. A list counts as options when each line starts with a number (`1.`, `2)`), a letter (`A.`, `B)`), or a bracket (`(Red)`, `（Red）`). Lists of steps, such as "1. Install, 2. Run", are not questions.

When you take a button, the bot sends the question and the option's text to the agent, and the card shows the answer. Other answers work in the same way: the bot sends the question and the text that you write. A question accepts one answer. The agent continues the turn from your answer, and its next answer is a new card.

Only cards show the buttons. With `feishu-cards = false`, answer the agent with `/answer`, or with a message.

## Settings

```ini
# ~/.config/kero/config

# The same Feishu app may connect from more than one machine. By
# default, only the machine the chat is bound to receives its events.

feishu-app = bot:cli_a1b2c3d4   # the app's App ID; every machine writes the same name
feishu-allow = *                # who may use it: open_id, user_id or email; repeat it

# All machines
feishu-timeout = 30m            # the longest a turn waits
feishu-stream = 2s              # how often a card updates

# While the bot is connected, kerod stays even with no session open:
# sessions started from Feishu restore after a kerod restart.

# This machine
feishu-run = command            # how a prompt runs its agent: command, in the background (claude -p); or session, in a pane
feishu-agent = claude           # the agent for new sessions and commands; default uses the new session's program
feishu-dir = ~/code/repo        # where new sessions start when you name no project

# One line for each agent, by the name the detector uses
feishu-agent-command = claude=claude --permission-mode acceptEdits
feishu-agent-command = codex=codex --full-auto
feishu-agent-command = pi=pi
```

| Key | Default | Description |
| --- | --- | --- |
| `feishu-app` | empty | The App ID of the Feishu app. Empty turns Feishu off. |
| `feishu-allow` | empty | Who may send prompts: `open_id`, `user_id` or `email` values. Empty allows only the user who ran `kero feishu setup`. |
| `feishu-timeout` | `30m` | The longest a turn waits before its card says it timed out. |
| `feishu-stream` | `2s` | How often a working card takes the new text. |
| `feishu-cards` | `true` | A turn shows on the user's message, with a reaction while it runs, and every answer is a card whose text is Markdown. `false` sends the answers as messages, and shows no card or reaction. |
| `feishu-run` | `session` | How a prompt runs its agent: `command`, as the agent's own command in the background, which tells the reply and no terminal shows; or `session`, in the session's pane, as a user's turn. |
| `feishu-agent` | `default` | The agent a `/new` session runs: `default`, or a name the detector knows, such as `claude`, `codex`, `opencode` or `pi`. |
| `feishu-dir` | empty | The directory new sessions start in when the message names no project. Empty uses `new-session-directory`. |
| `feishu-yolo` | `true` | Run every agent in its yolo mode: no permission prompts, and, where the agent has one, no sandbox. Claude Code runs with `--dangerously-skip-permissions` alone, and asks the chat for no permission: its `--permission-prompt-tool` makes each tool ask the program that runs it, and so fails it where that program sends nothing. The others' option is Codex's `--dangerously-bypass-approvals-and-sandbox`, and `--yolo` or `--auto` for the others' programs. `claude=false` turns one agent's off, and `*=true` or `*=false` sets it for every agent. An option in `feishu-agent-command` sets its agent's. |
| `feishu-agent-command` | empty | `name=command`, by the agent's name: the command that runs the agent. An agent it leaves out runs its program with no arguments. A command can start with `~/`. |

### The agents' environment

kerod starts the app's jobs with its own environment, which a launchd job makes. Its PATH is the account's login shell's PATH, then the places that agents install to (`~/.local/bin`, `~/.bun/bin`, `~/.cargo/bin`, nvm's and fnm's Node, `/opt/homebrew/bin` and `/usr/local/bin`). A login shell's PATH is read again at most once a minute. If an agent is not found, name its command in `feishu-agent-command`.

### The App Secret

The App Secret is not in the configuration file. The configuration file is often copied, backed up and synchronized to dotfiles, and the App Secret is a long-lived credential. kerod keeps it in a file next to `kerod.lock`, readable by your account only, and `kero feishu setup` writes it:

```sh
kero feishu setup     # asks for the App ID and App Secret, connects, and checks the app's settings
kero feishu status    # whether the bot is connected
```

`kero feishu setup` checks the Feishu app, step by step, for the bot capability, the `im:message` and `im:resource` scopes, the `im.message.receive` event, and a published version. If a step fails, it names the step and what to change in the [Feishu Open Platform console](https://open.feishu.cn/app).

The Settings window shows the app's name, the connection state, and who may use it. It never shows the App Secret. Settings › Feishu shows the app, the connection, who may use it, the run mode, the agent, the directory, and the yolo modes. The Info view (⌥⌘I) also shows a Feishu card while `feishu-app` names an app: the App ID, whether the bot is connected, and who may use it.

## How it fits into Kero

- **The agent detector** tells the bridge when the agent is working, waits for an answer, or is idle.
- **Conversation resume** loses nothing when kerod restarts: the session and its agent conversation come back as the other sessions do.
- **File panes** open the files an agent changes, as in any session.
- **Hosts** work with `-H`: the commands in Feishu act on the host of the bound session.

## Multi-host and groups

- By default, one machine receives the events of a chat: the machine the chat is bound to.
- When several machines connect to one app, `feishu-host` names the host that the chat's messages go to, and `/host` changes it in the chat.
- A group message acts only when it @-mentions the bot. A reply goes to the thread of the conversation, not to the group at large.

## Security

- **A Feishu message is input in your shell.** The agent runs with your account's permissions, as it does when you type in a pane. `feishu-allow` limits who can send. It allows only the account that ran `kero feishu setup` unless you add others. `*` allows anyone who can reach the bot.
- The bridge ignores messages that the bot itself sends, messages from other apps (`app_id` must match), and duplicate events, by `event_id`.
- A card button acts only for the person who owns the chat, or who `feishu-allow` names.
- The `answer` hook writes only in the directory that the bot gives its pane.
- Logs and `kero clients` never show the App Secret.

## Dependencies

| Need | Choice |
| --- | --- |
| The Feishu API, the long connection, event dispatch | The official Go SDK, `github.com/larksuite/oapi-sdk-go/v3` |
| Cards | JSON that the adapter builds; `collapsible_panel` for long text |
| Card button events | A later stage; M1 uses text commands |

## Stages

| Stage | Content | What shows it works |
| --- | --- | --- |
| **M1: connect** | The `feishu-*` keys; `kero feishu setup`, `status`, `test` and `send`; the long connection; `feishu-allow`; `/new`, `/use`, `/ls`, `/status`, `/stop`, `/answer`, `/close`; groups by @-mention; text to the bound session; a text reply with what the turn printed | Ask a question in a private chat: the agent works in a Kero pane, and Feishu shows the result |
| **M2: the protocol** | `pane.prompt` and `pane.prompt.preview`; waiting for idle, waiting or a timeout; cards that update; colors for working, waiting and error | A long answer appears as it is written; the card turns yellow when the agent asks |
| **M3: the experience** | Groups with @-mentions and threads; images and files copied into the session; `/stop`, `/answer`, `/status`, `/close`; buttons, and Open in Kero | A group uses the same session; a button opens it in the app |
| **M4: every agent** | An instruction that has each turn's answer written to a file, with hooks for claude, codex, pi and opencode; the terminal text when there is no file | All four agents work, with a script that runs each |
| **M5: many machines** | How one app's events go to several machines; `/host` | Two machines, each with a bot |
| **M6: finish** | Streaming cards; Markdown; a Settings › Feishu; this page in Japanese and Chinese | The tests and a manual checklist pass |

## Risks

1. **The terminal screen is not a clean answer.** It holds the TUI's frames and status bars. M4's answer file is the main fix. The terminal text is the fallback for agents with no hook. M1 uses it, which is enough to see that the link works.
2. **Paste.** A pasted multi-line prompt must not press Return on each line. The prefix must use bracketed paste, which prompts use already.
3. **The Feishu app's settings are yours to make.** This page's setup check must name exactly what is missing, or the first connection fails without a reason.
