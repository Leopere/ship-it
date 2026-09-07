# ship-it

`ship-it` is one no-argument command for routine Git delivery.

Before the first delivery each local day, it runs `git pull --autostash`. Every bare invocation then runs `git add .`, creates an automatic commit when changes are staged, and runs `git push` in the same call. Scripts and agents do not select a lifecycle phase.

Shipping stages all changes without content selection or approval prompts. Neither shipping nor deployment requires a clean worktree. Deployment runs from a private snapshot of the exact pushed commit, so local worktree and index changes do not block it or enter the snapshot.

Native Codex and Cursor lifecycle hooks invoke the same binary. Session start ensures the daily pull has happened. Stop ships the completed coding cycle. After a successful push, a tracked `.deploy-it.json` triggers `deploy-it` for the repository’s declared destination. Repositories without a deployment contract still ship normally. Git delivery requires no verification contract, tags, branch switches, wrapper scripts, or GitHub Actions policy.

Run `ship-it install` once to install the binary, skill, and lifecycle hooks. Routine use requires no arguments or agent involvement.

The hooks use the repository paths in their event payloads. Hook stdout stays valid JSON. Each run saves command output as it arrives in a private log under `~/.local/share/ship-it/hooks/`, with an explicit completion or failure record. Failed synchronous hooks also report their diagnostics on stderr. The Codex Stop timeout allows ten minutes for Git delivery and the bounded deployment command.

When one-shot-tally observes an explicit native edit in another repository, Stop also includes that repository. Read-only access does not select a repository. Pending delivery survives a failed deployment even when Git is clean. One continuation retry is allowed for an unchanged failed edit; a new turn or edit can retry again. A successful delivery clears only its captured edit generation and exact clean revision. SessionStart keeps its original repository scope.

The registry uses the native session ID. Historical edits made before installation, opaque shell edits outside the known roots, and child sessions with a different ID are not inferred. A kernel file lock protects registry updates; it does not claim to deduplicate concurrent initial Stop events.

Existing tabs can retain the old `ship-it hook` or `ship-it cursor-hook` Stop handler with a five-second timeout. Those legacy handlers start the same no-argument binary in a separate process session and return the result-log path immediately. The log records the eventual outcome; the queued message does not claim that delivery succeeded. New sessions continue to use the installed no-argument hooks.
