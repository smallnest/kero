package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"kero/internal/feishu"
	"kero/internal/proto"
	"kero/internal/settings"
)

const feishuUsage = `Usage:
  kero feishu setup [--app <id>] [--email <address>]
                                  ask for the app's ID and secret, keep them,
                                  and connect: the address may use the bot too
  kero feishu status [--json]      whether the bot is connected
  kero feishu test                 ask Feishu for an access token, as a check
  kero feishu send <chat> <text>   send a message to a chat, by its ID`

func feishuCmd(args []string, host string) error {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "setup":
		return feishuSetup(args)
	case "status":
		return feishuStatusCmd(args, host)
	case "test":
		return feishuTest()
	case "send":
		return feishuSend(args, host)
	case "", "help", "--help", "-h":
		fmt.Print(feishuUsage)
		return nil
	}
	return usageErr("kero feishu: no command %q", sub)
}

// feishuSetup asks for the app's ID and secret, keeps the secret in kero's
// directory, writes the keys, connects kerod's bot, and, given the
// account's email, sends the test message it says works.
func feishuSetup(args []string) error {
	f, err := parse(args, map[string]string{"app": "app", "email": "email"},
		map[string]string{"force": "force", "test": "test"})
	if err != nil {
		return err
	}
	app := f.vals["app"]
	if app == "" && len(f.pos) > 0 {
		app = f.pos[0]
	}
	email := f.vals["email"]
	if len(f.pos) > 1 {
		email = f.pos[1]
	}
	in := bufio.NewReader(os.Stdin)
	ask := func(question string) (string, error) {
		fmt.Print(question)
		if term.IsTerminal(int(os.Stdin.Fd())) {
			line, err := in.ReadString('\n')
			return strings.TrimSpace(line), err
		}
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}
	if app == "" {
		if app, err = ask("The Feishu app's App ID (cli_…): "); err != nil {
			return err
		}
	}
	if app == "" {
		return usageErr("kero feishu setup: the app's ID is needed")
	}
	secret, secretErr := settings.LoadFeishuSecret()
	if secretErr != nil && !errors.Is(secretErr, settings.ErrFeishuNotConfigured) {
		return secretErr
	}
	appSecret := ""
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Print("The App Secret (not shown): ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return err
		}
		appSecret = strings.TrimSpace(string(b))
	} else {
		if appSecret, err = ask("The App Secret: "); err != nil {
			return err
		}
	}
	if appSecret == "" {
		return usageErr("kero feishu setup: the App Secret is needed")
	}
	fmt.Println("Checking the app with Feishu…")
	if err := feishu.Probe(app, appSecret); err != nil {
		fmt.Printf("\n%s\n\n", err)
		fmt.Print(feishuChecklist)
		return err
	}
	fmt.Println("Feishu knows the app.")
	saved := settings.FeishuSecret{AppID: app, AppSecret: appSecret, Me: secret.Me}
	if err := settings.SaveFeishuSecret(saved); err != nil {
		return err
	}
	fmt.Printf("The secret is kept in %s, readable by you only.\n", settings.FeishuSecretPath())
	set := settings.Load()
	fset := set.Feishu
	if fset == nil {
		fset = &settings.Feishu{}
	}
	fset.App = app
	if email != "" {
		fset.Allow = append(fset.Allow, email)
	}
	if err := settings.ValidateFeishu(fset); err != nil {
		return err
	}
	if err := settings.SetFeishu(*fset); err != nil {
		return err
	}
	fmt.Println("feishu-app is in the configuration.")
	if c, done, err := connect(""); err == nil {
		c.Call("feishu.restart", nil, nil)
		var st proto.FeishuState
		if err := c.Call("feishu.status", nil, &st); err == nil && st.Connected {
			fmt.Printf("kerod connects app %s.\n", st.App)
		}
		done()
	}
	if email != "" {
		fmt.Printf("A test message goes to %s once it writes to the bot; the bot replies to it.\n", email)
	}
	fmt.Printf(`
Next, in Feishu:
  1. Open the app in Feishu, or find it in search. A first message to it
     gets Kero going, and Kero replies.
  2. If nothing comes, check that the app has the Bot capability, the
     scopes im:message and im:resource, and the event im.message.receive,
     received over a long connection, and that a version is published.

Who may use the bot: %s (feishu-allow).
`, allowLine(fset.Allow))
	return nil
}

const feishuChecklist = `Check the app in the Feishu Open Platform console (open.feishu.cn/app):
  - Credentials & Basic Info: the App ID and App Secret are the app's.
  - Add Features: the Bot capability is on.
  - Permissions & Scopes: im:message, im:message:readonly, im:resource.
  - Events & Callbacks: the subscription is a long connection, and the
    app has im.message.receive_v1.
  - Version Management: a version is published.
`

func allowLine(allow []string) string {
	if len(allow) == 0 {
		return "only the person who ran kero feishu setup"
	}
	return strings.Join(allow, ", ")
}

// feishuTest checks the app's access to Feishu, which shows a wrong
// secret, or a bot without a published version.
func feishuTest() error {
	secret, err := settings.LoadFeishuSecret()
	if err != nil {
		return err
	}
	if err := feishu.Probe(secret.AppID, secret.AppSecret); err != nil {
		return err
	}
	fmt.Printf("Feishu knows the app %s.\n", secret.AppID)
	return nil
}

// feishuSend sends text to a chat, or to a person by open_id, by the
// machine's app: a test, or a notice.
func feishuSend(args []string, host string) error {
	if len(args) < 2 {
		return usageErr("kero feishu send <chat> <text…>")
	}
	who, text := args[0], strings.Join(args[1:], " ")
	kind := "chat_id"
	if strings.HasPrefix(who, "ou_") {
		kind = "open_id"
	}
	secret, err := settings.LoadFeishuSecret()
	if errors.Is(err, settings.ErrFeishuNotConfigured) && len(host) > 0 {
		return fmt.Errorf("%s: %w", host, err)
	}
	if err != nil {
		return err
	}
	if err := feishu.SendText(secret.AppID, secret.AppSecret, kind, who, text); err != nil {
		return err
	}
	fmt.Println("Sent.")
	return nil
}

// feishuStatusCmd tells whether the bot is connected.
func feishuStatusCmd(args []string, host string) error {
	f, err := parse(args, nil, map[string]string{"json": "json"})
	if err != nil {
		return err
	}
	c, done, err := connect(host)
	if err != nil {
		return err
	}
	defer done()
	var st proto.FeishuState
	if err := c.Call("feishu.status", nil, &st); err != nil {
		return err
	}
	if f.set["json"] {
		return printJSON(st)
	}
	switch {
	case !st.Configured:
		fmt.Println("off: feishu-app names no app. kero feishu setup connects one.")
	case !st.Secret:
		fmt.Println("set up partly: kero feishu setup keeps the app's secret.")
	case st.Connected:
		fmt.Printf("connected: app %s\n", st.App)
		fmt.Printf("who may use it: %s\n", allowLine(settings.Load().Feishu.Allow))
	default:
		fmt.Printf("not connected: app %s, %s\n", st.App, st.Error)
		fmt.Println("kero daemon log holds why.")
	}
	return nil
}
