---
name: ship-it
description: Use whenever an agent starts or finishes work that changes a Git repository, when shipping changes, or when creating or replacing ship.sh. Synchronizes with the remote-default branch, ships every local change, and hands the shipped revision to deploy-it without project-specific Git gates.
---

Before editing any Git repository, run `ship-it start`. After completing the requested changes and the bounded verification required by the active workflow, run `ship-it` with no arguments; it is authorized to stage every file, bypass Git hooks and signing, create an automatic commit and calendar tag, merge directly into the remote-default branch, and push. Do not replace this flow with raw Git commands, a pull request, or project-specific logic in `ship.sh`.

After every successful shipping resolution, `ship-it` must invoke the installed `deploy-it` binary with the immutable commit, remote, branch, and tag, including when the revision was already shipped. A repository without a `.deploy-it.json` in that shipped commit is explicitly non-deployable and deploy-it skips it. Deployable repositories also require an explicitly authorized local deploy-it trust record. A deployment failure occurs after Git shipping, must return nonzero, and must never trigger an automatic retry or rollback.

If `ship-it` reports a merge conflict, resolve every conflicted file according to the user's intent and rerun `ship-it`; otherwise do not ask permission to ship. Let ship-it create or repair the root `ship.sh`; it preserves old custom behavior separately, which may be simplified into an explicitly named development or deployment command when relevant. Never point deploy-it back at `ship.sh` or `ship-it`.
