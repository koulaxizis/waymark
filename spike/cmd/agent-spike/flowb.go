package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/koulaxizis/waymark/spike/internal/wm"
	"github.com/mdp/qrterminal/v3"
	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
	qrcode "github.com/skip2/go-qrcode"
)

// runFlowB: the phone (browser) is the Controller. A browser cannot listen on
// a port, so the Agent shows a QR, the phone shows a 12-char return code with
// its own address, and the Agent opens the firewall path by sending first.
func runFlowB() error {
	for round := 1; ; round++ {
		err := flowBRound(round)
		if errors.Is(err, errQuit) {
			return nil
		}
		lg.Printf("flow B round %d ended: %v", round, err)
		if *flagFlow != "" && round >= 3 {
			return err
		}
		if a := wm.Ask("\nΝέο QR; / New QR? [Enter = ναι, q = έξοδος]: "); a == "q" || a == "Q" {
			return err
		}
	}
}

func flowBRound(round int) error {
	token := wm.RandBytes(16)
	ufrag := wm.B64(wm.MAC(token, []byte("ice-ufrag")))[:8]
	pwd := wm.B64(wm.MAC(token, []byte("ice-pwd")))[:24]

	udp, port, err := listenUDPHigh()
	if err != nil {
		return err
	}
	sniff := wm.NewSniffConn(udp, ufrag)
	mux := ice.NewUDPMuxDefault(ice.UDPMuxParams{UDPConn: sniff})
	defer mux.Close()

	se := wm.BaseSettings()
	se.SetLite(true)
	se.SetICECredentials(ufrag, pwd)
	se.SetICEUDPMux(mux)
	se.DisableCertificateFingerprintVerification(true) // checked in-channel with the token
	pc, dc, err := wm.NewPC(se)
	if err != nil {
		return err
	}
	defer pc.Close()
	pc.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) { lg.Printf("ICE %s", s) })

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	sdp, err := wm.SetLocalAndGather(pc, offer)
	if err != nil {
		return err
	}
	fp, err := wm.SDPFingerprint(sdp)
	if err != nil {
		return err
	}
	var ips []string
	for _, ip := range wm.LocalIPv4s() {
		ips = append(ips, ip.String())
	}
	if len(ips) == 0 {
		return errors.New("no IPv4 network")
	}
	payload := fmt.Sprintf("WM1.%s.%d.%s.%s", strings.Join(ips, ","), port, wm.B64(fp), wm.B64(token))
	url := *flagPage + "#" + payload
	lg.Printf("flow B round %d: listening on UDP %d, addresses %s", round, port, strings.Join(ips, ", "))

	showQR(url)
	tQR := time.Now()

	// Return code from the phone.
	var phoneIP net.IP
	var phonePort int
	for {
		code := wm.Ask("\nΚωδικός επιστροφής από το κινητό / Return code from the phone: ")
		if code == "" {
			continue
		}
		phoneIP, phonePort, err = wm.ParseReturnCode(token, code)
		if err != nil {
			lg.Printf("return code rejected: %v", err)
			continue
		}
		break
	}
	tCode := time.Now()
	phone := &net.UDPAddr{IP: phoneIP, Port: phonePort}
	lg.Printf("phone at %s (code typed %s after QR)", phone, wm.Secs(tCode.Sub(tQR)))
	lg.Say("   Πάτα «Σύνδεση» στο κινητό. / Press Connect on the phone.\n")

	// Open the path through the Agent's firewall by sending first.
	stopPunch := make(chan struct{})
	defer close(stopPunch)
	go func() {
		t := time.NewTicker(300 * time.Millisecond)
		defer t.Stop()
		for i := 0; i < 300; i++ {
			udp.WriteTo(wm.StunIndication(), phone)
			select {
			case <-stopPunch:
				return
			case <-t.C:
			}
		}
	}()

	var sr wm.SniffResult
	select {
	case sr = <-sniff.Found:
	case <-time.After(90 * time.Second):
		return errors.New("no ICE check from the phone in 90 s (firewall, client isolation, or Connect not pressed)")
	}
	lg.Printf("first check from phone %s ufrag %s (%s after code)", sr.From, sr.Ufrag, wm.Secs(sr.At.Sub(tCode)))
	if ua, ok := sr.From.(*net.UDPAddr); ok && !ua.IP.Equal(phoneIP) {
		lg.Printf("note: check came from %s, return code said %s", ua, phone)
		phone = ua
	}

	answer := strings.Join([]string{
		"v=0",
		"o=- 2 2 IN IP4 127.0.0.1",
		"s=-",
		"t=0 0",
		"a=group:BUNDLE 0",
		"m=application 9 UDP/DTLS/SCTP webrtc-datachannel",
		"c=IN IP4 0.0.0.0",
		"a=ice-ufrag:" + sr.Ufrag,
		"a=ice-pwd:" + strings.Repeat("x", 24), // unused: a lite agent never sends checks
		"a=fingerprint:sha-256 " + wm.Fingerprint(make([]byte, 32)),
		"a=setup:active",
		"a=mid:0",
		"a=sctp-port:5000",
		fmt.Sprintf("a=candidate:1 1 udp 2130706431 %s %d typ host", phone.IP, phone.Port),
		"",
	}, "\r\n")
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer}); err != nil {
		return fmt.Errorf("remote description: %w", err)
	}

	opened := make(chan struct{})
	msgs := make(chan wm.Msg, 16)
	dc.OnOpen(func() { close(opened) })
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		var x wm.Msg
		if json.Unmarshal(m.Data, &x) == nil {
			msgs <- x
		}
	})
	select {
	case <-opened:
	case <-time.After(20 * time.Second):
		return errors.New("data channel did not open in 20 s")
	}
	lg.Result("flowB.open_ms_after_code=%d", time.Since(tCode).Milliseconds())

	// The phone must prove it holds the token and bind its DTLS certificate.
	tr := wm.DCTransport{DC: dc}
	var hello wm.Msg
	select {
	case hello = <-msgs:
	case <-time.After(10 * time.Second):
		return errors.New("no hello from the phone")
	}
	phoneFP, _ := wm.UnB64(hello.FP)
	mac, _ := wm.UnB64(hello.MAC)
	real := wm.RemoteCertFingerprint(pc)
	if hello.T != "hello" || !hmacEqual(mac, wm.MAC(token, []byte("wm-b-hello"), phoneFP, fp)) {
		return errors.New("hello failed: phone does not hold the token")
	}
	if !hmacEqual(real, phoneFP) {
		return fmt.Errorf("hello failed: DTLS certificate %x does not match the one the phone signed", real)
	}
	sas := wm.SAS(token, phoneFP, fp)
	tr.Send(wm.Msg{T: "hello-ack", V: 1, MAC: wm.B64(wm.MAC(token, []byte("wm-b-ack"), phoneFP, fp)), SAS: sas})
	lg.Say("\n   Αριθμός επιβεβαίωσης / Check number:  %s  (ίδιος με το κινητό / same as the phone)\n", sas)
	lg.Printf("phone authenticated, info: %s", hello.Info)

	pres := wm.NewFakePres()
	last := time.Now()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case m := <-msgs:
			last = time.Now()
			switch m.T {
			case "cmd":
				r := pres.Handle(m)
				if !m.HB && m.Cmd != "ping" {
					lg.Printf("cmd %s -> slide %d/%d", m.Cmd, r.State.Slide, r.State.Total)
				}
				tr.Send(r)
			case "report":
				lg.Result("phone report: %s", m.Info)
			case "bye":
				return errQuit
			}
		case <-tick.C:
			if time.Since(last) > 6*time.Second {
				return errors.New("link lost (no message for 6 s)")
			}
		}
	}
}

// listenUDPHigh binds one UDP socket on all interfaces in 49152-65535.
func listenUDPHigh() (*net.UDPConn, int, error) {
	for i := 0; i < 50; i++ {
		p := 49152 + int(wm.RandBytes(2)[0])<<6 + int(wm.RandBytes(1)[0]&63)
		c, err := net.ListenUDP("udp4", &net.UDPAddr{Port: p})
		if err == nil {
			return c, p, nil
		}
	}
	return nil, 0, errors.New("no free UDP port")
}

func showQR(url string) {
	lg.Say("\nΣκάναρε με την κάμερα του κινητού / Scan with the phone camera:\n\n")
	qrterminal.GenerateWithConfig(url, qrterminal.Config{
		Level: qrterminal.L, Writer: os.Stdout, HalfBlocks: true,
		BlackChar: qrterminal.BLACK_BLACK, WhiteBlackChar: qrterminal.WHITE_BLACK,
		WhiteChar: qrterminal.WHITE_WHITE, BlackWhiteChar: qrterminal.BLACK_WHITE, QuietZone: 2,
	})
	lg.Printf("QR link: %s", url)
	png := filepath.Join(os.TempDir(), "waymark-spike-qr.png")
	if err := qrcode.WriteFile(url, qrcode.Low, 512, png); err == nil {
		lg.Say("Το QR σε εικόνα / QR as an image: %s\n", png)
		wm.OpenFile(png)
	}
}
