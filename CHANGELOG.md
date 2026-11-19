# Changelog

The version is stamped at release time (goreleaser ldflags), not kept in the code,
so an entry here is written for the version the owner tags next.

## Unreleased

### Added

- `afy messages list <run-id>` and `afy messages send <run-id> <body>`: a run's mailbox. `list` prints every
  message the run was sent, in order, read or not, with who sent it (a person, or another run of its tree) and
  whether the run has read it. `send` sends one: the body is JSON when it reads as JSON (`'{"approved": true}'`,
  `42`) and the text as given otherwise, with an id of its own, so a send repeated after a lost answer is stored
  once. A run waiting for a message is shown as `awaiting_message` in `afy runs`, and `afy run --wait` keeps
  following it. Release this CLI after the control plane that serves the mailbox routes.

- `include:` in aetherfy.yaml: `afy deploy` of an agent whose yaml lists repository paths to deploy with
  it (`include: [../../shared]`, relative to the agent's folder) uploads an archive rooted at the
  repository (the nearest folder holding `.git`) holding the folder and each path at its place, and sends
  the folder's repository path as `code_root`. Before the upload it refuses, with the control plane's
  sentence, an include that is malformed, names something a deploy always leaves out (`.git`, `.env`,
  `node_modules` and the other built-in exclusions), leaves the repository (`DEPLOYMENT_INCLUDE_OUTSIDE_REPOSITORY`),
  is not there (`DEPLOYMENT_INCLUDE_NOT_FOUND`) or overlaps the agent's folder
  (`DEPLOYMENT_INCLUDE_OVERLAPS_AGENT_FOLDER`), and any include from a folder outside a git repository.
  An agent without `include:` uploads exactly what it did. Release this CLI after the control plane that
  accepts `code_root`.

- `afy keys create [--name <n>]`, `afy keys list` and `afy keys revoke <id>`. A key is created by
  approving a code in the dashboard, not by a command that holds a key: `keys create` prints a code and
  a page, asks the control plane every few seconds (the interval it states) until a person signed in to
  the dashboard approves the code, then prints the new key once, with "This is the only time the full
  key is shown.", and stores it nowhere. Its two calls carry no credentials. `keys revoke` asks `[y/N]`
  first (`--yes` from a script, declining exits 1) and says so when the key is the one this CLI is using.
  `keys list --json` prints the control plane's document unchanged. Release this CLI after the control
  plane that serves `/auth/device`, `/auth/device/token`, `/keys` and `/usage`.
- `afy usage`: this billing period's usage as the dashboard's Usage page words it (period, plan,
  percent of the plan used, overage dollars, each agent's awake time). `--json` prints the control
  plane's document unchanged.
- `afy login --no-browser`.
- Every command whose answer carries `warnings` prints them, as the control plane words them, wherever they
  sit in the answer: `afy deploy`, `afy redeploy`, `afy rollback` and `afy cancel` (on the release),
  `afy status`, `afy create`, `afy update` and `afy init --agent` (on the agent), a move by `afy update
  --workspace` (inside the redeploy it starts), and `afy list` and `afy deployments`, which say each sentence
  once with the agents or versions it covers. Today that is the warning that the agent's runtime is past its
  end of life: the sentence names the date deploys on it stop and the runtime to create a new agent on. It is
  a warning, not an error, and the exit code does not change. `--json` carries them as `warnings` on the
  deployment and on the agent, and nested in a move. Release this CLI after the control plane that sends
  `warnings`.
- A run over the account's limit waits in a line instead of being refused, and a run stopped from outside ends
  `cancelled`. `afy run` says `Run accepted; waiting for a slot (#3 in line).` when the run waits, and
  `afy spawn` says it too. `afy runs`, `afy list`, `afy status` and `afy run --wait` show the state `waiting`
  with its place (`waiting (#3 in line)`, yellow) and `cancelled` (dim); where a failed run's reason is shown
  (`afy status`, `afy run --wait`) a cancelled run's is, as the control plane worded it. `--json` carries
  `position` and `cancellation_reason` on a run and `status`, `position` and a `job_id` that is `null` while
  the run waits on `afy run`. Release this CLI after the control plane that sends them.
- `afy cancel <name> --run <run-id>` cancels a run: one waiting in the line or not started is cancelled at
  once (`Run <id> cancelled.`, and how many runs below it were cancelled with it), one running is stopped
  (`Run <id> is being stopped; it ends cancelled.`), and one that had already ended prints the control plane's
  message and exits 0. The run must be the named agent's: another agent's run is refused as input (exit 2)
  before anything is cancelled. `afy cancel <name>` without `--run` cancels the agent's pending deployment, as
  before; its help no longer says that only a queued deployment can be cancelled. Release this CLI after the
  control plane that serves `POST /deployments/{run}/cancel`.
- `afy status` prints `Deadline: N min` for a schedule that has a deadline, and `--json` carries
  `cron_deadline_minutes`.
- A failed deployment or run shows its failure code beside its reason. `afy deploy`, `afy redeploy`,
  `afy rollback` and `afy run --wait` print `Failure code: <CODE>` under `Reason:`, and with `--json` the error
  object carries it as its `code`. `afy status` shows it under `Deploy error` and on the last run's line,
  `afy deployments` under the version's error, and `afy runs --json` carries `failure_code` on each run. A
  cancelled run (its reason is `cancellation_reason`) and a deployment that failed before codes were recorded
  show the reason alone. Release this CLI after the control plane that sends `failure_code`.
- While the platform retries an attempt that met a temporary unavailability of its own, `afy deploy`,
  `afy redeploy`, `afy rollback` and `afy run --wait` say so, once each time the retry changes, not on every
  poll: `The platform was temporarily unavailable: retrying in N s (attempt K of M).`, or `retrying now` once
  the time has come. With `--json` the line goes to stderr and stdout stays the one document. `--json` carries
  `retry` as the control plane sends it (`null` when nothing is being retried) on a deployment, on an agent's
  last run and on each run of `afy runs`. If the retries are used up, the deployment or run fails with the code
  `PLATFORM_TEMPORARILY_UNAVAILABLE`. Release this CLI after the control plane that sends `retry`.

### Fixed

- `afy deploy` of a folder holding a symlink failed with `archive/tar: write too long`. A link written as
  a relative path that lands inside the agent's folder (or a path it includes) is now packed as a link;
  any other link is refused before the upload, naming `include:` as the way to deploy shared code, as a
  push refuses it.

### Breaking

- `schedule:` in `aetherfy.yaml` is a block with `cron:` and an optional `deadline_minutes:`. The string form
  (`schedule: "0 9 * * 1-5"`) is refused by `afy deploy` and `afy diff` before anything is uploaded, with the
  block to write using the expression in the file. `afy init --schedule` and the prompt write the block, with
  a commented `deadline_minutes` line beside it. The product is pre-launch and keeps no compatibility form of
  the string. Release this CLI after the control plane that accepts the block.

- `afy login` with no key to hand no longer asks you to paste one. It runs the device sign-in: it prints a
  code and a page, you approve the code in the dashboard, and afy stores the key that approval created.
  `--api-key`, a key piped on stdin (`echo "$KEY" | afy login`) and `AETHERFY_API_KEY` work as before. The
  product is pre-launch and keeps no compatibility form of the prompt.

- `afy deployments --json` prints one object, `{"deployments": [...], "next_before_id": null}`, where it
  used to print the bare array of deployments. `next_before_id` is the id to pass as `--before` for the
  next, older page, or `null` when there are no older deployments. The product is pre-launch and keeps
  no compatibility form. Release this CLI after the control plane that answers the list as a page.
- `afy logs --json` prints one object, `{"logs": [...], "retention_days": N, "since_clamped": false}`,
  where it used to print the bare array of lines. `retention_days` is the plan's log retention in days
  (`null` where it is negotiated per contract) and `since_clamped` is `true` when `--since` reached past
  it and was cut to it. `afy logs --follow --json` is unchanged: one compact object per line. The
  product is pre-launch and keeps no compatibility form.
- `afy logs` refuses an answer from the control plane that does not state the plan's log retention
  (`X-Log-Retention-Days`), with the code `CLI_RESPONSE_INVALID` under `--json`. Release this CLI after
  the control plane that sends the header.

### Changed

- `afy run --wait` follows the run to the state it ends in, a waiting run included, and says each change of its
  place in the line. It exits `0` when the run completed and `1` when it failed or was cancelled, printing the
  reason. Its 30-minute bound is a failure: when the run is still going then, it exits `4` (as `afy start
  --wait` does at its bound) with a message that giving up the wait does not stop the run and how to follow it;
  it used to exit `0`. A state this CLI does not know is followed like any other and printed once. The first
  read is made at once, not after five seconds, and `--json` gets its one error object with the reason in its
  message.
- `afy spawn` printed an empty run id: it read fields the control plane does not send. It prints the run's id,
  its state and its region from what the control plane sends, and `--json` carries the answer as it came.
- `afy init` names no runtime of its own. It reads the language and the version a project asks for and
  resolves them against what the control plane offers (`GET /deploy-options`): a version that is offered
  is that runtime, an older one gets the lowest offered version above it and `afy init` says so, and a
  project that names no version gets the control plane's default for its language. Node 20 is no longer
  offered. `afy init` picks a runtime by what the control plane says each one is (its language, its version,
  whether it is TypeScript), never by reading its name, and the commented `# entrypoint:` suggestion in the
  file it writes is the file that runtime runs by default. A runtime the control plane adds needs no CLI
  release, whatever it is called.
- `afy github pause <agent>` and `afy github resume <agent>` turn auto-deploy off and on for an agent's
  GitHub link without unlinking it. While auto-deploy is paused a push builds nothing and says so on its
  commit, and it is not deployed later, not even after a resume; a push already being built finishes, and
  `afy deploy` still deploys. Resuming deploys nothing by itself: the next push deploys, or run
  `afy deploy`. Both are idempotent, like `afy schedule pause` and `resume`: a call that
  finds the state already set says `Auto-deploy for <agent> was already paused.` or `was already on.`, and
  `--json` prints the whole answer, as the schedule commands do: the link (with `auto_deploy_paused_at`) and
  `changed`, false for that no-op. An agent with no link is refused with
  `GITHUB_NOT_LINKED`. `afy status` shows `Auto-deploy paused since <time>` for a paused link, and
  `--json` carries it as `github.auto_deploy_paused_at` (`null` when auto-deploy is on). Unlinking clears
  the pause and re-linking keeps it. Release this CLI after the control plane that sends the field.
- `afy status` and `afy deployments` show a push that is being prepared, fetched from GitHub and checked
  before it becomes the agent's deployment, as a `Push preparing` line ahead of `Push waiting`; both can show
  at once. `afy status --json` carries it under `github.push_in_clone` (`id`, `sha` and `since`, or `null`).
  While it is prepared, a deploy, a rollback, a redeploy and a move are refused with `DEPLOYMENT_IN_PROGRESS`,
  and a delete, pause or archive with `AGENT_HAS_PENDING_DEPLOYMENTS`. Release this CLI after the control
  plane that sends the field.
- The deployment history holds releases only: a run is not a deployment, and the control plane no
  longer lists runs there (an agent's runs are `afy agents runs`). `afy status`, `afy redeploy` and
  `afy cancel` no longer skip run rows, because none arrive.
- `afy deployments` shows the 20 newest deployments and takes `--since` (30m, 24h, 7d), `--status`,
  `--limit` (1 to 100) and `--before`. When older deployments exist its last line gives the `--before`
  to pass for the next page. The "latest deployment" hints and the waiting push appear only on the
  first unfiltered page. Every other command that reads the history (`afy status`, `afy rollback`,
  `afy redeploy`, `afy cancel`) still reads all of it, page after page.
- `afy logs --since` past the plan's retention shows what is kept and puts a note on stderr naming the
  retention. It is not an error.
- `afy logs` notes on stderr when the plan's size cap removed older lines.
- `afy logs --follow` ends with the control plane's refusal when a read is refused, the first one
  included, instead of retrying every two seconds; a transient failure (a 5xx, a network error) is said
  once per outage and tried again, on the first read as on the polls after it.

## v0.4.2 (2026-10-21)

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
