# Termux on the Boox

Run booxd in the foreground on Android 11. Device verification is still pending:
the build alone does not prove DNS or the Android reader handoff works.

## Install Termux from F-Droid

Install [Termux (com.termux)](https://f-droid.org/packages/com.termux/) from
F-Droid. This is the supported build; the Play Store branch has different
compatibility constraints. Different signing keys prevent mixing builds.
Back up existing Termux data before uninstalling another build.

The F-Droid build targets SDK 28, allowing execution from Termux's private
directory on Android 10 and later. The bootstrap includes termux-tools;
running a prebuilt booxd requires no additional Termux packages.

## Give Termux shared storage

Run `termux-setup-storage` and grant the storage permission prompt.
It creates symlinks under `~/storage`: `~/storage/downloads` points to
`/sdcard/Download`, and `~/storage/shared` points to `/sdcard`.
If Android 11 still reports permission denied, revoke and grant Termux's
storage permission again.

## Put the binary in ~/bin

A builder runs `make android` with Go 1.25 or newer in this repository.
Download the resulting `dist/booxd-android-arm64` onto the Boox using its
browser or copy it to Downloads. No hosted release artifact is assumed here.
Shared storage cannot execute this binary; copy it into Termux's private home:

```sh
mkdir -p ~/bin
cp ~/storage/downloads/booxd-android-arm64 ~/bin/booxd
chmod +x ~/bin/booxd
echo 'export PATH="$HOME/bin:$PATH"' >> ~/.bashrc
. ~/.bashrc
command -v booxd
```

Termux's default Bash session reads `~/.bashrc`; new sessions pick up this
PATH addition. Do not assume `~/bin` is already on PATH.
See [Configuration](../README.md#configuration) for download directory settings.

## Disable cursor blink

In `~/.termux/termux.properties`, set:

```properties
terminal-cursor-blink-rate = 0
```

Create `~/.termux` if needed. Zero is already the default; keep it disabled.
Run `termux-reload-settings` after editing. The e-ink panel must not repaint
on a timer, including the terminal cursor. See [the redraw policy](eink.md).

## Stop Android freezing Termux

On Boox, open Apps, long-press Termux, then choose
Optimize > Others > Stay active in the background. Enable it.
Check App Management > Freeze Settings so Termux is not frozen.

On generic Android 11, use Settings > Apps > Special app access >
Battery optimization > All apps > Termux > Don't optimize.
Labels can vary with firmware. Background activity costs battery; use this
setting to keep a foreground download running while you watch it.

## Open a book

Supply an existing Netscape session cookie file; booxd never obtains a session.
Put every flag before the book ID:

```sh
booxd get --cookies cookies.txt --open BOOK_ID
```

Replace BOOK_ID with the chosen result's ID. After saving the file or finding it
already downloaded, booxd calls `termux-open`. It sends Android an ACTION_VIEW
intent with a content URI and the extension's MIME type, such as
`application/epub+zip`. Android selects the default reader for that file type.
A particular reader is not guaranteed. The command can exit successfully even
when no app handles the file.

## Why there is no daemon

Android reaps background processes on this device. Each booxd command runs in
the foreground, prints its result, and exits. There is no daemon, service,
socket listener, or scheduled job.

## Verify

Run these on the Boox; this check remains pending until tested on real hardware:

```sh
command -v booxd
echo "$TERM"
command -v termux-open
booxd search "crime and punishment"
booxd get --cookies cookies.txt --open BOOK_ID
cmd package query-activities --brief -a android.intent.action.VIEW -t application/epub+zip
```

Expect TERM to be `xterm-256color` and termux-open to be under `$PREFIX/bin`.
The binary includes terminfo for that terminal. The final Android command lists
EPUB handlers. Confirm the reader opens the saved book.

## Troubleshooting

- `booxd: cannot open …: termux-open not found`: run `pkg install termux-tools`.
- Nothing opens: check the handler list above and select a default reader.
- DNS lookup errors: booxd uses the first valid nameserver IP in
  `$PREFIX/etc/resolv.conf` (default prefix `/data/data/com.termux/files/usr`).
  A missing or invalid file falls back to `8.8.8.8`. Check that file and server
  reachability. This public fallback ignores network DNS and may fail on captive
  portals or networks blocking outside DNS; edit the file to choose another server.
