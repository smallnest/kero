---
title: Quick Start
description: Install Kero, open a project and start your first agent session.
---

# Quick Start

Install Kero, open a project, and run your first agent in a session that keeps going after you quit.

## Install

Download the latest Kero from the [releases page](https://github.com/egoist/kero/releases). On macOS, drag **Kero.app** into your Applications folder; on Windows, run the **Kero Setup** installer, which needs no administrator rights.

On Windows, sessions run in PowerShell 7 when it is installed, else Windows PowerShell; choose another shell, such as `cmd` or Git Bash's `bash`, in **Settings › Shell**. Kero needs Windows 10 1809 or later, for its pseudo consoles.

### Install the command line tool

Choose **Kero › Install Command Line Tool…** to put `kero` on your `PATH`: a link in `/usr/local/bin` on macOS, in `~/.local/bin` on Linux, and on Windows Kero's `bin` folder added to your user `PATH`, for terminals you open from then on. Run it in any terminal to check:

```sh
kero help
```

Typing `kero` alone in a terminal opens the app.

## Open a project

Press <kbd>⌘</kbd> <kbd>O</kbd> and choose a folder. Kero adds it to the sidebar as a project and opens a session there.

You can also start from the command line. A new session goes into the project that matches its directory:

```sh
cd ~/code/my-app
kero new --focus
```

## Start an agent

Run your agent in the session the way you would in any terminal:

```sh
claude
```

Or give it a session of its own with a name you'll recognize in the sidebar:

```sh
kero new -n fix-login -- claude
```

While the agent works, its session shows as **working**. When it stops to ask you something, it turns to **waiting** and Kero notifies you. Press <kbd>⌘</kbd> <kbd>J</kbd> to jump to it.

## Quit without losing anything

Press <kbd>⌘</kbd> <kbd>Q</kbd>. Your sessions keep running in kerod. Open Kero again and they're all there, with their scrollback.

## Next steps

- [Panes](./panes.md): split a session to run a dev server beside your agent
- [Coding Agents](./agents.md): run `kero skill setup` so agents learn to use Kero
