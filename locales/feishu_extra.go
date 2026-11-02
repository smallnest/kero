// The messages of Settings › Agents › Feishu that mygo generate names
// after their messages, not their keys: its keys stay free for this file.

package locales

import "github.com/egoist/mygo/i18n"

// SetFeishu says "Feishu"
// in the language the app shows.
func SetFeishu() string { return In(i18n.Locale()).SetFeishuTitle() }

// SetFeishu says "Feishu"
// in the language of msgs.
func (msgs Messages) SetFeishu() string { return msgs.SetFeishuTitle() }

// SetFeishuOn says "Connected"
// in the language the app shows.
func SetFeishuOn() string { return In(i18n.Locale()).SetFeishuConnectedState() }

// SetFeishuOn says "Connected"
// in the language of msgs.
func (msgs Messages) SetFeishuOn() string { return msgs.SetFeishuConnectedState() }

// SetFeishuOff says "Off"
// in the language the app shows.
func SetFeishuOff() string { return In(i18n.Locale()).SetFeishuOffState() }

// SetFeishuOff says "Off"
// in the language of msgs.
func (msgs Messages) SetFeishuOff() string { return msgs.SetFeishuOffState() }

// SetFeishuSetup says "kero feishu setup"
// in the language the app shows.
func SetFeishuSetup() string { return In(i18n.Locale()).SetFeishuSetupAction() }

// SetFeishuSetup says "kero feishu setup"
// in the language of msgs.
func (msgs Messages) SetFeishuSetup() string { return msgs.SetFeishuSetupAction() }

// SetFeishuOnlySetup says "Only who ran kero feishu setup"
// in the language the app shows.
func SetFeishuOnlySetup() string { return In(i18n.Locale()).SetFeishuOnlySetupAction() }

// SetFeishuOnlySetup says "Only who ran kero feishu setup"
// in the language of msgs.
func (msgs Messages) SetFeishuOnlySetup() string { return msgs.SetFeishuOnlySetupAction() }

// SetFeishuConnecting says "Checking with Feishu…"
// in the language the app shows.
func SetFeishuConnecting() string { return In(i18n.Locale()).SetFeishuConnectingNow() }

// SetFeishuConnecting says "Checking with Feishu…"
// in the language of msgs.
func (msgs Messages) SetFeishuConnecting() string { return msgs.SetFeishuConnectingNow() }
