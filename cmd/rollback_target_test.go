package cmd

import (
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// `afy deployments` suggests a rollback after a failed deploy. The suggestion
// used to be the newest active or superseded version, which named versions the
// server would refuse (image and archive both gone) and skipped ones it would
// accept (a rolled_back or failed release that still has its image or archive).
// The server's can_rollback is the rule.
func TestTheRollbackSuggestionFollowsTheServer(t *testing.T) {
	history := []api.Deployment{
		{Version: 5, Status: "superseded", CanRollback: false},
		{Version: 4, Status: "rolled_back", CanRollback: true},
		{Version: 3, Status: "superseded", CanRollback: true},
	}

	got := newestRollbackTarget(history)
	if got == nil || got.Version != 4 {
		t.Fatalf("suggested %+v, want v4: the newest version the server can roll back to", got)
	}
}

// THE CONTROL: nothing the server can roll back to, no suggestion.
func TestNoRollbackSuggestionWhenTheServerAcceptsNone(t *testing.T) {
	history := []api.Deployment{
		{Version: 2, Status: "superseded", CanRollback: false},
		{Version: 1, Status: "active", CanRollback: false},
	}

	if got := newestRollbackTarget(history); got != nil {
		t.Fatalf("suggested v%d, which the server would refuse", got.Version)
	}
}
