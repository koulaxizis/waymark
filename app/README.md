# Waymark Remote

Portable app for conferences: the operator opens and drives the presentation on the presenter laptop and moves files to it, over the local network, without admin rights and without any server. Part of [Waymark](https://waymarked.site). MIT.

The design and every decision live in the Waymark plan document. This folder holds the code of version 0.1 (presentation and files).

## Layout

| Path | What |
|---|---|
| `cmd/waymark-remote` | The Wails app (Go + plain HTML/CSS/JS in `frontend/`) |
| `internal/code` | The typed pairing codes (Crockford base32) |
| `internal/secure` | Frames, AES-256-GCM channel, key helpers |
| `internal/pair` | Pairing: Controller listens, Agent dials out, CPace, resume |
| `internal/proto` | Messages, commands, error codes, roles and what each may do |
| `internal/files` | The Agent folder: path checks, resumable uploads, Recycle Bin |
| `internal/pres` | Presentation drivers: PowerPoint COM, Impress, system viewer |
| `internal/agent` | Agent logic: roles, control key, state reports |
| `internal/controller` | Controller logic: links, calls, uploads, downloads |
| `internal/rtc` | Flow B: the Agent side of the browser Controller (QR, return code, ICE-lite WebRTC) |
| `../remote` | The browser Controller page, served at waymarked.site/remote/ |
| `android` | The Android app `site.waymarked.app`, a WebView shell around the same page |
| `e2e` | The browser page in headless Chromium against a real Agent |

## Build

Go 1.24 or newer. The Windows exe cross-compiles from any OS without CGO:

```sh
cd app
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -tags desktop,production \
  -ldflags "-s -w -H windowsgui -X main.Version=0.1.0" -o WaymarkRemote.exe ./cmd/waymark-remote
```

`rsrc_windows_amd64.syso` carries the icon and the manifest (runs as invoker, never asks for admin). Rebuild it after changing `winres/` with `go-winres make --in winres/winres.json --out rsrc --arch amd64`.

Tests: `go test -race ./internal/...` (includes an Agent ↔ Controller run on loopback). The browser test needs Node and Playwright: `go test -tags browser ./e2e`.

`remote/` shares `i18n.js`, `style.css` and `palette.css` with the desktop app. After changing them run `scripts/sync-web.sh`; CI checks the copies match.

Android: JDK 17 and the Android SDK, then `cd android && ./gradlew assembleDebug`. The build copies `remote/` into the APK. CI uploads a debug APK for every pull request.

## Release

Push a tag `vX.Y.Z`. The release workflow builds the exe and publishes it with `SHA256SUMS.txt`. It adds the signed APK when the repository has the secrets `WAYMARK_KEYSTORE_B64` (the keystore, base64), `WAYMARK_KEYSTORE_PASSWORD`, `WAYMARK_KEY_ALIAS` and `WAYMARK_KEY_PASSWORD`; the same APK then goes into the oros-fdroid repo. The update notice in the app offers only the latest stable release.

## Third-party code

- [pdf.js](https://github.com/mozilla/pdf.js) 4.10.38, Apache-2.0, in `cmd/waymark-remote/frontend/vendor/pdfjs/`.
- [jsQR](https://github.com/cozmo/jsQR) 1.4.0, Apache-2.0, in `../remote/vendor/`.
- Go modules listed in `go.mod` (Wails MIT, Pion MIT, filippo.io/cpace BSD-3, go-ole MIT, golang.org/x BSD-3).
