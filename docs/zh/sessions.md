---
title: 会话与项目
description: Kero 如何按项目组织终端会话，并让它们在 kerod 中持续运行。
---

# 会话与项目

Kero 将你的工作分为两层：项目（文件夹）包含会话（任务），每个会话都在 kerod 中持续运行，直到你关闭它。

## 会话

**会话**是一个工作单元：一项任务、一个智能体、一个长期运行的进程。它包含一个或多个[窗格](./panes.md)，每个窗格运行一个 shell 或一个程序。

| 操作                       | 快捷键                                       |
| -------------------------- | -------------------------------------------- |
| 新建会话                   | <kbd>⌘</kbd> <kbd>T</kbd>                    |
| 在主目录新建会话           | <kbd>⌘</kbd> <kbd>⇧</kbd> <kbd>T</kbd>       |
| 前往会话…                  | <kbd>⌘</kbd> <kbd>P</kbd>                    |
| 跳到需要你的会话           | <kbd>⌘</kbd> <kbd>J</kbd>                    |
| 下一个 / 上一个会话        | <kbd>⌘</kbd> <kbd>⇧</kbd> <kbd>]</kbd> / <kbd>[</kbd> |
| 重命名会话                 | <kbd>⌘</kbd> <kbd>R</kbd>                    |
| 关闭会话                   | <kbd>⌘</kbd> <kbd>⇧</kbd> <kbd>W</kbd>       |

按任务给会话命名，比如 `fix-login-redirect` 或 `review-pr-42`，这样就能在边栏中找到它们，也能在[命令行](./cli.md)中指定它们。

### 新会话从哪里开始

`new-session-directory` 设置决定新会话所在的文件夹：

- `current`（默认）：你当前所在会话的目录
- `project`：项目的根目录
- `home`：你的主目录

<a id="how-an-unnamed-session-is-named"></a>
### 未命名的会话如何命名

在你或智能体为会话命名之前，`session-name` 设置（设置中的 **会话名称取自**）决定边栏显示什么：

- `focused`（默认）：你所在窗格中程序的标题。如果该窗格只运行着 shell，则改用最需要你处理的那个窗格。
- `lead`：最需要你处理的窗格中程序的标题，比如正在工作或在等你的智能体
- `folder`：第一个窗格所在的文件夹，不论其中运行着什么

### 已结束的会话

在“设置 › 通用 › **会话历史**”中开启，或设置 `session-history = true`。

当 Claude Code、Codex、Pi 或 OpenCode 退出，或者你关闭它的窗格或会话时，Kero 会把它的对话保留在侧边栏底部的**已结束**部分，以便之后继续。点击标题即可展开：

- 已结束按从新到旧列出智能体会话，显示智能体、名称、文件夹以及结束了多久：先显示最新的 5 个，然后是**显示全部**。Kero 保留最近 50 个。其他主机的会话也在其中，带有主机名标签。
- 点击其中一个，会在它的文件夹中打开新会话并恢复对话，就像 `claude --resume <id>` 或 `codex resume <id>` 一样。
- 悬停并点击归档按钮，它会移到下方的**已归档**，在那里仍可恢复。
- 右键点击其中一个，可以恢复、归档或还原它，复制它的 ID 或文件夹，显示文件夹，或删除它。右键点击**已结束**可全部归档，右键点击**已归档**可全部删除。

这需要为该智能体设置好 [Kero 的技能](./agents.md#install-the-skill)，由它把对话的 ID 告诉 Kero。

## 项目

**项目**就是一个文件夹。Kero 在边栏中将会话归到各自的项目下。用 <kbd>⌘</kbd> <kbd>O</kbd> 打开项目，或者让 `kero new` 根据会话的启动目录自动选择。

```sh
kero projects            # 列出项目
kero new -p my-app       # 按名称在某个项目中新建会话
```

## kerod

会话运行在 **kerod** 中，这是一个由 app 按需启动的守护进程。Kero 只是 kerod 的一扇窗口：

- 退出 Kero 后，所有会话仍在运行。
- 再次打开 Kero，所有会话都保持原样，回滚缓冲也在。
- kerod 自身重启之后（比如重启电脑后），Kero 会在原来的目录中重新启动你的会话。设置 `restore-sessions = false` 可以关闭这一行为。
- 为某个智能体设置好 [Kero 的技能](./agents.md#install-the-skill)后，原先运行 Claude Code、Codex、Pi 或 OpenCode 的窗格会接着之前的对话继续，效果与智能体自带的恢复选项相同。其他程序不会擅自重新启动：它们的窗格会回到 shell。设置 `resume-agents = false` 可以关闭这一行为。

```sh
kero daemon status       # kerod 是否在运行？
kero daemon restart      # 重启它（会话会重新启动）
kero daemon stop         # 停止它，并结束所有会话
```
