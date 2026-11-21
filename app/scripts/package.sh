#!/bin/sh
# Builds a portable desktop package on the matching OS (CGO and the system
# WebView are needed, so there is no cross-build):
#   scripts/package.sh linux  0.1.0   -> dist/WaymarkRemote-0.1.0-linux-x86_64.AppImage
#   scripts/package.sh macos  0.1.0   -> dist/WaymarkRemote-0.1.0-macos.zip (universal .app)
# The AppImage uses the system's WebKitGTK 4.1 (most current distributions
# have it). The .app is not notarized: the first start is right-click > Open.
set -eu
os=$1
ver=$2
cd "$(dirname "$0")/.."
mkdir -p dist
build=$(mktemp -d)
flags="-s -w -X main.Version=$ver"
icon=cmd/waymark-remote/winres/icon256.png

case "$os" in
linux)
	go build -trimpath -tags desktop,production,webkit2_41 -ldflags "$flags" -o "$build/waymark-remote" ./cmd/waymark-remote
	app="$build/AppDir"
	mkdir -p "$app/usr/bin"
	cp "$build/waymark-remote" "$app/usr/bin/"
	cp "$icon" "$app/waymark-remote.png"
	cat > "$app/waymark-remote.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=Waymark Remote
Comment=Presentation and file control over the local network
Exec=waymark-remote
Icon=waymark-remote
Categories=Office;Presentation;
Terminal=false
EOF
	cat > "$app/AppRun" <<'EOF'
#!/bin/sh
exec "$(dirname "$(readlink -f "$0")")/usr/bin/waymark-remote" "$@"
EOF
	chmod +x "$app/AppRun"
	tool="$build/appimagetool"
	curl -fsSL -o "$tool" https://github.com/AppImage/appimagetool/releases/download/1.9.0/appimagetool-x86_64.AppImage
	chmod +x "$tool"
	ARCH=x86_64 "$tool" --appimage-extract-and-run "$app" "dist/WaymarkRemote-$ver-linux-x86_64.AppImage"
	;;
macos)
	for a in arm64 amd64; do
		CGO_ENABLED=1 GOARCH=$a go build -trimpath -tags desktop,production -ldflags "$flags" -o "$build/wm-$a" ./cmd/waymark-remote
	done
	app="$build/Waymark Remote.app"
	mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
	lipo -create -output "$app/Contents/MacOS/waymark-remote" "$build/wm-arm64" "$build/wm-amd64"
	set_icon="$build/icon.iconset"
	mkdir -p "$set_icon"
	for s in 16 32 128 256 512; do
		sips -z $s $s "$icon" --out "$set_icon/icon_${s}x${s}.png" >/dev/null
		d=$((s * 2))
		sips -z $d $d "$icon" --out "$set_icon/icon_${s}x${s}@2x.png" >/dev/null
	done
	iconutil -c icns -o "$app/Contents/Resources/icon.icns" "$set_icon"
	cat > "$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>Waymark Remote</string>
	<key>CFBundleDisplayName</key><string>Waymark Remote</string>
	<key>CFBundleIdentifier</key><string>site.waymarked.remote</string>
	<key>CFBundleVersion</key><string>$ver</string>
	<key>CFBundleShortVersionString</key><string>$ver</string>
	<key>CFBundleExecutable</key><string>waymark-remote</string>
	<key>CFBundleIconFile</key><string>icon</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>LSMinimumSystemVersion</key><string>11.0</string>
	<key>NSHighResolutionCapable</key><true/>
	<key>NSAppleEventsUsageDescription</key><string>Waymark Remote sends the slide keys to Keynote, PowerPoint or LibreOffice when the operator asks.</string>
	<key>NSLocalNetworkUsageDescription</key><string>Waymark Remote connects the operator and the presenter laptop on this network.</string>
</dict>
</plist>
EOF
	# Ad-hoc signature so Apple silicon runs it; not notarized.
	codesign --force --deep --sign - "$app"
	ditto -c -k --keepParent "$app" "dist/WaymarkRemote-$ver-macos.zip"
	;;
*)
	echo "usage: $0 linux|macos VERSION" >&2
	exit 2
	;;
esac
rm -rf "$build"
ls -la dist
