# Antenna

[![ci](https://github.com/johnericforte/antenna-onionos/actions/workflows/ci.yml/badge.svg)](https://github.com/johnericforte/antenna-onionos/actions/workflows/ci.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Antenna is a streaming video app for the Miyoo Mini Plus running OnionOS. You point it at what you want to watch and it pulls the video over WiFi, so the only thing that lands on your SD card is the app.

**Still being built.** It plays video on real hardware, and the parts described below work, but this is active development rather than a finished thing. Expect rough edges, and expect some of it to change.

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
| D-pad up / down | Move selection, hold to scroll |
| L1 / R1 | Jump to the next or previous letter |
| A | Play the selected title |
| SELECT | Search |
| START | Open settings |
| B or MENU | Exit to the Onion menu |

**During a video**, the controls are ffplay's own. Antenna hands playback to the **Video Player (FFplay)** package from Onion's Package Manager and waits for it, so seeking, pausing and quitting are whatever that build of ffplay binds them to, not something Antenna defines. Quitting the video returns you to the list rather than to the Onion menu.

## Config

Antenna plays whatever you point it at. The built-in list is public domain animation, but nothing in the app knows that, and swapping it is the main thing you'll want to change.

### Choosing what it plays

Edit `App/Antenna/items.txt` on the card with any text editor. One source per line: the reference, a space, then the name you want on screen.

The shape of the reference decides how it's read, so wiring in your own video takes one line and no code.

```
classic_cartoons_201603 Classic Cartoons
https://download.blender.org/durian/trailer/sintel_trailer-480p.mp4 Sintel Trailer
https://example.org/my/list.m3u My List
```

**An archive.org identifier** is the last part of a details URL. For `https://archive.org/details/classic_cartoons_201603` the identifier is `classic_cartoons_201603`. Paste the identifier, not the URL. An item is a folder of videos, so Antenna lists every title inside it and leaves out the ones this device cannot decode.

**A direct video URL** is played as a single title, with no metadata lookup at all. Both of these were checked against the live servers and work as written:

```
https://download.blender.org/durian/trailer/sintel_trailer-480p.mp4 Sintel Trailer
https://test-videos.co.uk/vids/bigbuckbunny/mp4/h264/360/Big_Buck_Bunny_360_10s_1MB.mp4 Big Buck Bunny
```

**A playlist URL** ending `.m3u` or `.txt` is fetched and expanded into the videos it names. Relative paths resolve against the playlist's own address, and an `#EXTINF` title is used when there is one. A plain list of URLs, one per line, works too, which is the format you would write by hand:

```
#EXTM3U
#EXTINF:212,First Film
https://example.org/films/first.mp4
../films/second.mp4
```

`.m3u8` is deliberately not treated as a playlist. That is HLS, whose playlists list a few seconds of video each rather than whole titles, and this device cannot play HLS at all. An `.m3u8` line stays a single source and is refused with a reason, instead of filling your list with hundreds of unplayable fragments.

A playlist that turns out to be a web page, which is what a server answering an error with a 200 usually sends, is reported rather than shown as a screen of nonsense titles.

The name after a reference is optional. Leave it off and Antenna uses the identifier, or the file name for a URL. Lines starting with `#` are ignored, and the order in the file is the order in the app, so put what you watch most at the top.

Delete `items.txt` to go back to the built-in list. A line the app cannot read stops it with the file name and line number on screen, rather than silently falling back and hiding your edit.

**Sizes are only checked when the source says what they are.** archive.org publishes the dimensions and bitrate of every file, so unplayable titles are left out of the list before you ever pick one. A URL you supply yourself carries no such description, so Antenna plays what it is pointed at. The device limits still apply: an oversized video stutters instead of being refused.

### Search

Press **SELECT** in the list. An on-screen keyboard appears and the list narrows as you type, with a count in the corner showing how many titles still match, so you can tell whether to keep typing without leaving the keyboard.

| Button | Action |
|---|---|
| D-pad | Move around the grid |
| A | Type the key under the cursor |
| B | Delete the last character |
| START | Done, keeping the filter |
| SELECT | Cancel, restoring the whole list |

Matching is case insensitive and matches anywhere in a title, so `bunny` finds "Bugs Bunny". The query is remembered, so reopening the search lets you correct it rather than retype it. This is Antenna's own keyboard, laid out like the one in the terminal OnionOS ships, since there is no system keyboard an app can call.

### Settings

Press **START** in the list. B goes back.

| Setting | What it does |
|---|---|
| Trace logging | Turns the trace log on or off. Takes effect at once and survives a reboot, so you can capture a log without pulling the card. |
| Reload sources | Browses every source again. This is the fix when WiFi was not up at startup and the list came back empty. |

Choices are written to `App/Antenna/settings.txt`, next to the item list, as plain `key=value` lines you can also edit by hand. A line the app does not recognise is reported in the log and skipped rather than stopping the app, because a stray line in a settings file is not worth refusing to start over.

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

Three ways to turn the trace on, in the order you would reach for them:

1. **In the app:** START, then Trace logging. Survives a reboot, and needs no card.
2. **A `debug` file** in `App/Antenna/` on the card. `launch.sh` sees it and sets `ANTENNA_DEBUG=1`.
3. **`ANTENNA_DEBUG=1`** in the environment.

The saved setting wins over the environment, since it is the choice made most recently and the only one changeable without the card.

With tracing on, the log carries the screen geometry, every button press, the resolved stream URL and bitrate, the ffplay command line, and whatever ffplay printed:

```
play: selected 6/31 id="classic_cartoons_201603/BugsBny.mp4" title="BugsBny"
play: stream progressive H.264 640x480 827541 bps url=https://dn600209...
player: exec /mnt/SDCARD/.tmp_update/bin/ffplay ["-hide_banner" ...]
player: exit after 640ms: err=<nil> state=exit status 0 stderr="..."
```

### How playback works

Antenna decodes nothing itself. It resolves a title to a direct URL and hands that to ffplay.

There is one provider. The shape of each line in `items.txt` decides how that source is read, rather than the user choosing between implementations. Adding a kind of source means teaching `internal/config` one more line shape and `internal/provider` how to browse it.

The ffplay OnionOS ships has no TLS support, and archive.org is HTTPS only, so a stream URL cannot be given to it directly. Handed one, it prints `Protocol not found` and **exits with status zero**, which makes a dead video look like a watched one. So `internal/relay` terminates TLS in Go and serves the stream to ffplay over `http://127.0.0.1` on a kernel-assigned port, forwarding Range headers so seeking still works. Nothing leaves the device, and the relay lives only as long as the video.

For the same reason, ffplay's exit code is not trusted on its own. Its stderr is scanned for fatal lines regardless of how it exited.

### Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Status

Browsing works on hardware. Playback is written and the fixes above came from real device logs, but a video has not yet been watched start to finish on the handheld.

## Content and licensing

Antenna ships no video and hosts nothing. The built-in provider browses what the Internet Archive already holds. Public domain status is per title, and an item sitting on archive.org does not prove it. Restored or colorized versions can carry fresh rights, and musical cues are often licensed separately from the animation.

Antenna is MIT licensed. See [LICENSE](LICENSE).
