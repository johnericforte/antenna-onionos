# Antenna

[![ci](https://github.com/johnericforte/antenna-onionos/actions/workflows/ci.yml/badge.svg)](https://github.com/johnericforte/antenna-onionos/actions/workflows/ci.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A video app for the Miyoo Mini Plus running OnionOS.

Antenna hosts nothing on its own. You wire in a provider, it plays whatever that provider hands back. The Internet Archive ships as the built-in one, pointed at public-domain classic cartoons.

## Why I built it

I wanted to watch old cartoons on my Miyoo Mini Plus. That was the whole idea. The thing has WiFi and a 640x480 screen, which lines up almost exactly with the 4:3 shorts from the 1930s, so it felt like something that should already exist.

It didn't, so I started building.

Partway through writing the provider layer I noticed the app doesn't care what it's playing. Nothing in the code knows about cartoons. It knows about a source that can hand it a small video stream. Point it somewhere else and it behaves the same way, which is why it isn't called Cartoons.

## Status

Milestone 1, the walking skeleton. It renders, scrolls, and exits cleanly back to the Onion menu. No network yet, so the list you see is placeholder data.

## Install

1. Install the **Video Player (FFplay)** package from Onion's Package Manager (Apps, then Package Manager). Antenna hands playback to it instead of shipping a decoder. You'll need this from Milestone 4 onward.
2. Grab the latest `Antenna.zip` from Releases.
3. Unzip it into `/mnt/SDCARD/App/`, so you end up with `/mnt/SDCARD/App/Antenna/`.
4. Refresh the Apps list, or just reboot. Antenna shows up under **Apps**.

## Controls

| Button | Action |
|---|---|
| D-pad up / down | Move selection |
| L1 / R1 | Page up / down |
| B or MENU | Exit to the Onion menu |

## Build

Go 1.22 or newer. No third-party modules.

```sh
make          # list the targets
make ci       # fmt, vet, lint, test, cross-compile. Same as CI.
make package  # build and zip the installable App folder
```

`make ci` runs what CI runs, in the same order, so a red build is always reproducible on your machine. `./build.sh` is still there if you'd rather skip Make.

## How it works

It's one static binary. `CGO_ENABLED=0` and the Go standard library only, so nothing lands in `libs/` and there's no OpenSSL to cross-compile against OnionOS's userland.

Drawing goes straight to `/dev/fb0` as BGRA8888. The panel is mounted upside down, so `Present` rotates the buffer while it blits, which is the same reason OnionOS video passes `-vf hflip,vflip` to ffplay. Text uses a 5x7 bitmap font I wrote into `internal/fb/font.go`, since the standard library has no rasterizer and I didn't want the dependency.

Providers plug in, and they all pass the same gate. `internal/provider` defines a `Provider` interface, and `Stream.Playable()` checks every stream against the device limits regardless of where it came from. That gate sits below the interface on purpose: whether something plays is a fact about this chip, so a provider doesn't get to opt out of it.

There's no transcoding anywhere. The device decodes H.264 in software on two Cortex-A7 cores at 1.2GHz with 128MB of RAM, which works out to roughly 480p at 1 Mbps, progressive. Anything heavier gets refused with a reason on screen instead of stuttering through.

That ceiling is what decides which sources are usable, not licensing. Archives and self-hosted libraries fit inside it. Most commercial platforms don't, since they moved to HLS and DRM at bitrates this chip can't decode.

Nothing here depends on a server I run. Everything ships in this repo or comes from the provider directly.

## Roadmap

| | |
|---|---|
| **M1** | Walking skeleton. Build, package, render, input, exit. Done |
| **M2** | Internet Archive provider: metadata API, derivative scoring, skip logic |
| **M3** | Stream resolution and handoff |
| **M4** | ffplay handoff, process lifecycle, return to app |
| **M5** | Bundled curated collections, CI check that identifiers still resolve |

## Notes for contributors

Two things about this device cost me real time, so they're worth knowing before you start.

Go's TLS needs a CA bundle shipped inside the app folder. OnionOS has no usable system CA store on some Miyoo images, and `SSL_CERT_FILE` has to point at your own copy.

Post-quantum TLS breaks the Miyoo kernel. Set `GODEBUG=tlsmlkem=0,tlssecpmlkem=0`. Certificate and hostname verification stay on.

Both are noted in `launch.sh` and land with M2.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Two rules matter more than the rest: no third-party Go modules, and shipped shell scripts use LF line endings.

## Content and licensing

Antenna ships no video and hosts nothing. The built-in provider browses what the Internet Archive already holds. Public domain status is per title, and an item sitting on archive.org doesn't prove it. Restored or colorized versions can carry fresh rights, and musical cues are often licensed separately from the animation itself.

Antenna is MIT licensed. See [LICENSE](LICENSE).
