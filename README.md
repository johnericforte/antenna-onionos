# Antenna

[![ci](https://github.com/johnericforte/antenna-onionos/actions/workflows/ci.yml/badge.svg)](https://github.com/johnericforte/antenna-onionos/actions/workflows/ci.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Antenna is a streaming video app for the Miyoo Mini Plus running OnionOS. It pulls video over WiFi from the Internet Archive, so nothing gets stored on your SD card except the app.

## Install on OnionOS

There's no release up yet, so there's nothing to download from that page today. These are the steps for when there is.

Get WiFi working on the handheld first. Antenna pulls everything over the network.

1. On the handheld, open **Apps**, then **Package Manager**, and install **Video Player (FFplay)**. Antenna hands playback over to it rather than shipping its own decoder.
2. Power off and pull the SD card. Put it in your computer.
3. Download `Antenna.zip` from [Releases](https://github.com/johnericforte/antenna-onionos/releases).
4. Unzip it into the `App` folder on the card. You want `App/Antenna/` holding `antenna`, `launch.sh`, `config.json` and `icon.png`.
5. Eject the card, put it back in the handheld, and reboot.
6. Antenna is under **Apps**.

If it doesn't show up, check whether the folder ended up nested a level too deep at `App/Antenna/Antenna/`. That's usually what happened.

## Controls

| Button | Action |
|---|---|
| D-pad up / down | Move selection |
| L1 / R1 | Page up / down |
| B or MENU | Exit to the Onion menu |

## What it plays

Antenna hosts nothing. You wire in a provider and it plays whatever that provider hands back. The Internet Archive is the one that ships built in.

The Miyoo Mini Plus decodes H.264 in software on two Cortex-A7 cores, which works out to around 480p at 1 Mbps. Titles heavier than that are left out of the list rather than shown and then stuttered through. On the three archive.org items that ship as defaults, that leaves 12 of 12 titles playable on one and 6 of 20 on another, measured on 2026-09-06.

## Status

Browsing works. Playback doesn't, that's the next piece.

None of this has run on the actual handheld yet. It was built and tested on a desktop, and the Miyoo hasn't seen it.

## Content and licensing

Antenna ships no video and hosts nothing. The built-in provider browses what the Internet Archive already holds. Public domain status is per title, and an item sitting on archive.org doesn't prove it. Restored or colorized versions can carry fresh rights, and musical cues are often licensed separately from the animation.

Antenna is MIT licensed. See [LICENSE](LICENSE).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).
