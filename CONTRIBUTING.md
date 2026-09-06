# Contributing

Thanks for taking a look. This is a small project with a few deliberate constraints, and the constraints are the interesting part, so it's worth reading these two rules before you open a PR.

## Two rules that matter

### No third-party Go modules

`go.mod` stays empty of dependencies. The whole app is one static ARMv7 binary with nothing beside it, which is what makes installing it a folder copy instead of a support thread. The standard library covers TLS, HTTP and JSON. Anything past that, I'd rather write.

Dev tools like golangci-lint are installed as binaries or run as CI actions. They're never imports, never `go.mod` entries. CI fails the build if `go list -m all` reports anything but the main module.

### Shipped shell scripts use LF line endings

A `launch.sh` with CRLF endings won't execute on the Miyoo's shell, and it fails quietly. The app just never starts. `.gitattributes` handles this and CI checks it, so you shouldn't have to think about it. Please don't override it.

## Getting set up

Go 1.22 is the floor. CI runs on 1.27.

```sh
make hooks   # installs the pre-commit hook: gofmt and vet
make ci      # everything CI runs
```

Run `make` on its own to see the targets. If `go` isn't on your PATH, the Makefile falls back to `~/.local/go/bin/go`.

## Before you open a PR

```sh
make ci
```

That's fmt-check, vet, lint, tests and the ARM cross-compile, same order as CI. Green here means green there.

## Testing on device

Most of this can't be tested off-device in any useful way. `internal/fb` writes to `/dev/fb0` and `internal/input` reads `/dev/input/event0`, and neither is mocked. The pure logic (the playability gate, glyph packing, text layout) is unit tested and should stay that way.

If you touch rendering or input, say in the PR whether you tested on hardware. "Not tested on device" is a fine answer. Leaving it unsaid isn't.

To install a build:

```sh
make package   # produces Antenna.zip
```

Unzip into `/mnt/SDCARD/App/` and refresh the Apps list. Onion's built-in HTTP file server (Tweaks, Network, HTTP) saves a lot of card-ejecting if you're iterating.

## Style

gofmt settles formatting, so there's nothing to argue about there. golangci-lint covers the rest. See `.golangci.yml`, which is tuned to stay quiet about style opinions and loud about actual defects.

Exported identifiers need doc comments. This repo is public and someone will read it to figure out how OnionOS apps get built.

## Scope

Bug reports and device compatibility fixes are always welcome.

For anything large, open an issue first. There's a roadmap in the README and a couple of deliberate non-goals, mainly no transcoding and no dependency on a server I run.

Provider contributions are welcome when the source can hand back a small progressive stream. Sources that need scraped HTML or a rotating token tend to break within weeks and turn into maintenance I can't promise to keep up with, so please open an issue before building one.
