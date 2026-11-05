# Termux on the Boox

Run booxd in the foreground on Android 11.

## Install Termux from F-Droid

Install [Termux (com.termux)](https://f-droid.org/packages/com.termux/) from
F-Droid. This is the supported build; the Play Store branch has different
compatibility constraints. Different signing keys prevent mixing builds.
Back up existing Termux data before uninstalling another build.

The F-Droid build targets SDK 28, allowing execution from Termux's private
directory on Android 10 and later. The bootstrap includes `curl` and
`termux-tools`; installing booxd requires no additional Termux packages.

## Give Termux shared storage

Run `termux-setup-storage` and grant the storage permission prompt.
It creates symlinks under `~/storage`: `~/storage/downloads` points to
`/sdcard/Download`, and `~/storage/shared` points to `/sdcard`.
If Android 11 still reports permission denied, revoke and grant Termux's
storage permission again.

## Install booxd

Install from inside Termux. The installer puts booxd in `$PREFIX/bin`, which is
already on `PATH`; it does not edit a shell startup file.

```sh
curl -fsSL https://github.com/curedbylethe/booxd/releases/latest/download/install.sh | sh
booxd version
```

`/sdcard` and `/storage/emulated/0` are mounted `noexec`. If a binary is
downloaded there, copy it into Termux's private storage before running it.
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

Run these on the Boox after installation:

```sh
booxd version
booxd search "crime and punishment"
```

The version line should end in `(android/arm64)`. Search should return results.
See [Open a book](#open-a-book) to verify NeoReader handoff.

## Troubleshooting

- `booxd: cannot open …: termux-open not found`: run `pkg install termux-tools`.
- Nothing opens: check the handler list above and select a default reader.
- DNS lookup errors: booxd uses the first valid nameserver IP in
  `$PREFIX/etc/resolv.conf` (default prefix `/data/data/com.termux/files/usr`).
  A missing or invalid file falls back to `8.8.8.8`. Check that file and server
  reachability. This public fallback ignores network DNS and may fail on captive
  portals or networks blocking outside DNS; edit the file to choose another server.
