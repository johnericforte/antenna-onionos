# Antenna

[![ci](https://github.com/johnericforte/antenna-onionos/actions/workflows/ci.yml/badge.svg)](https://github.com/johnericforte/antenna-onionos/actions/workflows/ci.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Antenna is a streaming video app for the Miyoo Mini Plus running OnionOS. It pulls video over WiFi from the Internet Archive, so the only thing that lands on your SD card is the app.

## Install on OnionOS

There's no release up yet, so there's nothing to download from that page today. These are the steps for when there is.

Get WiFi working on the handheld first. Antenna pulls everything over the network.

1. On the handheld, open **Apps**, then **Package Manager**, and install **Video Player (FFplay)**. Antenna hands playback over to it rather than shipping its own decoder.
2. Power off and pull the SD card. Put it in your computer.
3. Download `Antenna.zip` from [Releases](https://github.com/johnericforte/antenna-onionos/releases).
4. Unzip it into the `App` folder on the card. You want `App/Antenna/` holding `antenna`, `launch.sh`, `config.json`, `items.txt`, `cacert.pem` and `icon.png`.
5. Eject the card, put it back in the handheld, and reboot.
6. Antenna is under **Apps**.

If it doesn't show up, check whether the folder ended up nested a level too deep at `App/Antenna/Antenna/`. That's usually what happened.

### Controls

| Button | Action |
|---|---|
| D-pad up / down | Move selection |
| A | Play the selected title |
| L1 / R1 | Page up / down |
| B or MENU | Exit to the Onion menu |

## Config

Antenna plays whatever you point it at. The built-in list is public domain animation, but nothing in the app knows that, and swapping it is the main thing you'll want to change.

### Choosing what it plays

Edit `App/Antenna/items.txt` on the card with any text editor. One archive.org item per line: the identifier, a space, then the name you want on screen.

```
classic_cartoons_201603 Classic Cartoons
disneycartoons-publicdomain Disney Public Domain
pdcartooncollection Public Domain Cartoons
```

The name is optional. Leave it off and the identifier shows instead. Lines starting with `#` are ignored, and the order in the file is the order in the app, so put what you watch most at the top.

**Finding an identifier.** It's the last part of an archive.org details URL. For `https://archive.org/details/classic_cartoons_201603` the identifier is `classic_cartoons_201603`. Paste the identifier, not the URL.

**An item is a folder of videos, not one video.** Antenna lists every title inside it and quietly leaves out the ones this device cannot decode.

Delete `items.txt` to go back to the built-in list. A line the app cannot read stops it with the file name and line number on screen, rather than silently falling back and hiding your edit.

### Why some titles do not appear

The Miyoo Mini Plus decodes H.264 in software on two Cortex-A7 cores, which works out to around 480p at 1 Mbps. Anything heavier is left out of the list rather than shown and then stuttered through.

How much that costs you depends entirely on the item. Measured on 2026-09-06 across the three that ship by default:

| Item | Titles | Playable |
|---|---|---|
| `classic_cartoons_201603` | 12 | 12 |
| `disneycartoons-publicdomain` | 20 | 6 |
| `pdcartooncollection` | 38 | 13 |

Items with an h.264 derivative on every title work best. Items holding only large original uploads mostly will not play, and there is nothing the app can do about that.

## Development

Go 1.22 or newer. No third-party modules, and none may be added.

```sh
make          # list the targets
make ci       # fmt, vet, lint, test, cross-compile. What CI runs.
make package  # build and zip the installable App folder
```

`make package` needs the `zip` binary.

### Logs

The app writes to `App/Antenna/antenna.log` on the card. `launch.sh` sends both streams there, caps the file at 256 KB and rotates it, because a handheld has no console and that file is the only way to see what happened.

**Off by default.** A release build records failures only, which is what a user has to send when something breaks.

To record every step instead, put a file named `debug` in `App/Antenna/` on the card. `launch.sh` sees it and sets `ANTENNA_DEBUG=1`. Delete the file to go quiet again. Setting the environment variable directly works too.

With tracing on, the log carries the screen geometry, every button press, the resolved stream URL and bitrate, the ffplay command line, and whatever ffplay printed:

```
play: selected 6/31 id="classic_cartoons_201603/BugsBny.mp4" title="BugsBny"
play: stream progressive H.264 640x480 827541 bps url=https://dn600209...
player: exec /mnt/SDCARD/.tmp_update/bin/ffplay ["-hide_banner" ...]
player: exit after 640ms: err=<nil> state=exit status 0 stderr="..."
```

### How playback works

Antenna decodes nothing itself. It resolves a title to a direct URL and hands that to ffplay.

The ffplay OnionOS ships has no TLS support, and archive.org is HTTPS only, so a stream URL cannot be given to it directly. Handed one, it prints `Protocol not found` and **exits with status zero**, which makes a dead video look like a watched one. So `internal/relay` terminates TLS in Go and serves the stream to ffplay over `http://127.0.0.1` on a kernel-assigned port, forwarding Range headers so seeking still works. Nothing leaves the device, and the relay lives only as long as the video.

For the same reason, ffplay's exit code is not trusted on its own. Its stderr is scanned for fatal lines regardless of how it exited.

### Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Status

Browsing works on hardware. Playback is written and the fixes above came from real device logs, but a video has not yet been watched start to finish on the handheld.

## Content and licensing

Antenna ships no video and hosts nothing. The built-in provider browses what the Internet Archive already holds. Public domain status is per title, and an item sitting on archive.org does not prove it. Restored or colorized versions can carry fresh rights, and musical cues are often licensed separately from the animation.

Antenna is MIT licensed. See [LICENSE](LICENSE).
