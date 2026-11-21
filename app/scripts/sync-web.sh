#!/bin/sh
# Copies the files the browser Controller (remote/ on the site) shares with
# the desktop app. Run after changing them; CI fails when the copies differ.
set -e
root=$(cd "$(dirname "$0")/../.." && pwd)
src="$root/app/cmd/waymark-remote/frontend"
for f in i18n.js style.css palette.css; do
  if [ "$1" = "--check" ]; then
    cmp -s "$src/$f" "$root/remote/$f" || { echo "remote/$f differs from the app copy: run app/scripts/sync-web.sh" >&2; exit 1; }
  else
    cp "$src/$f" "$root/remote/$f"
  fi
done
