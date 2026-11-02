---
title: 飞书
description: 在飞书的私聊或群聊中驱动 Kero 会话里的编程智能体。
status: draft
---

# 飞书

飞书支持把一台机器上的 kerod 连接到一个飞书应用。你给机器人发消息，或在群里 @ 它，Kero 会话中的编程智能体就会收到消息。回答以卡片的形式回到聊天中。会话始终在 Kero 侧边栏里，所以对话在飞书中继续时，你仍可以查看它，或从终端接管。

这个想法来自 [MetaBot](https://github.com/xvirobotics/metabot)，它把飞书等聊天渠道接到编程智能体上。Kero 的做法不同。MetaBot 运行自己的引擎。Kero **使用已有的会话**：它的进程、智能体识别、对话历史、状态，以及对其他主机的访问。

## 架构

```text
Feishu Open Platform
   |  a long-lived WebSocket (im.message.receive_v1 events)
   v
kerod -- internal/feishu
   |-- IM adapter      messages  <->  commands, reply cards
   |-- Bridge          the binding of a chat to a session, the replies to Feishu
   |-- Prompt host     the prompts in the session's pane
   +-- existing code   detect / resume / pane.report / notify
   ^
   |  the same RPC
Kero app (the session is visible, and can be taken over)
kero CLI
other hosts
```

机器人保持一条到飞书的长连接 WebSocket。飞书称之为"长连接"。短连接需要一个公网 HTTPS 地址来接收 webhook。长连接不需要开放端口。

### 为什么放在 kerod 里

| 选择 | 得到什么 | 失去什么 |
| --- | --- | --- |
| 单独的程序，如 MetaBot | 功能已就绪 | 第二套运行时和第二套会话；无法驱动或接管 Kero 的窗格 |
| 放在 kerod 里 | 智能体识别、对话恢复、状态、主机，以及单个二进制；能在应用里看到聊天的工作 | kerod 里多一些代码 |
| 放在应用中 | 应用退出时机器人随之停止 | 应用要在后台保存 App Secret，且 kerod 关闭时没有帮助 |

无论如何，提示都经由 kerod 进入窗格。以后可以把 IM 适配层移到应用里，通过应用已有的 kerod 连接进入窗格。kerod 不变。

## 飞书中的命令

| 命令 | 作用 |
| --- | --- |
| `/new [项目] [智能体]` | 在项目中新建会话，在窗格里运行智能体，并把聊天绑定到它 |
| `/use <会话>` | 按名称或 ID 前缀，把聊天绑定到一个已打开的会话 |
| `/ls` | 列出已打开的会话，每个带一个表示状态的点 |
| `/status` | 显示绑定会话的状态、分支、改动和目录 |
| `/stop` | 中断当前这一轮（Ctrl-C） |
| `/answer <文字>` | 回答智能体提出的问题 |
| `/close` | 结束绑定的会话，需要确认 |

不是命令的文字作为提示发给绑定的会话。在群里，只有 @ 了机器人的消息才会这样做。群里的每个话题（thread）各有自己的对话。

### 命令模式

设置 `feishu-run = command` 后，kerod 为每个提示运行智能体自己的命令，不需要终端：

```text
claude -p --output-format text --session-id <id> "<prompt>"    # Claude Code
pi -p --session-id <id> "<prompt>"                              # Pi
gemini -p "<prompt>"                                            # Gemini CLI
opencode run "<prompt>"
codex exec "<prompt>"
```

每条命令在聊天的会话中运行，该会话没有终端和 shell：命令运行时侧边栏显示工作中，结束后显示空闲。它在 `feishu-dir` 指定的目录中运行，环境与窗格相同。命令的输出写到 kero 目录下 `feishu-run` 中的文件，其文本就是回复。聊天会继续这段对话。第一轮用 kerod 生成的 ID 开始会话，之后的轮次继续它：

| 智能体 | 第一轮 | 之后的轮次 |
| --- | --- | --- |
| Claude Code | `--session-id <id>` | `--resume <id>`，或结果中给出的 ID |
| Pi | `--session-id <id>` | `--session-id <id>` |
| Gemini | `--session-id <id>` | `--resume <id>` |
| OpenCode | `run "<prompt>"` | `run --session <id>`；输出中给出会话 |
| Codex | `exec "<prompt>"` | `exec resume <id> --skip-git-repo-check`；输出中给出会话 |

`/new <项目> <智能体>` 用另一个智能体开始新对话。

回复是命令的输出。对 Claude Code，在 `feishu-agent-command` 中使用 `--output-format stream-json` 的命令给出的是消息的文本，而不是整个流。

后台命令没有窗格输入：在这类会话中 `/answer` 会说明这一点。

### 会话模式

机器人把提示输入窗格，按回车，并跟踪窗格直到这一轮结束：智能体等待用户，或工作后屏幕和状态保持不动。它也能结束一轮状态始终不说"工作中"的对话：

- 如果屏幕和发提示前一样，也没有出现回答，机器人回复"Kero 无法区分这一轮的文本和之前的文本"。
- 如果窗格保持"工作中"超过 5 秒，又没有工作迹象，机器人认为这一轮已结束。

`feishu-timeout` 之后智能体仍在工作，回复会说明这一点。

### 每一轮的回答

终端屏幕不是干净的回答：它包含 TUI 的框架和状态栏。对 Claude Code，`kero skill setup` 安装的 `Stop` 钩子（`kero answer claude`）在每轮结束时把智能体的最后一条消息写到机器人交给窗格的文件中（kerod 目录下的 `feishu-answers`）。机器人用这条消息作回复，而不是屏幕上的文字。其他智能体没有可用的 Stop 钩子，机器人使用终端文本。

不给机器人交给窗格文件时，`kero answer` 不做任何事。在 Kero 之外，或在任何错误下，它也不做任何事：它从不让智能体失败。

## 对话视图

对话显示在 session 的窗格里，替代它的终端。点击会话菜单中的 **对话**，或窗格标题栏上的对话按钮，即可打开。窗格会单独显示，让对话有足够的空间。标题栏上的按钮可切回终端。对话显示该 session 所在的飞书会话：

- 用户的提问显示在右侧；
- 智能体运行的工具显示为一行浅色文字；
- 智能体的消息显示在左侧，本轮的回答带底色。

对话打开时，以及主机状态每次变化时，session 所在的主机通过 `feishu.feed` 读取对话。顶部显示会话名称；点击搜索按钮可以列出机器人所在的所有会话，选中后在窗格中显示那个会话。

在底部的输入框中输入内容并按回车，即可作为提示词发送给智能体。文字会粘贴到当前窗格的终端里，随后单独发送回车，和飞书消息驱动窗格的方式相同。这条内容不会发到飞书。

## 设置

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
feishu-run = command            # how a prompt runs its agent: command, in the background; or session, in a pane
feishu-agent = claude           # the agent for new sessions and commands; default uses the new session's program
feishu-dir = ~/code/repo        # where new sessions start when you name no project

# One line for each agent, by the name the detector uses
feishu-agent-command = claude=claude --permission-mode acceptEdits
feishu-agent-command = codex=codex --full-auto
feishu-agent-command = pi=pi
```

| 键 | 默认值 | 说明 |
| --- | --- | --- |
| `feishu-app` | 空 | 飞书应用的 App ID。为空则关闭飞书。 |
| `feishu-allow` | 空 | 谁可以发提示：`open_id`、`user_id` 或 `email`。为空时只允许运行 `kero feishu setup` 的用户。 |
| `feishu-timeout` | `30m` | 一轮等待的最长时间，超过后卡片说明已超时。 |
| `feishu-stream` | `2s` | 工作中的卡片多久更新一次文本。 |
| `feishu-run` | `session` | 提示如何运行智能体：`command`，在后台运行智能体自己的命令；或 `session`，在会话的窗格中运行，和用户的轮次一样。 |
| `feishu-agent` | `default` | `/new` 会话运行的智能体：`default`，或识别器认识的名字，如 `claude`、`codex`、`opencode`、`pi`。 |
| `feishu-dir` | 空 | 消息未指明项目时，新会话开始的目录。为空时使用 `new-session-directory`。 |
| `feishu-agent-command` | 空 | `名称=命令`，按智能体名称：运行该智能体的命令。未列出的智能体运行其程序，不带参数。命令可以以 `~/` 开头。 |

### 智能体的运行环境

kerod 用自身的环境启动任务，而这个环境来自 launchd 的任务。PATH 先是你登录 shell 的 PATH，再加上智能体通常安装的位置（`~/.local/bin`、`~/.bun/bin`、`~/.cargo/bin`、nvm 和 fnm 的 Node、`/opt/homebrew/bin`、`/usr/local/bin`）。登录 shell 的 PATH 最多每分钟重新读取一次。如果找不到某个智能体，就用 `feishu-agent-command` 指定它的命令。

### App Secret

App Secret 不在配置文件中。配置文件常被复制、备份并同步到 dotfiles，而 App Secret 是长期凭证。kerod 把它保存在 `kerod.lock` 旁边的文件里，只有你的账户可读，由 `kero feishu setup` 写入：

```sh
kero feishu setup     # asks for the App ID and App Secret, connects, and checks the app's settings
kero feishu status    # whether the bot is connected
```

`kero feishu setup` 逐步检查飞书应用：机器人能力、`im:message` 和 `im:resource` 权限、`im.message.receive` 事件，以及已发布的版本。某一步失败时，它指出这一步，以及在[飞书开放平台控制台](https://open.feishu.cn/app)中要改什么。

设置窗口显示应用名、连接状态，以及谁可以使用。它从不显示 App Secret。信息视图（⌥⌘I）在 `feishu-app` 指定了应用时也显示一张飞书卡片：App ID、机器人是否已连接、谁可以使用。

## 与 Kero 的配合

- **智能体识别**告诉桥，智能体在工作、等待回答，还是空闲。
- **对话恢复**使 kerod 重启时不丢失任何东西：会话和智能体对话像其他会话一样恢复。
- **文件窗格**打开智能体修改的文件，和任何会话一样。
- **主机**与 `-H` 配合使用：飞书中的命令作用于绑定会话所在的主机。

## 多个主机与群聊

- 默认情况下，一个聊天的事件只发到一台机器：聊天绑定的那台。
- 多台机器连接同一个应用时，`feishu-host` 指定聊天消息发往的主机，`/host` 在聊天中修改它。
- 群消息只有 @ 机器人时才生效。回复发到对话的话题中，而不是整个群。

## 安全

- **飞书消息就是你的 shell 输入。** 智能体以你账户的权限运行，和在窗格里输入时一样。`feishu-allow` 限制谁能发送。除非你添加其他人，它只允许运行 `kero feishu setup` 的账户。`*` 允许任何能找到机器人的人。
- 桥忽略机器人自己发的消息、其他应用的消息（`app_id` 必须匹配），以及重复事件（按 `event_id`）。
- 卡片按钮只对聊天的主人，或 `feishu-allow` 列出的人生效。
- 日志和 `kero clients` 从不显示 App Secret。

## 依赖

| 需要 | 选择 |
| --- | --- |
| 飞书 API、长连接、事件分发 | 官方 Go SDK，`github.com/larksuite/oapi-sdk-go/v3` |
| 卡片 | 适配层构建的 JSON；长文本用 `collapsible_panel` |
| 卡片按钮事件 | 后续阶段；目前使用文字命令 |

## 阶段

| 阶段 | 内容 |
| --- | --- |
| **M1：连接** | `feishu-*` 设置；`kero feishu setup`、`status`、`test`、`send`；长连接；`feishu-allow`；`/new`、`/use`、`/ls`、`/status`、`/stop`、`/answer`、`/close`；通过 @ 的群聊；发到绑定会话的文字；带这一轮输出的文字回复 |
| **M2：协议** | 等待空闲、等待或超时；更新的卡片；工作中、等待和错误的颜色 |
| **M3：体验** | 带 @ 和话题的群聊；复制到会话中的图片和文件；按钮，以及在 Kero 中打开 |
| **M4：每个智能体** | 每轮的回答写到文件：Claude Code 的 `Stop` 钩子（`kero answer`）；没有文件时用终端文本 |
| **M5：多台机器** | 一个应用的事件如何发到多台机器；`/host` |
| **M6：收尾** | 流式卡片；Markdown；设置 › 智能体 › 飞书；本文的日文和中文版本 |

## 风险

1. **终端屏幕不是干净的回答。** 它包含 TUI 的框架和状态栏。每轮的回答文件是主要的解决办法。终端文本是没有钩子的智能体的后备。
2. **粘贴。** 粘贴的多行提示不应逐行按回车。前缀必须使用括号粘贴，提示工具已经这样做了。
3. **飞书应用的设置需要你自己完成。** 设置检查必须准确指出缺什么，否则第一次连接会无原因地失败。
