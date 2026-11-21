# Changelog

All notable changes to Waymark Remote. Versions follow `MAJOR.MINOR.PATCH`.

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
- Controller in any browser (phone, tablet, laptop) at waymarked.site/remote/, nothing to install: the Agent shows a QR, the page shows a return code, they connect over an encrypted WebRTC data channel on the local network. Same roles, control key, presentation pad and files as the desktop Controller. The page loads once from the site; commands and files never pass through it. A browser that loses the link pairs again with a new QR.
- Android app `site.waymarked.app`: the same page bundled in the APK, works offline, scans the QR, saves downloads into Downloads/Waymark, keeps the screen on while connected.
- Linux AppImage (uses the system WebKitGTK 4.1) and macOS universal .app (not notarized: first start with right-click > Open). There the drivers are the built-in PDF viewer, LibreOffice Impress and the system viewer; keystrokes through System Events on macOS (Accessibility permission once) and xdotool on Linux X11. Wayland allows no keystrokes: use the built-in viewer.
- Greek and English, dark and light theme (pine green palette v1).
