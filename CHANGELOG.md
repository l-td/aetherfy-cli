# Changelog

## Unreleased

### Breaking

- `afy logs --json` prints one object, `{"logs": [...], "retention_days": N, "since_clamped": false}`,
  where it used to print the bare array of lines. `retention_days` is the plan's log retention in days
  (`null` where it is negotiated per contract) and `since_clamped` is `true` when `--since` reached past
  it and was cut to it. `afy logs --follow --json` is unchanged: one compact object per line. The
  product is pre-launch and keeps no compatibility form.
- `afy logs` refuses an answer from the control plane that does not state the plan's log retention
  (`X-Log-Retention-Days`), with the code `CLI_RESPONSE_INVALID` under `--json`. Release this CLI after
  the control plane that sends the header.

### Changed

- `afy logs --since` past the plan's retention shows what is kept and puts a note on stderr naming the
  retention. It is not an error.
- `afy logs` notes on stderr when the plan's size cap removed older lines.
- `afy logs --follow` ends with the control plane's refusal when a read is refused, the first one
  included, instead of retrying every two seconds; a transient failure (a 5xx, a network error) is said
  once per outage and tried again, on the first read as on the polls after it.
