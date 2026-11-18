# deskrig

One testing interface for agents: see and drive a desktop (this Mac, a macOS VM, more later) with the same commands

```sh
npm install -g deskrig      # or run it once: npx deskrig ...
deskrig setup
```

The first run downloads the deskrig release this package names from GitHub, checks it against the
sha256 pinned in the package, and installs it to `~/.local/bin/deskrig`, where the
[install script](https://github.com/sudiptadeb/deskrig/blob/main/install.sh) puts it. From then on
the binary there (or in `/usr/local/bin`) is the one that runs, and it keeps itself current.

Everything else: https://github.com/sudiptadeb/deskrig
