# ship-it

`ship-it` is the deliberately boring path from a local working tree to the remote-default branch: fetch, stage everything, commit, merge, calendar-tag, push, and hand the shipped revision to `deploy-it`. It does not invent project gates or create pull requests.

Install `deploy-it` first. Run `ship-it start` before editing and `ship-it` afterward. An optional positional argument supplies the commit message; otherwise one is generated from the changed paths. Every successful shipping resolution invokes the installed `deploy-it` binary with the exact commit, remote, branch, and tag, including when the revision was already shipped. Repositories without `.deploy-it.json` in that commit skip deployment, while deployable repositories require explicit local trust. Run `ship-it install` to place the binary in `~/.local/bin` and install its embedded skill for Codex and Claude.
