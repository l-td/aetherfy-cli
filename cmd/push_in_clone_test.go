package cmd

import (
	"strings"
	"testing"
)

// A PUSH IN ITS CLONE HAS NO DEPLOYMENT ROW EITHER: it is admitted to deploy and
// still being fetched and checked, and until its release is written the agent's
// link (`github.push_in_clone`) is the only thing that says it exists. These pin
// that `afy status` and `afy deployments` print it as `Push preparing`, in front
// of the push waiting behind it, and print nothing when no push is in its clone.

const cloneBody = `"push_in_clone":{"id":"3f1c2d4e-0000-4000-8000-000000000001",` +
	`"sha":"1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d","since":"2026-11-14T10:15:30.123456Z"}`

const linkHead = `{"linked":true,"repo":"l-td/templates","branch":"main","root_dir":null,` +
	`"webhook_id":"1","account_connected":true,"branch_deleted_at":null,`

const waitingBody = `"waiting_push":{"sha":"9f8e7d6c5b4a39281706f5e4d3c2b1a098765432","received_at":"2026-09-26T12:00:00Z"}`

// A push in its clone alone, and both at once (the newer push waits behind it).
const cloneLink = linkHead + `"waiting_push":null,` + cloneBody + `}`
const cloneAndWaitingLink = linkHead + waitingBody + `,` + cloneBody + `}`

// An agent whose link says neither, as the control plane sends it when no push is in flight.
const noPushLink = linkHead + `"waiting_push":null,"push_in_clone":null}`

const cloneLine = "1a2b3c4, since 2026-11-14 10:15 UTC — becomes the next deployment when it is ready"

func TestStatusShowsThePushInItsClone(t *testing.T) {
	out, _ := runStatus(t, "text", cloneLink)
	if !strings.Contains(out, "Push preparing") || !strings.Contains(out, cloneLine) {
		t.Fatalf("afy status did not show the push in its clone:\n%s", out)
	}
	if strings.Contains(out, "Push waiting") {
		t.Fatalf("afy status invented a waiting push:\n%s", out)
	}
}

func TestStatusSaysNothingWhenNoPushIsInItsClone(t *testing.T) {
	// The three shapes that carry no such push: null, absent (an older control
	// plane) and a waiting push alone.
	for name, link := range map[string]string{"null": noPushLink, "absent": idleLink, "waiting alone": waitingLink} {
		out, _ := runStatus(t, "text", link)
		if strings.Contains(out, "Push preparing") {
			t.Fatalf("afy status invented a push in its clone (%s):\n%s", name, out)
		}
	}
}

func TestStatusShowsBothPushesWithTheCloneFirst(t *testing.T) {
	out, _ := runStatus(t, "text", cloneAndWaitingLink)
	i, j := strings.Index(out, "Push preparing"), strings.Index(out, "Push waiting")
	if i < 0 || j < 0 {
		t.Fatalf("afy status must show both pushes:\n%s", out)
	}
	if i > j {
		t.Fatalf("the push in its clone is ahead of the one waiting behind it:\n%s", out)
	}
	if !strings.Contains(out, cloneLine) || !strings.Contains(out, waitingLine) {
		t.Fatalf("afy status words one of the pushes differently:\n%s", out)
	}
}

func TestStatusJSONCarriesThePushInItsClone(t *testing.T) {
	out, _ := runStatus(t, "json", cloneLink)
	for _, want := range []string{
		`"push_in_clone"`, "3f1c2d4e-0000-4000-8000-000000000001",
		"1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d", "2026-11-14T10:15:30.123456Z",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("afy status --json dropped %q from push_in_clone:\n%s", want, out)
		}
	}
}

func TestStatusJSONKeepsANullPushInItsCloneNull(t *testing.T) {
	out, _ := runStatus(t, "json", noPushLink)
	if !strings.Contains(out, `"push_in_clone": null`) {
		t.Fatalf("afy status --json must say push_in_clone is null, not drop it:\n%s", out)
	}
}

func TestDeploymentsNameThePushInItsCloneAfterTheHistoryAndBeforeTheWaitingOne(t *testing.T) {
	out := deploymentsWithAgent(t, cloneAndWaitingLink)
	i, j, h := strings.Index(out, "Push preparing"), strings.Index(out, "Push waiting"), strings.Index(out, "Total:")
	if i < 0 || j < 0 || h < 0 {
		t.Fatalf("afy deployments must name both pushes after the history:\n%s", out)
	}
	if !(h < i && i < j) {
		t.Fatalf("the order is history, the push in its clone, then the push waiting:\n%s", out)
	}
	if !strings.Contains(out, cloneLine) {
		t.Fatalf("afy deployments words the push in its clone differently from afy status:\n%s", out)
	}
}

func TestDeploymentsSayNothingWhenNoPushIsInItsClone(t *testing.T) {
	out := deploymentsWithAgent(t, waitingLink)
	if strings.Contains(out, "Push preparing") {
		t.Fatalf("afy deployments invented a push in its clone:\n%s", out)
	}
}

func TestOnlyTheUnfilteredFirstPageNamesThePushInItsClone(t *testing.T) {
	run := func(args ...string) string {
		cp := newFakeCP(t)
		cp.on("GET", "/agents/bot", 200, statusAgentJSON(cloneLink))
		cp.on("GET", "/agents/bot/deployments", 200, deploymentsPage(failedLatestRows))
		stdout, _, err := runAfy(t, cp, append([]string{"deployments", "bot"}, args...)...)
		if err != nil {
			t.Fatalf("deployments %v: %v", args, err)
		}
		return stdout
	}
	if out := run(); !strings.Contains(out, "Push preparing") {
		t.Errorf("the first page must name the push in its clone:\n%s", out)
	}
	for _, args := range [][]string{{"--status", "failed"}, {"--since", "7d"}, {"--before", "dep-9"}} {
		if out := run(args...); strings.Contains(out, "Push preparing") {
			t.Errorf("afy deployments %v is not the latest page, yet names the push in its clone:\n%s", args, out)
		}
	}
}
