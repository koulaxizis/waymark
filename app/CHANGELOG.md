# Changelog

All notable changes to Waymark Remote. Versions follow `MAJOR.MINOR.PATCH`.

## 0.1.2 (unreleased)

- Bluetooth as the last way (Proposal 14): when a phone shares its connection over Bluetooth and the computer joins it, Waymark takes that network as a Bluetooth path. The order is cable, Wi-Fi, mobile data, relay, Bluetooth; Bluetooth is tried only when the others fail, and the link moves back as soon as a better path answers. Both ends show "over Bluetooth".
- Bluetooth link between two computers (Proposal 14): the Controller opens a Bluetooth serial channel (RFCOMM) next to its network port, and paired Agents learn its address over the encrypted link. When cable, Wi-Fi, mobile data and the relay all fail, the Agent reconnects over Bluetooth, and moves back as soon as a network answers. Windows and Linux, no admin rights; the two computers must be paired once in the system's Bluetooth settings. About 100 to 250 KB/s within some 10 m: enough for slide commands, slow for large files, which resume where they stopped. macOS and the phone come later.
- Bluetooth clicker in the Android app (Proposal 14): the phone becomes a Bluetooth keyboard for the presentation computer, with Next, Previous, Start, Black screen and End (Page Down, Page Up, F5, B, Esc). It works with any presentation program, with no Agent and no network. While connected, the volume buttons change slides. Pair once from the computer's Bluetooth settings. Android 9 or later, on phones that keep the Bluetooth keyboard profile; it asks for the "Nearby devices" permission.
- Builds use Go 1.26.11 (toolchain line in go.mod; was 1.26.0, eleven security releases behind) and pion/webrtc v4.2.29 (weekly repo health 2026-11-24, findings 1 and 3).
- The update check asks first: on the first start the app asks whether to look for new stable versions, because the check sends one request to GitHub, which sees the IP address. Until the answer, and after a no, nothing is sent. Settings keeps the switch. Earlier versions had it on without asking; they are asked once too (compliance audit 2026-11-23, proposal 5).
- The settings file `waymark-remote.json` and the auto-send ledger are written readable by the owner only (0600 on macOS and Linux), since the settings can hold the relay access key. Settings explains that the key is stored unencrypted next to the app (proposal 6).

## 0.1.1 (unreleased)

- Relay for when the computers share no network (Proposal 13): the last path after cable, Wi-Fi and mobile data. Off by default. Set its address and access key once in the Controller's Settings; paired Agents learn it over the encrypted link. The relay joins two WebSockets and sees only the encrypted channel. The link moves back to a network as soon as one answers. Both ends show "over relay (internet)".
- Your own relay in one step: a Cloudflare Worker with a Deploy button (`app/relay-worker`, free plan) or the `waymark-relay` program for your own server, in the Releases.
- The phone does not use the relay yet (planned for 0.1.2).
- Android app: the page no longer draws under the status bar on Android 15 (edge to edge); the first fix padded the WebView, which draws over its own padding, so the padding now goes on a frame around it. The web page also keeps clear of the status bar and home bar on phones that draw edge to edge.

## 0.1.0 (unreleased)

First version: presentation and files.

- One portable app (`WaymarkRemote.exe`, no installer, no admin) that runs as Agent on the presenter laptop or as Controller with the role operator, secretariat or supervisor.
- Pairing with a 12-character code: the Controller listens, the Agent dials out, so the presenter laptop never opens a port. CPace key exchange, 4-digit check number, the presenter accepts and may lower the role. Reconnects on its own for 24 hours.
- Encrypted channel (AES-256-GCM), closed command list, roles checked by the Agent on every command, one holder of the control key, the supervisor can always take it, "Disconnect all" on the Agent.
- Presentation: PowerPoint through COM on Windows (all commands, slide n/N), built-in PDF viewer with pdf.js (all commands, n/N, full screen on the second screen), LibreOffice Impress and the system viewer with keystrokes.
- Files: folder tree, upload of files and folders with drag and drop, resume after a drop, SHA-256 check, never overwrites (" (2)"), delete to the Recycle Bin, download, rename, new folder. The file in use cannot be renamed or deleted.
- Operator auto-send folder per Agent (Proposal 6), one way only.
- Keeps the laptop awake during a show; asks on close whether to delete received files.
- New stable version notice from GitHub Releases, off in Settings; never during a show; no automatic install.
- Controller in any browser (phone, tablet, laptop) at waymarked.site/remote/, nothing to install: the Agent shows a QR, the page shows a return code, they connect over an encrypted WebRTC data channel on the local network. Same roles, control key, presentation pad and files as the desktop Controller. The page loads once from the site; commands and files never pass through it. A browser that loses the link comes back by itself for 24 hours, after a reload too, with no new QR.
- Android app `site.waymarked.app`: the same page bundled in the APK, works offline, scans the QR, saves downloads into Downloads/Waymark, keeps the screen on while connected.
- Linux AppImage (uses the system WebKitGTK 4.1) and macOS universal .app (not notarized: first start with right-click > Open). There the drivers are the built-in PDF viewer, LibreOffice Impress and the system viewer; keystrokes through System Events on macOS (Accessibility permission once) and xdotool on Linux X11. Wayland allows no keystrokes: use the built-in viewer.
- A link that does not drop (Proposal 13): the devices use every network they share, cable first, then Wi-Fi, then mobile data, and move to a better one when it comes back. A 1-second heartbeat finds a lost link in 3 seconds; a slide command made during a drop is sent again on the new link and runs once, or is refused at once on the phone, never late. The phone comes back without a new QR; if the laptop's firewall blocks it, the phone shows a code to type on the Agent. Both ends show the network in use ("over cable", "over Wi-Fi", "over mobile data").
- Greek and English, dark and light theme (pine green palette v1).
