# Changelog

The version is stamped at release time (goreleaser ldflags), not kept in the code,
so an entry here is written for the version the owner tags next.

## v0.4.2 (not tagged yet)

### Breaking changes, and how to update a script

**`--force` is now `--yes` on the deletes.** `afy delete` and `afy workspaces delete`
took `--force` to skip the question; they take `--yes` (`-y`), like `afy collections
delete`. `afy secrets delete` had no way to skip its question and now takes `--yes`.
There is no alias: a script that passes `--force` gets `unknown flag: --force` and
exit 2.

```bash
# before
afy delete scraper --force
afy workspaces delete research --force
# after
afy delete scraper --yes
afy workspaces delete research --yes
afy secrets delete scraper API_KEY --yes      # new: it could not be scripted before
```

`--force` keeps its other meanings, which are not a confirmation: `afy init --force`
(overwrite an existing `aetherfy.yaml`), `afy collections schema get --save ... --force`
(overwrite the file) and `afy upgrade --force`.

**Every question a command asks follows one exit rule.** This covers the six deletes
(`afy delete`, `afy workspaces delete`, `afy secrets delete`, `afy collections delete`,
`afy collections schema delete`, `afy points delete`), `afy access`, `afy collections
regions`, the overage question of `afy deploy` and the overwrite question of `afy init`:

| What happens | Before | Now |
|---|---|---|
| The person answers no | `secrets delete`, `workspaces delete`, `delete`, the deploy overage question and `init`'s overwrite: **exit 0**, "Cancelled" / "Aborted." printed, and with `--json` a success-shaped result (`"deleted": false`, `"cancelled": true`) on stdout. `access`, `collections regions` and the three vector deletes: exit 2 | **exit 1**, `Cancelled: ...`, nothing changed. With `--json`: an error document on stderr with the code `CLI_CANCELLED`, and nothing on stdout |
| No terminal and no `--yes` (a script, a pipe) | the prompt was printed and read nothing (the answer was "no", so exit 0), or exit 1, or a differently worded exit 2 | **exit 2, before asking**: `This <what it does>; pass --yes to confirm without a prompt.` With `--json`: the code `CLI_USAGE_ERROR`. `afy init`'s overwrite is skipped by `--force` and says so |
| Input ends before an answer (Ctrl-D, a closed pipe) | varied | exit 2, nothing changed |

A script that relied on a declined delete exiting 0 now sees exit 1: pass `--yes` to
delete, or handle the exit code. A script that fed `y` to `afy secrets delete` on
stdin without a terminal now exits 2: pass `--yes`.

`afy access` and `afy collections regions` used to refuse `--json` outright without
`--yes`. On a terminal they now ask (the question goes to stderr, stdout stays one
JSON object); without a terminal they are refused like the others.

**`afy whoami` reads the plan from the API on every run.** It used to print the plan
saved by `afy login`, which was empty when the key came from `AETHERFY_API_KEY`. It
now calls `GET /auth/me`, so the email and plan are right whichever way the key was
supplied, and right after a plan change. Two consequences: it needs the network and
fails like any other command when the API cannot be reached or refuses the key (exit
1; exit 3 when not logged in, as before), and the plan is no longer written to the
credentials file (a `tier:` line in an old file is ignored). A script that parsed
`whoami --json` gets the same fields.

### Other changes

- A yaml declaring `name:`, `description:` or `workspace:` is refused by `afy deploy`
  and `afy diff` before anything is uploaded, with the control plane's sentence, where
  it used to be refused by the server after the upload (HTTP 422
  `DEPLOYMENT_CONFIG_PARSE_ERROR`).
- `afy workspaces regions` checks `--regions` locally, as `workspaces create` does:
  an unknown region, one the plan does not open, too many, or an empty value is exit 2
  with the allowed regions listed, instead of the server's "Request validation
  failed." with exit 1. When the server itself refuses, each rule it names is printed
  under the summary.
- `afy workspaces create` prints the regions it created the workspace with (and
  `regions` is in its `--json`, as before).
- New error codes in the `--json` error document, both the CLI's own with no `status`:
  `CLI_USAGE_ERROR` (input refused before anything was sent; already used for exit 2)
  and `CLI_CANCELLED` (a question answered no, exit 1).
