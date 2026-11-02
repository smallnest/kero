---
title: Feishu
description: Feishu または Lark のプライベートチャットやグループから、Kero のセッションで動くコーディングエージェントを操作します。
status: draft
---

# Feishu

Feishu 対応は、1 台のマシンの kerod に 1 つの Feishu アプリを接続します。ボットにメッセージを送るか、グループで @ メンションすると、Kero セッションのコーディングエージェントがそれを受け取ります。回答はカードとしてチャットに返ります。セッションは Kero のサイドバーに残るので、Feishu で会話を続けながら、見ることもターミナルから引き継ぐこともできます。

この考えは [MetaBot](https://github.com/xvirobotics/metabot) から来ています。MetaBot は Feishu などのチャットチャネルをコーディングエージェントにつなぎます。Kero は別のやり方をします。MetaBot は自身のエンジンを動かします。Kero は **すでにあるセッションを使います**: そのプロセス、エージェント検出、会話履歴、状態、ほかのホストへのアクセスです。

## アーキテクチャ

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

ボットは Feishu への長寿命の WebSocket を保ちます。Feishu はこれを「長接続」と呼びます。短い接続には webhook 用の公開 HTTPS エンドポイントが必要です。長接続はポートを開けません。

### kerod で動かす理由

| 選択 | 得られるもの | 失うもの |
| --- | --- | --- |
| MetaBot のように別プログラム | 機能がそろっている | 2 つ目のランタイムと 2 つ目のセッション一式。Kero のペインを操作も引き継ぎもできない |
| ここで設計したように kerod 内 | エージェント検出、会話の再開、状態、ホスト、1 つのバイナリ。チャットの作業がアプリに見える | kerod にコードが増える |
| アプリ内 | ボットはアプリが終了すると止まる | アプリが App Secret をバックグラウンドで持ち、アプリが閉じていると kerod の助けがない |

どの場合もプロンプトは kerod を通ってペインに入ります。後の段階で IM アダプターをアプリに移し、アプリがすでに持つ kerod への接続からペインに入れられます。kerod は変わりません。

## Feishu のコマンド

| コマンド | 何をするか |
| --- | --- |
| `/new [プロジェクト] [エージェント]` | プロジェクトでセッションを始め、ペインでエージェントを動かし、チャットをそれに結び付けます |
| `/use <セッション>` | 名前か ID の先頭で、開いているセッションにチャットを結び付けます |
| `/ls` | 開いているセッションを一覧し、それぞれに状態の点を付けます |
| `/status` | 結び付いたセッションの状態、ブランチ、変更、ディレクトリを表示します |
| `/stop` | 今のターンを中断します（Ctrl-C） |
| `/answer <テキスト>` | エージェントの質問に答えます |
| `/close` | 結び付いたセッションを、確認のあと終えます |

コマンドでないテキストはプロンプトとして結び付いたセッションへ送られます。グループでは、ボットを @ メンションしたメッセージだけがそうします。グループの会話はそれぞれ自分のスレッド（「トピック」）を持ちます。

### コマンドモード

`feishu-run = command` では、kerod がプロンプトごとにエージェント自身のコマンドをターミナルなしで動かします:

```text
claude -p --output-format text --session-id <id> "<prompt>"    # Claude Code
pi -p --session-id <id> "<prompt>"                              # Pi
gemini -p "<prompt>"                                            # Gemini CLI
opencode run "<prompt>"
codex exec "<prompt>"
```

各コマンドはチャットのセッションで動きます。そのセッションにはターミナルもシェルもありません。動いているあいだサイドバーは working を、終わると idle を表示します。`feishu-dir` のディレクトリで、ペインと同じ環境で動きます。出力は kero のディレクトリの `feishu-run` 下のファイルに書き込まれ、そのテキストが返信になります。チャットは会話を続けます。最初のターンは kerod が作った ID でセッションを始め、次のターンがそれを続けます:

| エージェント | 最初のターン | 次のターン |
| --- | --- | --- |
| Claude Code | `--session-id <id>` | `--resume <id>`、または結果が示す ID |
| Pi | `--session-id <id>` | `--session-id <id>` |
| Gemini | `--session-id <id>` | `--resume <id>` |
| OpenCode | `run "<prompt>"` | `run --session <id>`。出力がセッションを示す |
| Codex | `exec "<prompt>"` | `exec resume <id> --skip-git-repo-check`。出力がセッションを示す |

`/new <プロジェクト> <エージェント>` は別のエージェントで新しい会話を始めます。

返信はコマンドの出力です。Claude Code では、`feishu-agent-command` で `--output-format stream-json` を使うコマンドは、ストリーム全体ではなくメッセージのテキストを返します。

バックグラウンドのコマンドはペインへの入力を持ちません。そのようなセッションでの `/answer` はそう答えます。

### セッションモード

ボットはプロンプトをペインに入力して Enter を押し、ターンが終わるまでペインを追います: エージェントがユーザーを待つ、または働いたあと画面と状態が静かになるまでです。状態がずっと「working」と言わないターンも、画面が止まれば終えます:

- 画面がプロンプトの前と同じで、回答も出ない場合、ボットは「Kero はターンのテキストを以前のものと区別できません」と返信します。
- ペインが 5 秒を超えて「working」のままで、作業の兆しがない場合、ボットはターンが終わったものとします。

`feishu-timeout` のあともエージェントが働いている場合、返信はそう伝えます。

### ターンの回答

ターミナルの画面はきれいな回答ではありません。TUI の枠やステータスバーを含みます。Claude Code では、`kero skill setup` が入れる `Stop` フック（`kero answer claude`）が、ターンが終わるたびにエージェントの最後のメッセージを、ボットがペインに渡したファイル（kerod のディレクトリの `feishu-answers`）に書き込みます。ボットは画面のテキストではなく、そのメッセージで返信します。ほかのエージェントに使える Stop フックはなく、ボットはターミナルのテキストを使います。

ボットがペインにファイルを渡していなければ、`kero answer` は何もしません。Kero の外でも、どんなエラーでも何もしません: エージェントを失敗させることはありません。

## チャットビュー

チャットは、セッションのペインにターミナルの代わりに表示されます。セッションメニューの **会話**、またはペインヘッダーのチャットボタンをクリックして開きます。ペインは単独で表示され、チャットに十分な場所ができます。ヘッダーのボタンでターミナルに戻ります。チャットには、そのセッションのチャットが表示されます：

- ユーザーのプロンプトは右側；
- エージェントが実行したツールは淡い一行；
- エージェントのメッセージは左側で、ターンの回答には背景を付けます。

チャットを開いたときと、ホストの状態が変わるたびに、セッションのホストが `feishu.feed` でチャットを読みます。上部にはチャットの名前が表示され、検索ボタンでボットが参加しているチャットの一覧が開きます。選んだチャットがペインに表示されます。

下部の入力欄に書いて Enter を押すと、チャットに送信されます。プロンプトはユーザーからのメッセージとして届き、ボットは他のメッセージと同じように回答します。

## 設定

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

| キー | 既定値 | 説明 |
| --- | --- | --- |
| `feishu-app` | 空 | Feishu アプリの App ID。空なら Feishu は無効です。 |
| `feishu-allow` | 空 | プロンプトを送れる人: `open_id`、`user_id`、`email`。空なら `kero feishu setup` を実行したユーザーだけです。 |
| `feishu-timeout` | `30m` | ターンが待つ最長時間。超えるとカードがタイムアウトを示します。 |
| `feishu-stream` | `2s` | 作業中のカードが新しいテキストを取り込む間隔です。 |
| `feishu-run` | `session` | プロンプトがエージェントを動かす方法: `command` はバックグラウンドでエージェント自身のコマンド、`session` はセッションのペインでユーザーのターンとして。 |
| `feishu-agent` | `default` | `/new` セッションが動かすエージェント: `default`、または検出器が知る名前（`claude`、`codex`、`opencode`、`pi`）。 |
| `feishu-dir` | 空 | メッセージがプロジェクトを名指さないときに、新しいセッションが始まるディレクトリ。空なら `new-session-directory` です。 |
| `feishu-agent-command` | 空 | `名前=コマンド`、エージェント名ごと: そのエージェントを動かすコマンド。載っていないエージェントは、引数なしでプログラムを実行します。コマンドは `~/` で始められます。 |

### エージェントの環境

kerod は自分自身の環境でタスクを起動します。その環境は launchd のジョブのものです。PATH はユーザーのログインシェルの PATH に、エージェントの導入先（`~/.local/bin`、`~/.bun/bin`、`~/.cargo/bin`、nvm と fnm の Node、`/opt/homebrew/bin`、`/usr/local/bin`）を続けたものです。ログインシェルの PATH は 1 分に 1 回まで読み直します。エージェントが見つからないときは、`feishu-agent-command` でそのコマンドを指定します。

### App Secret

App Secret は設定ファイルにありません。設定ファイルはよくコピーされ、バックアップされ、dotfiles に同期されますが、App Secret は長寿命の資格情報です。kerod は `kerod.lock` の隣のファイルに、あなたのアカウントだけが読めるように保存し、`kero feishu setup` が書き込みます:

```sh
kero feishu setup     # asks for the App ID and App Secret, connects, and checks the app's settings
kero feishu status    # whether the bot is connected
```

`kero feishu setup` は Feishu アプリを段階ごとに確認します: ボット機能、`im:message` と `im:resource` の権限、`im.message.receive` イベント、公開済みのバージョン。ある段階が失敗すると、その段階と、[Feishu 開放平台のコンソール](https://open.feishu.cn/app)で何を変えるかを示します。

設定ウィンドウはアプリ名、接続状態、使える人を表示します。App Secret は決して表示しません。情報ビュー（⌥⌘I）も、`feishu-app` にアプリがあるあいだ Feishu カードを表示します: App ID、ボットが接続しているか、使える人。

## Kero との組み合わせ

- **エージェント検出**が、エージェントが作業中か、回答を待つか、アイドルかをブリッジに伝えます。
- **会話の再開**により、kerod が再起動しても何も失いません: セッションとエージェントの会話はほかのセッションと同じように戻ります。
- **ファイルペイン**は、どのセッションでも同じように、エージェントが変えたファイルを開きます。
- **ホスト**は `-H` とともに働きます: Feishu のコマンドは結び付いたセッションのホストで動きます。

## 複数ホストとグループ

- 既定では、1 つのチャットのイベントは 1 台のマシンが受け取ります: チャットが結び付いたマシンです。
- 複数のマシンが同じアプリに接続すると、`feishu-host` がチャットのメッセージを送るホストを決め、`/host` がチャットでそれを変えます。
- グループのメッセージはボットを @ メンションしたときだけ作用します。返信は会話のスレッドに送られ、グループ全体には送られません。

## セキュリティ

- **Feishu のメッセージはあなたのシェルへの入力です。** エージェントはペインで入力したときと同じく、あなたのアカウントの権限で動きます。`feishu-allow` が送れる人を制限します。あなたが加えない限り、`kero feishu setup` を実行したアカウントだけが許されます。`*` はボットに届く誰でも許します。
- ブリッジは、ボット自身が送るメッセージ、ほかのアプリのメッセージ（`app_id` が一致する必要があります）、`event_id` で見た重複イベントを無視します。
- カードのボタンは、チャットの持ち主か `feishu-allow` に載った人だけに作用します。
- ログと `kero clients` は App Secret を決して表示しません。

## 依存関係

| 必要 | 選択 |
| --- | --- |
| Feishu API、長接続、イベントの配信 | 公式 Go SDK `github.com/larksuite/oapi-sdk-go/v3` |
| カード | アダプターが組み立てる JSON。長いテキストには `collapsible_panel` |
| カードのボタンイベント | 後の段階。今はテキストコマンド |

## 段階

| 段階 | 内容 |
| --- | --- |
| **M1: 接続** | `feishu-*` キー、`kero feishu setup`、`status`、`test`、`send`、長接続、`feishu-allow`、`/new`、`/use`、`/ls`、`/status`、`/stop`、`/answer`、`/close`、@ メンションのグループ、結び付いたセッションへのテキスト、ターンが表示したもののテキスト返信 |
| **M2: プロトコル** | アイドル、待ち、タイムアウトの待機。更新するカード。working、waiting、error の色 |
| **M3: 体験** | @ メンションとスレッドのグループ。セッションにコピーされる画像とファイル。ボタンと「Kero で開く」 |
| **M4: すべてのエージェント** | 各ターンの回答をファイルに書く: Claude Code の `Stop` フック（`kero answer`）。ファイルがなければターミナルのテキスト |
| **M5: 複数のマシン** | 1 つのアプリのイベントが複数のマシンに届く方法。`/host` |
| **M6: 仕上げ** | ストリーミングカード、Markdown、設定 › エージェント › Feishu、このページの日本語と中国語版 |

## リスク

1. **ターミナルの画面はきれいな回答ではありません。** TUI の枠やステータスバーを含みます。ターンの回答ファイルが主な対策です。ターミナルのテキストはフックのないエージェントの代替です。
2. **貼り付け。** 貼り付けた複数行のプロンプトで、各行の後に Enter を押してはいけません。プレフィックスはブラケットペーストを使う必要があり、プロンプトはすでに使っています。
3. **Feishu アプリの設定はあなたが行います。** 設定の確認は、何が足りないかを正確に示す必要があります。さもないと最初の接続が理由なく失敗します。
