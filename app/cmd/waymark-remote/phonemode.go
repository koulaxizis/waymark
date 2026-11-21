package main

import (
	"encoding/base64"
	"errors"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/koulaxizis/waymark/app/internal/agent"
	"github.com/koulaxizis/waymark/app/internal/rtc"
)

// PhoneInvite is the QR card on the Agent screen (flow B).
type PhoneInvite struct {
	URL     string `json:"url"`
	QR      string `json:"qr"` // PNG data URL
	Expires int64  `json:"expires"`
}

// AgentInvite shows a new QR for a phone or any browser. An earlier unused
// QR stops working.
func (a *App) AgentInvite() (PhoneInvite, error) {
	ag, err := a.getAgent()
	if err != nil {
		return PhoneInvite{}, err
	}
	inv, err := rtc.NewInvite(ag.Name)
	if err != nil {
		return PhoneInvite{}, err
	}
	png, err := qrcode.Encode(inv.URL, qrcode.Medium, 320)
	if err != nil {
		inv.Close()
		return PhoneInvite{}, err
	}
	a.mu.Lock()
	old := a.invite
	a.invite = inv
	a.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return PhoneInvite{
		URL:     inv.URL,
		QR:      "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		Expires: inv.Expires.UnixMilli(),
	}, nil
}

// AgentAcceptPhone takes the return code the page shows. It returns when the
// browser is connected or the attempt failed.
func (a *App) AgentAcceptPhone(code string) error {
	ag, err := a.getAgent()
	if err != nil {
		return err
	}
	// A code from a page that is coming back (Proposal 13) opens its way.
	if ag.ResumeCode(code) {
		return nil
	}
	a.mu.Lock()
	inv := a.invite
	a.mu.Unlock()
	if inv == nil {
		return errors.New("show a QR first")
	}
	acc, err := inv.Accept(code, (&agentUI{app: a}).Prompt)
	if err != nil {
		return err
	}
	a.mu.Lock()
	if a.invite == inv {
		a.invite = nil
	}
	a.mu.Unlock()
	ag.Attach(acc.ID, acc.Name, acc.Role, acc.Link, resumer{acc.Resume})
	return nil
}

// resumer adapts rtc.Resume to agent.Resumer.
type resumer struct{ r *rtc.Resume }

func (x resumer) Wait(stop <-chan struct{}) (agent.Link, error) {
	l, err := x.r.Wait(stop)
	if err != nil {
		return nil, err
	}
	return l, nil
}
func (x resumer) TryCode(c string) bool { return x.r.TryCode(c) }
func (x resumer) Port() int             { return x.r.Port() }

// AgentCloseInvite hides the QR and frees its port.
func (a *App) AgentCloseInvite() {
	a.mu.Lock()
	inv := a.invite
	a.invite = nil
	a.mu.Unlock()
	if inv != nil {
		inv.Close()
	}
}
