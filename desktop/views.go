package main

import (
	"embed"
	"math"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/iyazerski/clipsync/desktop/locales"
	"github.com/iyazerski/clipsync/internal/client"
)

const (
	downloadPage = "https://github.com/iyazerski/clipsync/releases/latest"
	relayGuide   = "https://github.com/iyazerski/clipsync#host-your-own-relay"
)

//go:embed icons/*.svg
var iconFiles embed.FS

func icon(name string) *ui.SVG {
	data, err := iconFiles.ReadFile("icons/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	return ui.MustParseSVG(data)
}

var (
	iconApp      = icon("app")
	iconAdd      = icon("add")
	iconBack     = icon("arrow_back")
	iconCheck    = icon("check_circle")
	iconChevron  = icon("chevron_right")
	iconCopy     = icon("content_copy")
	iconDevices  = icon("devices")
	iconError    = icon("error")
	iconInfo     = icon("info")
	iconLink     = icon("link")
	iconLock     = icon("lock")
	iconMore     = icon("more_horiz")
	iconOffline  = icon("cloud_off")
	iconPause    = icon("pause")
	iconPhone    = icon("phone_android")
	iconResume   = icon("play_arrow")
	iconSettings = icon("settings")
	iconShow     = icon("visibility")
	iconHide     = icon("visibility_off")
	iconSync     = icon("sync")
	iconUpdate   = icon("system_update")
	iconMac      = icon("laptop_mac")
	iconWindows  = icon("desktop_windows")
	iconComputer = func() *ui.SVG {
		if runtime.GOOS == "windows" {
			return iconWindows
		}
		return iconMac
	}()
)

func (a *app) view(c *ui.Context) {
	a.keepInvite()
	if a.toast != "" {
		c.Toast(a.toast)
		a.toast = ""
	}
	switch a.page {
	case pageWelcome:
		a.welcome(c)
	case pageJoin:
		a.joinPage(c)
	case pageReady:
		a.readyPage(c)
	default:
		a.dashboard(c)
	}
	a.relayDialog(c)
	ui.Modal(c, &a.adding, func() {
		ui.Column(c).Width(560).Gap(18).Children(func() {
			ui.Row(c).Children(func() {
				ui.Text(c, locales.AddTitle()).FontSize(20).Bold().Grow(1)
			})
			a.addDevice(c)
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.PrimaryButton(c, locales.Done()).Clicked() {
					a.adding = false
				}
			})
		})
	})
	if ui.AlertDialog(c, &a.unpairing, locales.UnpairTitle(), locales.UnpairText(), locales.Cancel(), locales.Unpair()) == 1 {
		a.toastIf(c, a.unpair())
	}
	removeText := locales.RemoveText(a.removing.Name)
	if a.cfg.Relay != publicRelay {
		removeText += " " + locales.RemoveTextRelay()
	}
	if ui.AlertDialog(c, &a.askingRemove, locales.RemoveTitle(a.removing.Name), removeText, locales.Cancel(), locales.Remove()) == 1 {
		a.toastIf(c, a.removeDevice(a.removing.id))
	}
}

func (a *app) toastIf(c *ui.Context, err error) {
	if err != nil {
		c.Toast(locales.SaveFailed())
	}
}

// titleBar leaves room for the window controls and lets the empty top of the
// window move it, as a title bar would.
func titleBar(c *ui.Context) {
	bar := c.TitleBar()
	if bar.Height > 0 {
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(bar.Height).DragWindow()
	}
}

// onboarding lays out a page of the first launch, centered, with footer's
// buttons, when there's one, always in sight below it.
func onboarding(c *ui.Context, build, footer func()) {
	ui.Column(c).Fill().Children(func() {
		ui.Scroll(c).Grow(1).Shrink(1).FillWidth().Children(func() {
			ui.Column(c).FillWidth().MinHeightPercent(100).Center().Padding(40, 32).Children(func() {
				ui.Column(c).FillWidth().MaxWidth(480).Gap(16).Children(build)
			})
		})
		if footer != nil {
			t := c.Theme()
			ui.Row(c).FillWidth().Padding(12, 20).Gap(8).Justify(ui.End).
				BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Background(t.Surface).Children(footer)
		}
	})
	titleBar(c)
}

func heading(c *ui.Context, title, intro string) {
	ui.Text(c, title).FontSize(26).Bold().TextAlign(ui.Center)
	ui.Text(c, intro).FontSize(14).TextColor(c.Theme().TextMuted).TextAlign(ui.Center).LineHeight(1.35)
}

func (a *app) welcome(c *ui.Context) {
	onboarding(c, func() {
		ui.Image(c, iconApp).Size(80, 80).AlignSelf(ui.Center).Margin(0, 0, 4, 0)
		heading(c, locales.WelcomeTitle(), locales.WelcomeIntro())
		ui.Column(c).Gap(10).Margin(12, 0, 0, 0).Children(func() {
			if choice(c, iconComputer, locales.WelcomeFirst(), locales.WelcomeFirstDetail()) {
				a.toastIf(c, a.setUpFirst())
			}
			if choice(c, iconLink, locales.WelcomeJoin(), locales.WelcomeJoinDetail()) {
				a.page = pageJoin
			}
		})
		ui.Row(c).Justify(ui.Center).Gap(6).Margin(8, 0, 0, 0).Children(func() {
			label := locales.WelcomeOwnRelay()
			if a.cfg.Relay != publicRelay {
				label = locales.WelcomeRelay(relayHost(a.cfg.Relay))
			}
			if ui.Link(c, label, "").FontSize(13).Clicked() {
				a.openRelay()
			}
		})
	}, nil)
}

// choice is a large button with an icon, a title and a detail line.
func choice(c *ui.Context, svg *ui.SVG, title, detail string) bool {
	t := c.Theme()
	b := ui.ButtonBase(c).Label(title).Padding(16).Gap(14).Radius(12).AlignItems(ui.Center).
		Border(1, t.Border).Background(t.Surface)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		badge(c, svg, t.Accent)
		ui.Column(c).Grow(1).Shrink(1).Gap(3).Children(func() {
			ui.Text(c, title).FontSize(15).FontWeight(600)
			ui.Text(c, detail).FontSize(13).TextColor(t.TextMuted)
		})
		pointing(c, ui.Icon(c, iconChevron).FontSize(20).TextColor(t.TextMuted))
	})
	return b.Clicked()
}

// badge is an icon on a soft square of its color.
func badge(c *ui.Context, svg *ui.SVG, color ui.Color) {
	ui.Box(c).Size(40, 40).Radius(10).Background(color.Alpha(0.14)).Center().Shrink(0).Children(func() {
		ui.Icon(c, svg).FontSize(22).TextColor(color)
	})
}

// pointing mirrors an icon that points along the line, right to left.
func pointing(c *ui.Context, icon ui.Element) {
	if c.Direction() == ui.RightToLeft {
		icon.Scale(-1, 1)
	}
}

func backButton(c *ui.Context) bool {
	b := ui.Button(c, "").Label(locales.Back()).Children(func() {
		pointing(c, ui.Icon(c, iconBack).FontSize(16))
		ui.Text(c, locales.Back()).SingleLine()
	})
	return b.Clicked() || c.Shortcut(0, ui.KeyEscape)
}

func (a *app) joinPage(c *ui.Context) {
	submitted := false
	onboarding(c, func() {
		ui.Row(c).Children(func() {
			if backButton(c) {
				a.cancelJoin()
				a.page, a.link, a.joinErr = pageWelcome, "", joinNoError
			}
		})
		badge(c, iconLink, c.Theme().Accent)
		ui.Text(c, locales.JoinTitle()).FontSize(26).Bold()
		ui.Text(c, locales.JoinIntro()).FontSize(14).TextColor(c.Theme().TextMuted).LineHeight(1.35)
		ui.Field(c, locales.JoinLink(), func() {
			ui.Row(c).Gap(8).Children(func() {
				in := ui.TextInput(c.Key("link"), &a.link).Placeholder("42 183 059").AutoFocus().Grow(1).Shrink(1).
					Direction(ui.LeftToRight).Disabled(a.joining)
				if in.Changed() {
					a.joinErr = joinNoError
				}
				submitted = in.Submitted()
				if ui.Button(c, locales.Paste()).Disabled(a.joining).Clicked() {
					a.link, a.joinErr = strings.TrimSpace(c.ReadClipboard()), joinNoError
				}
			})
		}).Error(a.joinError())
	}, func() {
		if a.joining {
			ui.PrimaryButton(c, "").Label(locales.Joining()).Disabled(true).Children(func() {
				ui.Spinner(c).Size(14, 14)
				ui.Text(c, locales.Joining()).SingleLine()
			})
			return
		}
		if ui.PrimaryButton(c, locales.JoinButton()).Disabled(strings.TrimSpace(a.link) == "").Clicked() || submitted {
			a.toastIf(c, a.join())
		}
	})
}

func (a *app) joinError() string {
	switch a.joinErr {
	case joinInvalid:
		return locales.JoinInvalid()
	case joinWrongCode:
		return locales.JoinWrongCode()
	case joinCodeGone:
		return locales.JoinCodeGone()
	case joinOffline:
		return locales.JoinOffline(relayHost(a.cfg.Relay))
	}
	return ""
}

func (a *app) readyPage(c *ui.Context) {
	onboarding(c, func() {
		ui.Row(c).Gap(10).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconCheck).FontSize(30).TextColor(c.Theme().Success)
			ui.Text(c, locales.ReadyTitle()).FontSize(26).Bold()
		})
		ui.Text(c, locales.ReadyIntro()).FontSize(14).TextColor(c.Theme().TextMuted).TextAlign(ui.Center).LineHeight(1.35)
		ui.Column(c).Margin(4, 0, 0, 0).Padding(20).Radius(14).Border(1, c.Theme().Border).Children(func() {
			a.addDevice(c)
		})
		if runtime.GOOS == "darwin" {
			note(c, iconInfo, locales.PasteboardNote())
		}
	}, func() {
		if ui.PrimaryButton(c, locales.Done()).Clicked() {
			a.page = pageDevices
		}
	})
}

func note(c *ui.Context, svg *ui.SVG, text string) {
	t := c.Theme()
	ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, svg).FontSize(18).TextColor(t.TextMuted)
		ui.Text(c, text).FontSize(12.5).TextColor(t.TextMuted).Grow(1).Shrink(1).LineHeight(1.35)
	})
}

// addDevice shows how to pair a phone or another computer with this one.
func (a *app) addDevice(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(18).Children(func() {
		ui.Segmented(c, &a.addTab, locales.AddPhone(), locales.AddComputer()).FillEqually().Label(locales.AddTitle())
		if a.addTab == 0 {
			ui.Row(c).Gap(24).AlignItems(ui.Center).Children(func() {
				a.qrCode(c)
				ui.Column(c).Grow(1).Shrink(1).Gap(14).Children(func() {
					step(c, 1, locales.PhoneStep1())
					step(c, 2, locales.PhoneStep2())
					step(c, 3, locales.PhoneStep3())
					ui.Text(c, locales.PhoneNote()).FontSize(12).TextColor(t.TextMuted)
				})
			})
			ui.Text(c, locales.PhoneCode()).FontSize(13).TextColor(t.TextMuted)
			a.codeBox(c)
		} else {
			ui.Column(c).Gap(14).Children(func() {
				step(c, 1, locales.ComputerStep1())
				ui.Row(c).MarginStart(34).Gap(8).Children(func() {
					if ui.Button(c, locales.CopyDownloadLink()).Clicked() {
						c.WriteClipboard(downloadPage)
						c.Toast(locales.Copied())
					}
				})
				step(c, 2, locales.ComputerStep2())
				step(c, 3, locales.ComputerStep3())
				ui.Column(c).MarginStart(34).Children(func() { a.codeBox(c) })
				ui.Text(c, locales.OrLink()).FontSize(13).TextColor(t.TextMuted).Margin(6, 0, 0, 0).MarginStart(34)
				a.linkBox(c)
			})
		}
		note(c, iconLock, locales.SecretWarning())
	})
}

// codeBox shows the code that another device can join with, and what became
// of the last one.
func (a *app) codeBox(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).MinHeight(44).Children(func() {
			switch {
			case a.codeNotice == codeFailed || a.codeNotice == codeWrong:
				text, color := locales.CodeFailed(), t.Danger
				if a.codeNotice == codeWrong {
					text, color = locales.CodeWrong(), t.Warning
				}
				ui.Icon(c, iconError).FontSize(18).TextColor(color)
				ui.Text(c, text).FontSize(13.5).Grow(1).Shrink(1)
				if ui.Button(c, locales.CodeNew()).Clicked() {
					a.openInvite()
				}
			case a.code == "":
				ui.Spinner(c).Size(18, 18).Label(locales.CodeOpening())
				ui.Text(c, locales.CodeOpening()).FontSize(13.5).TextColor(t.TextMuted)
			default:
				ui.Text(c, client.FormatCode(a.code)).Direction(ui.LeftToRight).
					Font("ui-monospace").FontSize(30).Bold().Selectable().Grow(1)
				if ui.Button(c, locales.CodeNew()).Clicked() {
					a.openInvite()
				}
			}
		})
		switch {
		case a.codeNotice == codeJoined:
			ui.Text(c, locales.CodeJoined()).FontSize(12.5).TextColor(t.Success)
		case a.code != "":
			ui.Text(c, locales.CodeOnce()).FontSize(12.5).TextColor(t.TextMuted)
		}
	})
}

func (a *app) linkBox(c *ui.Context) {
	t := c.Theme()
	link := a.cfg.pairLink()
	if !a.showLink {
		link = a.cfg.Relay + "/pair#" + strings.Repeat("•", 16)
	}
	ui.Row(c).MarginStart(34).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, link).Direction(ui.LeftToRight).Font("ui-monospace").FontSize(12.5).SingleLine().Ellipsis("…").Selectable().
			Grow(1).Shrink(1).Padding(8, 10).Radius(8).Background(t.Surface).Border(1, t.Border)
		show, label := iconShow, locales.ShowLink()
		if a.showLink {
			show, label = iconHide, locales.HideLink()
		}
		if ui.Button(c, "").Label(label).Tooltip(label).Children(func() { ui.Icon(c, show).FontSize(16) }).Clicked() {
			a.showLink = !a.showLink
		}
		if ui.PrimaryButton(c, "").Label(locales.CopyLink()).Children(func() {
			ui.Icon(c, iconCopy).FontSize(15)
			ui.Text(c, locales.CopyLink()).SingleLine()
		}).Clicked() {
			a.sys.copySecret(a.cfg.pairLink())
			c.Toast(locales.Copied())
		}
	})
}

func step(c *ui.Context, n int, text string) {
	t := c.Theme()
	ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
		ui.Box(c).Size(22, 22).Radius(11).Shrink(0).Background(t.Accent.Alpha(0.14)).Center().Children(func() {
			ui.Text(c, strconv.Itoa(n)).FontSize(12).Bold().TextColor(t.Accent)
		})
		ui.Text(c, text).FontSize(13.5).Grow(1).Shrink(1).LineHeight(1.35).Margin(2, 0, 0, 0)
	})
}

// qrCode draws the pairing link's QR code, black on white whatever the theme,
// as phone cameras read it best.
func (a *app) qrCode(c *ui.Context) {
	const size = 176
	modules := a.qr
	ui.Box(c).Size(size+24, size+24).Shrink(0).Padding(12).Radius(12).Background(ui.RGB(255, 255, 255)).
		Border(1, c.Theme().Border).Label(locales.QrLabel()).Draw(func(p *ui.Painter, r ui.Rect) {
		n := len(modules)
		if n == 0 {
			return
		}
		// Whole device pixels per module keep the code sharp.
		scale := p.Scale()
		cell := float32(math.Floor(float64(size*scale)/float64(n))) / scale
		x0 := r.X + (r.W-cell*float32(n))/2
		y0 := r.Y + (r.H-cell*float32(n))/2
		black := ui.RGB(0, 0, 0)
		for y, row := range modules {
			for x, on := range row {
				if on {
					p.Fill(ui.Rect{X: x0 + float32(x)*cell, Y: y0 + float32(y)*cell, W: cell, H: cell}, black, 0)
				}
			}
		}
	})
}

func relayHost(relay string) string {
	if u, err := url.Parse(relay); err == nil && u.Host != "" {
		return u.Host
	}
	return relay
}

func (a *app) openRelay() {
	a.relayOpen, a.relayFailed = true, false
	a.relayDraft = ""
	if a.cfg.Relay != publicRelay {
		a.relayDraft = a.cfg.Relay
	}
}

func (a *app) relayDialog(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.relayOpen, func() {
		ui.Column(c).Width(460).Gap(14).Children(func() {
			ui.Text(c, locales.RelayTitle()).FontSize(20).Bold()
			ui.Text(c, locales.RelayIntro()).FontSize(13.5).TextColor(t.TextMuted).LineHeight(1.35)
			var failed string
			if a.relayFailed {
				failed = locales.RelayInvalid()
			}
			submitted := false
			ui.Field(c, locales.RelayAddress(), func() {
				in := ui.TextInput(c.Key("relay"), &a.relayDraft).Placeholder("https://clip.example.com").AutoFocus().Direction(ui.LeftToRight)
				if in.Changed() {
					a.relayFailed = false
				}
				submitted = in.Submitted()
			}).Error(failed)
			if a.cfg.paired() {
				note(c, iconInfo, locales.RelayRepair())
			}
			ui.Link(c, locales.RelayGuide(), relayGuide).FontSize(13)
			ui.Row(c).Gap(8).Margin(6, 0, 0, 0).Children(func() {
				if a.cfg.Relay != publicRelay && ui.Button(c, locales.RelayUsePublic()).Clicked() {
					a.toastIf(c, a.setRelay(publicRelay))
				}
				ui.Spacer(c)
				if ui.Button(c, locales.Cancel()).Clicked() {
					a.relayOpen = false
				}
				if ui.PrimaryButton(c, locales.Save()).Disabled(strings.TrimSpace(a.relayDraft) == "").Clicked() || submitted {
					a.toastIf(c, a.setRelay(a.relayDraft))
				}
			})
		})
	})
}

func (a *app) dashboard(c *ui.Context) {
	t := c.Theme()
	vibrant := c.Vibrancy()
	if vibrant {
		c.Root().Background(ui.Transparent)
	}
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		side := ui.Column(c).Width(212).Shrink(0).Padding(max(c.TitleBar().Bottom+14, 16), 10, 12, 10).Gap(12).DragWindow()
		if !vibrant {
			side.Background(t.Surface).BorderWidth(0, 1, 0, 0).BorderColor(t.Border)
		}
		side.Children(func() { a.sidebar(c) })
		ui.Column(c).Grow(1).Shrink(1).Background(t.Background).Children(func() {
			ui.Scroll(c.Key(a.page)).Fill().Children(func() {
				ui.Column(c).FillWidth().MaxWidth(720).Padding(max(c.TitleBar().Height, 20)+12, 32, 32, 32).Gap(18).Children(func() {
					if a.page == pageSettings {
						a.settings(c)
					} else {
						a.devices(c)
					}
				})
			})
			titleBar(c)
		})
	})
}

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(10).Padding(0, 8, 6, 8).AlignItems(ui.Center).Children(func() {
		ui.Image(c, iconApp).Size(26, 26)
		ui.Text(c, "clipsync").FontSize(16).Bold()
	})
	chosen := "devices"
	if a.page == pageSettings {
		chosen = "settings"
	}
	if ui.Sidebar(c, &chosen, func() {
		ui.SidebarItem(c, "devices", iconDevices, locales.NavDevices())
		ui.SidebarItem(c, "settings", iconSettings, locales.NavSettings())
	}).Grow(1).Background(ui.Transparent).Changed() {
		a.page = pageDevices
		if chosen == "settings" {
			a.page = pageSettings
		}
	}
	if a.updates.stage == updateReady {
		if ui.PrimaryButton(c, "").Label(locales.UpdateRestart()).Children(func() {
			ui.Icon(c, iconUpdate).FontSize(16)
			ui.Text(c, locales.UpdateRestart()).SingleLine()
		}).Clicked() {
			a.sys.restart()
		}
	}
	ui.Row(c).Gap(8).Padding(4, 8).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(8, 8).Radius(4).Background(statusColor(t, a.sync))
		ui.Text(c, statusLabel(a.sync)).FontSize(12.5).TextColor(t.TextMuted)
	})
}

func statusColor(t *ui.Theme, s syncState) ui.Color {
	switch s {
	case syncConnected:
		return t.Success
	case syncConnecting, syncOffline:
		return t.Warning
	case syncRefused, syncBroken:
		return t.Danger
	}
	return t.TextMuted
}

func statusLabel(s syncState) string {
	switch s {
	case syncConnecting:
		return locales.StatusConnecting()
	case syncConnected:
		return locales.StatusSyncing()
	case syncOffline:
		return locales.StatusOffline()
	case syncRefused:
		return locales.StatusRefused()
	case syncBroken:
		return locales.StatusBroken()
	}
	return locales.StatusPaused()
}

func (a *app) statusDetail(c *ui.Context) string {
	host := relayHost(a.cfg.Relay)
	switch a.sync {
	case syncConnecting:
		return locales.StatusConnectingDetail(host)
	case syncConnected:
		last := a.lastSent
		if a.lastReceived.After(last) {
			last = a.lastReceived
		}
		if last.IsZero() {
			return locales.StatusSyncingDetail(host)
		}
		return locales.StatusLastSync(host, since(c, last))
	case syncOffline:
		return locales.StatusOfflineDetail(host)
	case syncRefused:
		return locales.StatusRefusedDetail(host)
	case syncBroken:
		return locales.StatusBrokenDetail()
	}
	return locales.StatusPausedDetail()
}

// since says how long ago t was, in whole minutes, and asks for a frame when that changes.
func since(c *ui.Context, t time.Time) string {
	elapsed := c.Now().Sub(t)
	c.After(time.Minute - elapsed%time.Minute)
	minutes := math.Floor(elapsed.Minutes())
	if minutes < 60 {
		return i18n.FormatRelativeTime(c.Locale(), -minutes, i18n.RelativeMinute, i18n.RelativeTimeOptions{Auto: true})
	}
	if hours := math.Floor(elapsed.Hours()); hours < 24 {
		return i18n.FormatRelativeTime(c.Locale(), -hours, i18n.RelativeHour, i18n.RelativeTimeOptions{Auto: true})
	}
	return i18n.FormatRelativeTime(c.Locale(), -math.Floor(elapsed.Hours()/24), i18n.RelativeDay, i18n.RelativeTimeOptions{Auto: true})
}

func pageHeader(c *ui.Context, title string, actions func()) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).MinHeight(32).Children(func() {
		ui.Text(c, title).FontSize(24).Bold().Grow(1)
		if actions != nil {
			actions()
		}
	})
}

func card(c *ui.Context) ui.Element {
	t := c.Theme()
	return ui.Column(c).Radius(12).Border(1, t.Border).Background(t.Surface)
}

func (a *app) devices(c *ui.Context) {
	t := c.Theme()
	pageHeader(c, locales.NavDevices(), func() {
		if ui.PrimaryButton(c, "").Label(locales.AddDevice()).Children(func() {
			ui.Icon(c, iconAdd).FontSize(16)
			ui.Text(c, locales.AddDevice()).SingleLine()
		}).Clicked() {
			a.adding = true
		}
	})
	card(c).Children(func() {
		ui.Row(c).Padding(18).Gap(14).AlignItems(ui.Center).Children(func() {
			svg := iconSync
			switch a.sync {
			case syncPaused:
				svg = iconPause
			case syncOffline:
				svg = iconOffline
			case syncRefused, syncBroken:
				svg = iconError
			}
			badge(c, svg, statusColor(t, a.sync))
			ui.Column(c).Grow(1).Shrink(1).Gap(3).Children(func() {
				ui.Text(c, statusLabel(a.sync)).FontSize(16).FontWeight(600)
				ui.Text(c, a.statusDetail(c)).FontSize(13).TextColor(t.TextMuted).LineHeight(1.35)
			})
			if a.sync == syncPaused {
				if ui.Button(c, "").Label(locales.Resume()).Children(func() {
					ui.Icon(c, iconResume).FontSize(16)
					ui.Text(c, locales.Resume()).SingleLine()
				}).Clicked() {
					a.resume()
				}
			} else if a.sync != syncBroken {
				if ui.Button(c, "").Label(locales.Pause()).Children(func() {
					ui.Icon(c, iconPause).FontSize(16)
					ui.Text(c, locales.Pause()).SingleLine()
				}).Clicked() {
					a.pause()
				}
			}
		})
	})
	ui.Text(c, locales.YourDevices()).FontSize(13).FontWeight(600).TextColor(t.TextMuted).Margin(8, 0, -6, 4)
	others := a.otherDevices()
	card(c).Children(func() {
		deviceRow(c, iconComputer, t.Accent, a.computer, osName(), func() {
			ui.Badge(c, locales.ThisDevice()).Background(t.Accent.Alpha(0.14)).TextColor(t.Accent)
		})
		for _, d := range others {
			ui.Divider(c).MarginStart(68)
			svg, kind := kindLook(d.Kind)
			color, status := t.TextMuted, locales.DeviceSeen(since(c, d.Seen))
			switch {
			case d.online:
				color, status = t.Success, locales.DeviceOnline()
			case d.Pending:
				color, status = t.Warning, locales.DevicePending()
			}
			ui.Column(c.Key(d.id)).Children(func() {
				deviceRow(c, svg, color, d.Name, kind+" · "+status, func() { a.deviceMenu(c, d) })
			})
		}
	})
	if len(others) == 0 {
		ui.Text(c, locales.NoOtherDevices()).FontSize(13).TextColor(t.TextMuted).LineHeight(1.35).Margin(-4, 4, 0, 4)
	}
	more := ui.ButtonBase(c).Label(locales.AddMore()).Padding(14, 18).Gap(14).Radius(12).AlignItems(ui.Center).
		Border(1.5, t.Border).BorderStyle(ui.BorderDashed)
	if more.Hovered() {
		more.Background(t.SurfaceHover)
	}
	more.Children(func() {
		badge(c, iconAdd, t.TextMuted)
		ui.Column(c).Grow(1).Shrink(1).Gap(3).Children(func() {
			ui.Text(c, locales.AddMore()).FontSize(15).FontWeight(600)
			ui.Text(c, locales.AddMoreDetail()).FontSize(13).TextColor(t.TextMuted)
		})
	})
	if more.Clicked() {
		a.adding = true
	}
}

func deviceRow(c *ui.Context, svg *ui.SVG, color ui.Color, name, detail string, end func()) {
	t := c.Theme()
	ui.Row(c).Padding(14, 18).Gap(14).AlignItems(ui.Center).Children(func() {
		badge(c, svg, color)
		ui.Column(c).Grow(1).Shrink(1).Gap(3).Children(func() {
			ui.Text(c, name).FontSize(15).FontWeight(600).SingleLine().Ellipsis("…")
			ui.Text(c, detail).FontSize(13).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
		})
		if end != nil {
			end()
		}
	})
}

// deviceMenu is what the user can do with another device: remove it, and
// after a new pairing, add it again or take it off this computer's list.
func (a *app) deviceMenu(c *ui.Context, d listedDevice) {
	t := c.Theme()
	more := ui.ButtonBase(c).Label(locales.More()).Padding(6).Radius(8)
	if more.Hovered() {
		more.Background(t.SurfaceHover)
	}
	more.Children(func() {
		ui.Icon(c, iconMore).FontSize(18).TextColor(t.TextMuted)
	}).PopoverMenuButton(func(m *ui.Menu) {
		if d.Pending {
			if m.Item(locales.AddDevice()).Chosen() {
				a.adding = true
			}
			if m.Item(locales.ForgetDevice()).Chosen() {
				a.forgetDevice(d.id)
			}
			// It may have joined the new pairing without connecting yet.
			m.Separator()
		}
		if m.Item(locales.RemoveDevice()).Chosen() {
			a.removing, a.askingRemove = d, true
		}
	})
}

// kindLook is the icon and name of a kind of device.
func kindLook(kind string) (*ui.SVG, string) {
	switch kind {
	case "android":
		return iconPhone, "Android"
	case "windows":
		return iconWindows, "Windows"
	}
	return iconMac, "macOS"
}

func osName() string {
	if runtime.GOOS == "windows" {
		return "Windows"
	}
	return "macOS"
}

// settingRow is a row of a settings card: a title with a detail, and its control.
func settingRow(c *ui.Context, title, detail string, control func()) {
	t := c.Theme()
	ui.Row(c).Padding(12, 16).Gap(16).AlignItems(ui.Center).MinHeight(52).Children(func() {
		ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(13.5)
			if detail != "" {
				ui.Text(c, detail).FontSize(12).TextColor(t.TextMuted).LineHeight(1.3)
			}
		})
		control()
	})
}

func section(c *ui.Context, title string, rows func()) {
	t := c.Theme()
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(t.TextMuted).Margin(0, 0, 0, 4)
		card(c).Dividers(1, t.Border).Children(rows)
	})
}

func (a *app) settings(c *ui.Context) {
	t := c.Theme()
	pageHeader(c, locales.NavSettings(), nil)
	section(c, locales.SettingsGeneral(), func() {
		settingRow(c, locales.Language(), "", func() {
			// "" follows the system's language.
			languages := append([]string{""}, a.sys.languages()...)
			names := make([]string, len(languages))
			chosen := a.sys.language()
			current := ""
			for i, tag := range languages {
				names[i] = locales.LanguageSystem()
				if tag != "" {
					names[i] = i18n.Autonym(tag)
				}
				if tag == chosen {
					current = names[i]
				}
			}
			if ui.Select(c, &current, names).Label(locales.Language()).Width(200).Changed() {
				for i, name := range names {
					if name == current {
						a.sys.setLanguage(languages[i])
					}
				}
			}
		})
		settingRow(c, locales.OpenAtLogin(), locales.OpenAtLoginDetail(), func() {
			on := a.sys.openAtLogin()
			if ui.Switch(c, &on).Label(locales.OpenAtLogin()).Changed() {
				if err := a.sys.setOpenAtLogin(on); err != nil {
					c.Toast(locales.SaveFailed())
				}
			}
		})
		tray, trayDetail := locales.ShowInMenuBar(), locales.ShowInMenuBarDetail()
		if runtime.GOOS == "windows" {
			tray, trayDetail = locales.ShowInTray(), locales.ShowInTrayDetail()
		}
		settingRow(c, tray, trayDetail, func() {
			on := !a.cfg.HideTray
			if ui.Switch(c, &on).Label(tray).Changed() {
				a.cfg.HideTray = !on
				a.toastIf(c, a.save())
				a.sys.showTray(on)
			}
		})
	})
	section(c, locales.SettingsUpdates(), func() {
		if a.updates.enabled {
			settingRow(c, locales.AutoUpdate(), locales.AutoUpdateDetail(), func() {
				on := !a.cfg.ManualUpdates
				if ui.Switch(c, &on).Label(locales.AutoUpdate()).Changed() {
					a.cfg.ManualUpdates = !on
					a.toastIf(c, a.save())
				}
			})
		}
		settingRow(c, locales.Version(versionText()), a.updateDetail(), func() {
			if !a.updates.enabled {
				return
			}
			switch a.updates.stage {
			case updateChecking, updateInstalling:
				ui.Spinner(c).Label(a.updateDetail())
			case updateAvailable:
				if ui.PrimaryButton(c, locales.UpdateInstall()).Clicked() {
					a.sys.installUpdate()
				}
			case updateReady:
				if ui.PrimaryButton(c, locales.UpdateRestart()).Clicked() {
					a.sys.restart()
				}
			default:
				if ui.Button(c, locales.CheckUpdates()).Clicked() {
					a.sys.checkUpdates(true)
				}
			}
		})
	})
	section(c, locales.SettingsRelay(), func() {
		kind := locales.RelayPublic()
		if a.cfg.Relay != publicRelay {
			kind = locales.RelayOwn()
		}
		settingRow(c, relayHost(a.cfg.Relay), kind, func() {
			if ui.Button(c, locales.RelayChange()).Clicked() {
				a.openRelay()
			}
		})
		if a.cfg.Relay != publicRelay {
			token, _ := client.Token(a.cfg.Secret)
			settingRow(c, locales.RelayToken(), locales.RelayTokenDetail(), func() {
				if ui.Button(c, "").Label(locales.Copy()).Children(func() {
					ui.Icon(c, iconCopy).FontSize(15)
					ui.Text(c, locales.Copy()).SingleLine()
				}).Clicked() {
					a.sys.copySecret(token)
					c.Toast(locales.Copied())
				}
			})
		}
	})
	section(c, locales.SettingsComputer(), func() {
		settingRow(c, locales.Logs(), locales.LogsDetail(), func() {
			if ui.Button(c, locales.ShowLogs()).Clicked() {
				a.sys.openLogs()
			}
		})
		settingRow(c, locales.Unpair(), locales.UnpairDetail(), func() {
			if ui.Button(c, locales.UnpairButton()).TextColor(t.Danger).Clicked() {
				a.unpairing = true
			}
		})
	})
}

func versionText() string {
	if v := appVersion(); v != "" {
		return v
	}
	return "dev"
}

func (a *app) updateDetail() string {
	switch a.updates.stage {
	case updateChecking:
		return locales.UpdateChecking()
	case updateCurrent:
		return locales.UpdateCurrent()
	case updateAvailable:
		return locales.UpdateAvailable(a.updates.version)
	case updateInstalling:
		return locales.UpdateDownloading(a.updates.version)
	case updateReady:
		return locales.UpdateReady(a.updates.version)
	case updateFailed:
		return locales.UpdateFailed()
	}
	return ""
}
