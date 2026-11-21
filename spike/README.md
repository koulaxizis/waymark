# Waymark pairing spike (throwaway)

This branch answers one question: can a Controller reach the Agent on a presenter PC
without admin rights, on every network we care about, and how fast?
Nothing here ships. The code stays on `spike/pairing`.

## What is here

| Path | What it does |
| --- | --- |
| `cmd/agent-spike` | Presenter-PC side. Flow A: type the Controller's code. Flow B: show a QR for a phone. Answers `ping`, `state.get` and fake `pres.next/prev/black`. |
| `cmd/controller-spike` | Operator side for flow A. Shows a 12-char code, pairs, opens WebRTC, measures. |
| `web/pairing/index.html` | Phone page for flow B (Chrome on Android). Served from `https://waymarked.site/spike/pairing/`. |
| `internal/wm` | Codes, CPace pairing, encrypted TCP channel, WebRTC helpers, log file. |

### Flow A: Agent dials out (laptop or APK Controller)

1. The Controller listens on a TCP port in 49152–65535 and shows `XXXX-XXXX-XXXX`:
   the last two IPv4 octets (16 bits), the port offset (14 bits) and a one-time secret (30 bits).
   The Agent takes the first two octets from its own interfaces. A 16-char long code with the
   full IPv4 is shown too, for networks where the two machines differ in the first two octets.
2. The Agent connects out and runs CPace (ristretto255) on the secret, then key confirmation.
   Both screens show the same 4-digit check number; the Agent user confirms.
   Three wrong attempts replace the code. A code pairs once.
3. Over the encrypted TCP channel the two sides swap SDP. The Agent is ICE-controlling and sends
   checks first, so its firewall only sees replies.
4. If the data channel is not open in 10 s, commands fall back to the encrypted TCP channel
   and the log says so.
5. Loss: no message for 6 s. The Agent retries every 2 s with the stored pairing key
   (HMAC challenge, valid 24 h); no new code.

### Flow B: phone browser as Controller

1. The Agent opens one UDP port, runs ICE-lite with credentials derived from a 16-byte token,
   and shows a QR: `https://waymarked.site/spike/pairing/#WM1.<ips>.<port>.<dtls-fp>.<token>`.
2. The phone page asks for the camera (Chrome then shows its real IP instead of a `.local` name),
   builds the session locally and shows a 12-char return code: phone IPv4, port and a 12-bit
   token check.
3. The user types the return code on the Agent. The Agent starts sending small STUN indications
   to the phone to open its firewall path, then the user presses Connect on the phone.
4. The phone's ICE username fragment is read from its first check (no room in the code), the
   DTLS fingerprint of the phone is bound in-channel with HMAC(token), and both show a
   4-digit check number.

## Build

```
go test ./...
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/ ./cmd/...
```

## Test plan on the available machines

Agent PC: Windows 10 Pro. Controller: the second PC, and the Xiaomi Redmi (Chrome).
Each run writes `waymark-spike-<role>-<time>.log` next to the exe (or in `%TEMP%`).
Lines starting with `RESULT` are the measurements.

| # | Agent account | Controller | Network | Expect |
| --- | --- | --- | --- | --- |
| S1 | Local Standard user | Second PC (flow A) | Wi-Fi, Public profile | Connects, no firewall prompt on the Agent |
| S2 | Microsoft-account user, Standard | Second PC (flow A) | Wi-Fi, Public | Same as S1 |
| S3 | Local Standard | Second PC (flow A) | Wired switch or router | Same as S1 |
| S4 | Local Standard | Second PC (flow A) | Public; press Cancel on any prompt | Log what still works |
| S8 | Local Standard | Redmi, Chrome (flow B) | Same Wi-Fi | Connects |
| S8b | Local Standard | Redmi, Chrome (flow B) | Redmi hotspot | Connects |
| S9 | Local Standard | Second PC (flow A) | Direct Ethernet cable, 169.254.x.x | Connects |
| S10 | Local Standard | Second PC (flow A) | Guest Wi-Fi with client isolation | Clear failure within 10 s |
| S11 | Local Standard | Second PC (flow A) | Wi-Fi off 30 s on the Agent, then 30 min running | Reconnects without a new code |

Pass: S1–S4, S8, S9 and S11 connect without admin; S10 fails clearly; pairing under 5 s
(not counting typing); next/prev p95 under 150 ms; reconnect under 5 s after the network returns.

Results checked here (Linux, one host, simulated stateful firewall that drops new inbound UDP
on the Agent side): flow A opened in 6 ms with the Controller's first check dropped; flow B
opened in 57 ms after Connect with no inbound packet dropped; reconnect 2.5 s after the network
returned. Real Windows Defender Firewall behaviour is what S1–S4 must show.
