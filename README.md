# ship-it

`ship-it` is the deliberately boring path from a local working tree to the remote-default branch: fetch, stage everything, commit, merge, calendar-tag, and push. It does not run project gates or create pull requests.

Run `ship-it start` before editing and `ship-it` afterward. An optional positional argument supplies the commit message; otherwise one is generated from the changed paths. Run `ship-it install` to place the binary in `~/.local/bin` and install its embedded skill for Codex and Claude.
