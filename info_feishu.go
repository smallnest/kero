package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"kero/internal/proto"

	"kero/locales"
)

// feishuStatusTick is the time between the reads of the bot's state.
const feishuStatusTick = 5 * time.Second

// infoFeishu is the Info view's card of the Feishu bot: the app it
// connects to, whether kerod connects, and who may use it, while
// feishu-app names one. While the bot connects, the view reads the state
// again, for the connection to drop.
func (a *App) infoFeishu(c *ui.Context, pal *palette, p *proto.Pane) {
	set := a.set.Feishu
	if set == nil || set.App == "" {
		return
	}
	st := a.paneInfoOf(p)
	connected := st.feishu != nil && st.feishu.Connected
	aside := locales.SetFeishuOff()
	if connected {
		aside = locales.SetFeishuOn()
	}
	// The state first, once, else the card would show off for a moment;
	// then again as the bot connects, for the connection to drop.
	if !st.feishuGot && (st.feishu == nil || connected && time.Since(st.feishuAt) > feishuStatusTick) {
		st.feishuGot = true
		a.callOn(a.hostOf(p.ID), "feishu.status", nil, func(err error) {
			a.update(func() {
				if a.info[p.ID] == st {
					st.feishuGot = false
				}
			})
		})
	}
	if st.feishu == nil || st.feishuGot {
		// The state, once kerod answers; the card waits for it.
		return
	}
	who := locales.SetFeishuAllow() + ": " + locales.SetFeishuOnlySetup()
	if len(set.Allow) > 0 {
		who = locales.SetFeishuAllow() + ": " + strings.Join(set.Allow, ", ")
	}
	infoCard(c, pal, linkIcon, locales.SetFeishu(), aside, func() {
		infoRows(c, func() {
			ui.Column(c).Padding(4, 12, 10, 12).Gap(4).Children(func() {
				ui.Text(c, who).FontSize(12).TextColor(pal.textMuted).Wrap()
				if st.feishu != nil && !st.feishu.Connected {
					why := st.feishu.Error
					if why == "" {
						why = "not connected"
					}
					ui.Text(c, why).FontSize(11.5).TextColor(pal.textFaint).Wrap()
				}
				ui.Text(c, fmt.Sprintf("%s, %s", locales.SetFeishuSetup(), set.Agent)).FontSize(11.5).TextColor(pal.textFaint).Wrap().FontFeatures("tnum")
			})
		})
	})
}
