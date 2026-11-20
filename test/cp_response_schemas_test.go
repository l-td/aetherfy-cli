package test

// Holds every struct internal/api decodes a control-plane answer into to the
// schema that answer is declared with, both ways (test/cpschemas).
//
// WHAT GOES WRONG WITHOUT IT. The CLI decodes by json tag, and encoding/json
// says nothing when a tag names a field the server does not send: the Go field
// keeps its zero value. A renamed field reads as empty, a renamed state as no
// state, a dropped warning as no warning. The CLI's own tests serve
// hand-written bodies and agree with themselves about every spelling; this
// test asks the control plane's app instead.
//
// THE PAIRS ARE PER ROUTE. One struct decodes several answers (Agent is
// AgentResponse on four routes and WorkspaceAgentResponse on a fifth), so each
// pair names the call, the operation and status it reads, and, where one status
// answers with a union of models, the member the call asked for. Every exported
// method of api.Client is either a pair or named in notDecoded with the reason,
// so a new call cannot skip the check.
//
// WAIVERS. A struct may decode a subset of an answer (KindNotDecoded), or one
// struct may decode two answers that differ (KindNotSent on the narrower one).
// Each waiver names its fields and says why; a waiver that excuses nothing
// fails, so it leaves with the difference it excused.

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/test/cperrors"
	"github.com/l-td/aetherfy-cli/test/cpschemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const v1 = "/api/v1"

type answerPair struct {
	Call   string       // the api.Client method
	Op     string       // "<METHOD> <path> <status>", as the app's document keys it
	Member string       // the model the call decodes, when the status answers with a union
	Type   reflect.Type // what the call decodes into
}

func typeOf(v interface{}) reflect.Type { return reflect.TypeOf(v) }

var answerPairs = []answerPair{
	{"GetAgentAccess", "GET " + v1 + "/agents/{agent_id_or_name}/access 200", "", typeOf(api.AgentAccess{})},
	{"GrantWorkspace", "POST " + v1 + "/agents/{agent_id_or_name}/access/workspaces 200", "", typeOf(api.AgentAccess{})},
	{"RevokeWorkspace", "DELETE " + v1 + "/agents/{agent_id_or_name}/access/workspaces/{workspace_name} 200", "", typeOf(api.AgentAccess{})},
	{"SetOwnWorkspaceLevel", "PUT " + v1 + "/agents/{agent_id_or_name}/access/own-workspace 200", "", typeOf(api.AgentAccess{})},
	{"SetSecretsAccess", "PUT " + v1 + "/agents/{agent_id_or_name}/access/secrets 200", "", typeOf(api.AgentAccess{})},

	{"ListAgents", "GET " + v1 + "/agents 200", "", typeOf([]api.Agent{})},
	{"GetAgent", "GET " + v1 + "/agents/{agent_id_or_name} 200", "", typeOf(api.Agent{})},
	{"CreateAgent", "POST " + v1 + "/agents 201", "", typeOf(api.Agent{})},
	{"UpdateAgent", "PATCH " + v1 + "/agents/{agent_id_or_name} 200", "", typeOf(api.Agent{})},
	{"MoveAgent", "POST " + v1 + "/agents/{agent_id_or_name}/move 200", "", typeOf(api.AgentMove{})},
	{"StartAgent", "POST " + v1 + "/agents/{agent_id_or_name}/start 202", "", typeOf(api.StartAgentResult{})},
	{"SpawnAgent", "POST " + v1 + "/agents/{agent_id_or_name}/spawn 202", "", typeOf(api.SpawnResponse{})},
	{"RunAgent", "POST " + v1 + "/agents/{agent_id_or_name}/run 202", "", typeOf(api.RunAgentResponse{})},
	{"ListAgentRuns", "GET " + v1 + "/agents/{agent_id_or_name}/runs 200", "", typeOf([]api.AgentRun{})},
	{"PauseSchedule", "POST " + v1 + "/agents/{agent_id_or_name}/schedule/pause 200", "", typeOf(api.ScheduleStateResponse{})},
	{"ResumeSchedule", "POST " + v1 + "/agents/{agent_id_or_name}/schedule/resume 200", "", typeOf(api.ScheduleStateResponse{})},
	{"ValidateAPIKey", "GET " + v1 + "/auth/me 200", "", typeOf(api.UserInfo{})},

	{"SetCollectionProtected", "PATCH " + v1 + "/collections/{collection_id} 200", "CollectionProtected", typeOf(api.CollectionProtection{})},
	{"GetCollectionRecentAgents", "GET " + v1 + "/collections/{collection_id}/recent-agents 200", "", typeOf(api.RecentAgents{})},
	{"PreviewCollectionRegions", "POST " + v1 + "/collections/{collection_id}/regions/preview 200", "", typeOf(api.RegionsChangePreview{})},
	{"ChangeCollectionRegions", "PATCH " + v1 + "/collections/{collection_id} 200", "CollectionRegionsChange", typeOf(api.CollectionChange{})},
	{"ChangeCollectionRegions", "PATCH " + v1 + "/collections/{collection_id} 202", "CollectionRegionsChange", typeOf(api.CollectionChange{})},
	{"MoveCollection", "PATCH " + v1 + "/collections/{collection_id} 200", "CollectionMove", typeOf(api.CollectionChange{})},
	{"MoveCollection", "PATCH " + v1 + "/collections/{collection_id} 202", "CollectionMove", typeOf(api.CollectionChange{})},
	{"UpdateWorkspaceRegions", "PATCH " + v1 + "/workspaces/{workspace_name}/regions 200", "", typeOf(api.WorkspaceRegionsChange{})},
	{"UpdateWorkspaceRegions", "PATCH " + v1 + "/workspaces/{workspace_name}/regions 202", "", typeOf(api.WorkspaceRegionsChange{})},
	{"GetCollectionOperation", "GET " + v1 + "/collections/{collection_id}/operation 200", "", typeOf(api.CollectionOperationAnswer{})},
	{"GetRegionChangeUsage", "GET " + v1 + "/users/me/region-changes 200", "", typeOf(api.RegionChangeUsage{})},
	{"GetOperation", "GET " + v1 + "/operations/{operation_id} 200", "", typeOf(api.Operation{})},
	{"GetDeployOptions", "GET " + v1 + "/deploy-options 200", "", typeOf(api.DeployOptions{})},

	{"Deploy", "POST " + v1 + "/agents/{agent_id_or_name}/deploy 202", "", typeOf(api.DeployResponse{})},
	{"GetDeployment", "GET " + v1 + "/deployments/{deployment_id} 200", "", typeOf(api.Deployment{})},
	{"ListDeploymentsPage", "GET " + v1 + "/agents/{agent_id_or_name}/deployments 200", "", typeOf(api.DeploymentsPage{})},
	{"CancelDeployment", "POST " + v1 + "/agents/{agent_id_or_name}/deployments/{version}/cancel 202", "", typeOf(api.Deployment{})},
	{"CancelRun", "POST " + v1 + "/deployments/{deployment_id}/cancel 200", "", typeOf(api.RunCancelResponse{})},
	{"Redeploy", "POST " + v1 + "/agents/{agent_id_or_name}/deployments/{version}/redeploy 202", "", typeOf(api.RollbackResponse{})},
	{"Rollback", "POST " + v1 + "/agents/{agent_id_or_name}/deployments/{version}/rollback 202", "", typeOf(api.RollbackResponse{})},
	{"GetAgentLogs", "GET " + v1 + "/agents/{agent_id_or_name}/logs 200", "", typeOf([]api.LogEntry{})},

	{"StartDevice", "POST " + v1 + "/auth/device 201", "", typeOf(api.DeviceAuthorization{})},
	{"PollDevice", "POST " + v1 + "/auth/device/token 200", "DeviceApproved", typeOf(api.DeviceToken{})},
	{"PollDevice", "POST " + v1 + "/auth/device/token 202", "", typeOf(api.DeviceToken{})},

	{"GitHubConnectURL", "GET " + v1 + "/auth/github 200", "", typeOf(api.GitHubConnectURL{})},
	{"GitHubStatus", "GET " + v1 + "/auth/github/status 200", "", typeOf(api.GitHubStatus{})},
	{"GitHubRepositories", "GET " + v1 + "/auth/github/repositories 200", "", typeOf(api.GitHubRepoList{})},
	{"GitHubLinkAgent", "POST " + v1 + "/agents/{agent_id_or_name}/github 201", "", typeOf(api.GitHubLinkResponse{})},
	{"GitHubPauseAutoDeploy", "POST " + v1 + "/agents/{agent_id_or_name}/github/pause 200", "", typeOf(api.GitHubAutoDeployResponse{})},
	{"GitHubResumeAutoDeploy", "POST " + v1 + "/agents/{agent_id_or_name}/github/resume 200", "", typeOf(api.GitHubAutoDeployResponse{})},

	{"ListKeys", "GET " + v1 + "/keys 200", "", typeOf(api.KeyList{})},
	{"ListSecrets", "GET " + v1 + "/agents/{agent_id_or_name}/secrets 200", "", typeOf([]api.Secret{})},
	{"ListWorkspaceSecrets", "GET " + v1 + "/workspaces/{workspace_name}/secrets 200", "", typeOf([]api.Secret{})},
	{"GetUsage", "GET " + v1 + "/usage 200", "", typeOf(api.Usage{})},

	{"ListWorkspaceAgents", "GET " + v1 + "/workspaces/{workspace_name}/agents 200", "", typeOf([]api.WorkspaceAgent{})},
	{"ListWorkspaceGrantedAgents", "GET " + v1 + "/workspaces/{workspace_name}/granted-agents 200", "", typeOf([]api.GrantedAgent{})},
	{"CreateWorkspace", "POST " + v1 + "/workspaces 201", "", typeOf(api.Workspace{})},
	{"ListWorkspaces", "GET " + v1 + "/workspaces 200", "", typeOf([]api.Workspace{})},
	{"GetWorkspace", "GET " + v1 + "/workspaces/{workspace_name} 200", "", typeOf(api.Workspace{})},
	{"UpdateWorkspace", "PATCH " + v1 + "/workspaces/{workspace_name} 200", "", typeOf(api.Workspace{})},
	{"DeleteWorkspace", "DELETE " + v1 + "/workspaces/{workspace_name} 200", "", typeOf(api.WorkspaceDeleteResponse{})},
}

// notDecoded names every exported api.Client method that is not a pair, and why.
var notDecoded = map[string]string{
	// The transport: each typed call above goes through one of these.
	"Get": "transport", "GetRaw": "transport", "Post": "transport", "PostFile": "transport",
	"Patch": "transport", "Put": "transport", "Delete": "transport", "DeleteWithResult": "transport",
	"SetVerbose": "configures the client, reads no answer",

	"GetAgentYAML":                 "the agent's aetherfy.yaml as bytes, not a JSON answer",
	"DeleteAgent":                  "reads the status only",
	"StopAgent":                    "reads the status only",
	"ArchiveAgent":                 "reads the status only",
	"RestoreAgent":                 "reads the status only",
	"GetAgentStatus":               "is GetAgent",
	"ListDeployments":              "follows ListDeploymentsPage",
	"GetDeploymentLogs":            "is GetAgentLogs",
	"GitHubDisconnect":             "a 204, no body",
	"GitHubDisconnectInstallation": "a 204, no body",
	"GitHubUnlinkAgent":            "a 204, no body",
	"DeleteSecret":                 "a 204, no body",
	"DeleteWorkspaceSecret":        "a 204, no body",
	"RevokeKey":                    "decodes into a map it never reads",
	"SetSecret":                    "decodes into a map it never reads",
	"SetWorkspaceSecret":           "decodes into a map it never reads",
}

// answerWaivers: every difference the CLI keeps on purpose, with its reason.
var answerWaivers = []cpschemas.Waiver{
	// Subsets: the CLI reads part of an answer.
	{Kind: cpschemas.KindNotDecoded, Type: "Agent", Schema: "AgentResponse",
		Fields: []string{"awake", "entrypoint", "idle_timeout_minutes", "include", "keep_alive", "memory_mb", "serves_websocket"},
		Reason: "how an agent runs is its aetherfy.yaml's to say (one owner per property), and afy reads the manifest " +
			"itself (GetAgentYAML) rather than these echoes of it; afy shows an agent's status, not whether its machine is awake"},
	{Kind: cpschemas.KindNotDecoded, Type: "CollectionChange", Schema: "CollectionRegionsChange",
		Fields: []string{"plan", "regions_to_add", "regions_to_remove"},
		Reason: "afy collections regions shows what a change adds and removes from the preview it read first"},
	{Kind: cpschemas.KindNotDecoded, Type: "CollectionChange", Schema: "CollectionMove",
		Fields: []string{"source_workspace_id", "workspace_id"},
		Reason: "afy collections move names the destination from its own argument"},
	{Kind: cpschemas.KindNotDecoded, Type: "CollectionOperationAnswer", Schema: "CollectionOperationResponse",
		Fields: []string{"collection_id"},
		Reason: "the CLI asked by that id"},
	{Kind: cpschemas.KindNotDecoded, Type: "DeployOptions", Schema: "DeployOptionsResponse",
		Fields: []string{"max_memory_mb", "runtime_labels", "workspace_id"},
		Reason: "the CLI checks a memory size by its own `allowed` flag in memory_mb, names a runtime by its id, and asked for the workspace by id"},
	{Kind: cpschemas.KindNotDecoded, Type: "DeployResponse", Schema: "DeploymentResponse",
		Fields: []string{"admitted_at", "build_failure_detail", "can_redeploy", "can_rollback", "cancellation_reason",
			"cancellation_requested", "created_at", "deployed_at", "error_message", "failure_code", "has_result",
			"image_size_mb", "is_degraded", "is_ephemeral", "is_serving", "pending_region_alert_stage", "pending_regions",
			"position", "regions", "regions_ready", "regions_total", "release_version", "result", "result_error", "retry",
			"rollback_notice", "spawned_count", "started_at"},
		Reason: "afy deploy reads the new release's id, version, state, queue place and warnings, then follows the release with GetDeployment, which decodes the rest"},
	{Kind: cpschemas.KindNotDecoded, Type: "Deployment", Schema: "DeploymentResponse",
		Fields: []string{"has_result", "image_size_mb", "is_ephemeral", "is_serving", "pending_regions", "queue_position",
			"result", "result_error", "rollback_notice", "spawned_count", "started_at"},
		Reason: "the CLI's release and run views show state, failure, retry, regions and queue place (`position`); a run's " +
			"result and the image size are the dashboard's, and the rollback notice is read by afy rollback (RollbackResponse)"},
	{Kind: cpschemas.KindNotDecoded, Type: "RollbackResponse", Schema: "DeploymentResponse",
		Fields: []string{"admitted_at", "build_failure_detail", "can_redeploy", "can_rollback", "cancellation_reason",
			"cancellation_requested", "deployed_at", "failure_code", "has_result", "image_size_mb", "is_degraded",
			"is_ephemeral", "is_serving", "pending_region_alert_stage", "pending_regions", "position", "queue_position",
			"regions", "regions_ready", "regions_total", "release_version", "result", "result_error", "retry",
			"spawned_count", "started_at"},
		Reason: "afy rollback and afy redeploy print the new release's version, state, notice and warnings; afy deployments shows the rest"},
	{Kind: cpschemas.KindNotDecoded, Type: "Operation", Schema: "OperationResponse",
		Fields: []string{"target_id"},
		Reason: "the CLI follows an operation it started on a collection or workspace it already names"},
	{Kind: cpschemas.KindNotDecoded, Type: "Operation.failures[]", Schema: "OperationFailure",
		Fields: []string{"attempts", "collection_id", "region", "sub_step", "type"},
		Reason: "a failed step is shown by its message, which names the region and the collection"},
	{Kind: cpschemas.KindNotDecoded, Type: "Secret", Schema: "SecretKeyResponse",
		Fields: []string{"description"},
		Reason: "afy secrets list shows each key and its times; the CLI sets no description"},
	{Kind: cpschemas.KindNotDecoded, Type: "UserInfo", Schema: "UserInfoResponse",
		Fields: []string{"is_test", "limits", "subscription_status"},
		Reason: "afy whoami shows the account and its plan"},
	{Kind: cpschemas.KindNotDecoded, Type: "WorkspaceRegionsChange", Schema: "WorkspaceRegionsChange",
		Fields: []string{"scope", "workspace_name"},
		Reason: "the CLI names the workspace from its own argument"},

	// One struct, two answers: the device poll's pending and approved bodies,
	// told apart by `status`; the other answer's fields stay zero.
	{Kind: cpschemas.KindNotSent, Type: "DeviceToken", Schema: "DeviceApproved",
		Fields: []string{"interval"},
		Reason: "interval is the pending answer's; an approved poll ends the polling"},
	{Kind: cpschemas.KindNotSent, Type: "DeviceToken", Schema: "DevicePending",
		Fields: []string{"api_key", "key"},
		Reason: "the key is the approved answer's; a pending poll carries none"},
	{Kind: cpschemas.KindPointer, Type: "DeviceToken", Schema: "DeviceApproved",
		Fields: []string{"key"},
		Reason: "nil on the pending answer, which the same struct decodes"},

	// A body the control plane passes through.
	{Kind: cpschemas.KindNotSent, Type: "Usage", Schema: "UsageMeter",
		Fields: []string{"agents", "awakeBudgetNote", "currentTotalLabel", "forecast", "gaugeLabel", "gaugePercent",
			"inOverage", "overageDollars", "overageLabel", "periodEnd", "periodStart", "reason", "tier", "today",
			"totalCompositionLabel"},
		Reason: "the control plane passes the dashboard's usage meter through unchanged (UsageMeter allows extra fields " +
			"and names only hasActivePeriod); these are the dashboard's fields, which no control-plane schema can hold"},
}

// answerAwaiting names pairs whose route declares no shape in the committed
// snapshot yet, with what it waits for. Once the snapshot carries the shape the
// entry fails as outdated, so it leaves when its reason does.
var answerAwaiting = map[string]string{
	"GET " + v1 + "/agents/{agent_id_or_name}/runs 200": "the control plane models GET /runs after agent-messaging " +
		"merges (AWAITING_MERGE in its tests/unit/test_every_route_declares_its_shape.py)",
}

func pairKey(p answerPair) string {
	if p.Member != "" {
		return p.Op + " " + p.Member
	}
	return p.Op
}

// holdPairs runs every pair against an extraction and returns the problems,
// the outdated waivers and the outdated awaiting entries.
func holdPairs(ext *cpschemas.Extraction, pairs []answerPair, waivers []cpschemas.Waiver,
	awaiting map[string]string) (problems []string, unused []string) {
	c := &cpschemas.Checker{Schemas: ext.Schemas, Waivers: waivers}
	awaitUsed := map[string]bool{}
	for _, p := range pairs {
		key := pairKey(p)
		raw, present := ext.Operations[p.Op]
		if !present {
			problems = append(problems, fmt.Sprintf("%s: the control plane has no operation %q", p.Call, p.Op))
			continue
		}
		schema, _ := raw.(map[string]interface{})
		if raw == nil || len(schema) == 0 {
			if _, ok := awaiting[key]; ok {
				awaitUsed[key] = true
				continue
			}
			problems = append(problems, fmt.Sprintf("%s: %s declares no shape, so nothing holds %s to it",
				p.Call, p.Op, p.Type))
			continue
		}
		var found []cpschemas.Problem
		if p.Member != "" {
			found = c.CheckMember(p.Type, schema, p.Member, p.Call)
		} else {
			found = c.Check(p.Type, schema, "", p.Call)
		}
		problems = append(problems, grouped(p, found)...)
	}
	unused = c.Unused()
	for key, why := range awaiting {
		if !awaitUsed[key] {
			unused = append(unused, fmt.Sprintf("awaiting %s (%s): the snapshot now declares its shape, or no pair reads it", key, why))
		}
	}
	sort.Strings(problems)
	sort.Strings(unused)
	return problems, unused
}

// grouped renders one pair's problems a line per (kind, type, schema), naming
// the fields, so a reader sees each difference once with every field it covers.
func grouped(p answerPair, found []cpschemas.Problem) []string {
	type group struct {
		fields  []string
		details map[string]bool
	}
	groups := map[string]*group{}
	var order []string
	for _, f := range found {
		key := fmt.Sprintf("%s %s against %s", f.Kind, f.Type, f.Schema)
		g, ok := groups[key]
		if !ok {
			g = &group{details: map[string]bool{}}
			groups[key] = g
			order = append(order, key)
		}
		g.fields = append(g.fields, f.Field)
		g.details[f.Detail] = true
	}
	var out []string
	for _, key := range order {
		g := groups[key]
		var details []string
		for d := range g.details {
			details = append(details, d)
		}
		sort.Strings(details)
		out = append(out, fmt.Sprintf("%s via %s (%s): fields %v: %s",
			key, p.Call, p.Op, g.fields, strings.Join(details, "; ")))
	}
	return out
}

func loadSchemaSnapshot(t *testing.T) *cpschemas.Snapshot {
	t.Helper()
	snap, err := cpschemas.Load(repoRoot + "/" + cpschemas.SnapshotPath)
	require.NoError(t, err, "cannot read %s: regenerate it with go run ./%s",
		cpschemas.SnapshotPath, cpschemas.GeneratorPath)
	return snap
}

// THE PIN ITSELF.
func TestEveryDecodedAnswerMatchesTheControlPlanesSchema(t *testing.T) {
	snap := loadSchemaSnapshot(t)
	problems, unused := holdPairs(snap.Extraction(), answerPairs, answerWaivers, answerAwaiting)
	if len(problems) > 0 {
		t.Errorf("every struct must decode exactly what its answer declares (%d difference(s)):\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
	if len(unused) > 0 {
		t.Errorf("waivers that excuse nothing must go:\n  %s", strings.Join(unused, "\n  "))
	}
}

// Every call is a pair or says why it is not, so a new call cannot skip the check.
func TestEveryClientCallIsPairedOrExplained(t *testing.T) {
	paired := map[string]bool{}
	for _, p := range answerPairs {
		paired[p.Call] = true
	}
	client := reflect.TypeOf(&api.Client{})
	var missing, both []string
	seen := map[string]bool{}
	for i := 0; i < client.NumMethod(); i++ {
		name := client.Method(i).Name
		seen[name] = true
		_, explained := notDecoded[name]
		switch {
		case !paired[name] && !explained:
			missing = append(missing, name)
		case paired[name] && explained:
			both = append(both, name)
		}
	}
	var stale []string
	for name := range paired {
		if !seen[name] {
			stale = append(stale, name)
		}
	}
	for name := range notDecoded {
		if !seen[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(both)
	sort.Strings(stale)
	assert.Empty(t, missing, "api.Client methods that are neither a pair nor in notDecoded: %v", missing)
	assert.Empty(t, both, "methods both paired and explained away: %v", both)
	assert.Empty(t, stale, "pairs or notDecoded entries naming no api.Client method: %v", stale)
}

// The committed snapshot must be believable on its own, since CI has no
// control plane to compare it against.
func TestCommittedResponseSchemaSnapshotIsTrustworthy(t *testing.T) {
	snap := loadSchemaSnapshot(t)
	assert.Equal(t, cpschemas.SchemaVersion, snap.SchemaVersion,
		"snapshot schemaVersion %d, this guard reads %d: regenerate with go run ./%s",
		snap.SchemaVersion, cpschemas.SchemaVersion, cpschemas.GeneratorPath)
	assert.Equal(t, cpschemas.GeneratorPath, snap.Generator)
	require.NoError(t, cpschemas.Validate(snap.Extraction()))
	require.NotNil(t, snap.Source, "the committed snapshot records no source")
	assert.Regexp(t, `^[0-9a-f]{40}$`, snap.Source.Commit, "the committed snapshot names no control-plane commit")
}

// Where the control plane IS checked out, re-extract and red on any difference,
// so a stale snapshot cannot survive a dev build. CI checks out no siblings and
// skips; the e2e nightly sets AETHERFY_REQUIRE_CP and CONTROL_PLANE_PYTHON, so
// there a skip is a failure.
func TestResponseSchemaSnapshotMatchesTheLiveControlPlane(t *testing.T) {
	cpRoot := cperrors.Root(repoRoot)
	if !cperrors.RootExists(cpRoot) {
		cperrors.SkipUnlessRequired(t, "SKIPPED the live-drift check: no control-plane checkout at %s "+
			"(set %s to point elsewhere). The committed snapshot was checked instead.", cpRoot, cperrors.RootEnv)
	}
	if cpschemas.SourceMissing(cpRoot) {
		t.Fatalf("the control plane IS checked out at %s, but it has no main.py: a move, not a missing "+
			"checkout. Teach test/cpschemas/dump.py where the app lives now.", cpRoot)
	}
	// The push gate first, the generator's own rule: a checkout carrying
	// unpushed Python describes answers nobody else gets, so a diff would report
	// the developer's own work as staleness.
	if why := cpschemas.Unshareable(cpRoot); why != "" {
		cperrors.SkipUnlessRequired(t, "NOT COMPARED: %s. The committed snapshot is unchanged and still "+
			"governs; push the control-plane change, then regenerate: go run ./%s", why, cpschemas.GeneratorPath)
	}
	live, err := cpschemas.Extract(cpRoot)
	if err != nil {
		cperrors.SkipUnlessRequired(t, "NOT COMPARED: %v", err)
	}
	require.NoError(t, cpschemas.Validate(live),
		"the extraction from %s is not trustworthy, refusing to compare against it", cpRoot)

	snap := loadSchemaSnapshot(t)
	diff := cpschemas.Diff(snap.Extraction(), live)
	assert.Empty(t, diff, "the committed snapshot no longer matches the control plane at %s.\n"+
		"Regenerate: go run ./%s, then read what TestEveryDecodedAnswerMatchesTheControlPlanesSchema says.\n  %s",
		cpRoot, cpschemas.GeneratorPath, strings.Join(diff, "\n  "))
	src := cpschemas.DescribeSource(cpRoot)
	t.Logf("[cp-response-schemas] COMPARED %d operation(s), %d schema(s) against aetherfy-control-plane@%s",
		len(live.Operations), len(live.Schemas), src.Commit)
}
