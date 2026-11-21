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

## Build

Go 1.24 or newer. The Windows exe cross-compiles from any OS without CGO:

```sh
cd app
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -tags desktop,production \
  -ldflags "-s -w -H windowsgui -X main.Version=0.1.0" -o WaymarkRemote.exe ./cmd/waymark-remote
```

`rsrc_windows_amd64.syso` carries the icon and the manifest (runs as invoker, never asks for admin). Rebuild it after changing `winres/` with `go-winres make --in winres/winres.json --out rsrc --arch amd64`.

Tests: `go test -race ./internal/...` (includes an Agent ↔ Controller run on loopback).

## Release

Push a tag `vX.Y.Z`. The release workflow builds the exe and publishes it with `SHA256SUMS.txt`. The update notice in the app offers only the latest stable release.

## Third-party code

- [pdf.js](https://github.com/mozilla/pdf.js) 4.10.38, Apache-2.0, in `cmd/waymark-remote/frontend/vendor/pdfjs/`.
- Go modules listed in `go.mod` (Wails MIT, Pion MIT, filippo.io/cpace BSD-3, go-ole MIT, golang.org/x BSD-3).
