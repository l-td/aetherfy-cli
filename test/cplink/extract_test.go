package cplink

import (
	"strings"
	"testing"
)

// The parser reads Python by shape, so its rules are worth running against
// source it cannot have been tuned to: a model with the decorations the real one
// has (a docstring, comments, defaults, a nested annotation) plus the shapes it
// must NOT take for fields.
const sample = `
from pydantic import BaseModel


class Unrelated(BaseModel):
    not_mine: str


class GitHubLinkStatusResponse(BaseModel):
    """Current link state for an agent.

    webhook_secret is intentionally NOT exposed here.
    """
    linked: bool
    repo: str | None = None
    # A comment between fields.
    root_dir: str | None = None
    account_connected: bool = True
    branch_deleted_at: datetime | None = None

    @classmethod
    def _thing(cls, v: str) -> str:
        local_var: int = 1
        return v


class Later(BaseModel):
    also_not_mine: str
`

func TestParseModelReadsOneClassBody(t *testing.T) {
	got, err := ParseModel(sample, "GitHubLinkStatusResponse")
	if err != nil {
		t.Fatalf("ParseModel: %v", err)
	}
	want := "account_connected,branch_deleted_at,linked,repo,root_dir"
	if strings.Join(got, ",") != want {
		t.Errorf("fields = %v, want %s", got, want)
	}
}

func TestParseModelStopsAtTheNextClass(t *testing.T) {
	// The negative control that matters most: a walk that never stops collects
	// every field of every model below it, and the snapshot then "matches"
	// whatever the CLI decodes because it contains nearly everything.
	got, _ := ParseModel(sample, "GitHubLinkStatusResponse")
	for _, leaked := range []string{"not_mine", "also_not_mine", "local_var"} {
		for _, g := range got {
			if g == leaked {
				t.Errorf("the parser leaked %q out of another scope: %v", leaked, got)
			}
		}
	}
}

func TestParseModelRefusesAModelThatIsNotThere(t *testing.T) {
	// A rename must be an error, never an empty list. An empty list would pass
	// every comparison by having nothing to disagree with.
	if _, err := ParseModel(sample, "GitHubLinkStatusRenamed"); err == nil {
		t.Fatal("ParseModel accepted a model that is not in the source")
	}
}

func TestValidateRefusesAnExtractionItCannotVouchFor(t *testing.T) {
	cases := []struct {
		name   string
		fields *Fields
		want   string
	}{
		{"nothing at all", nil, "no field list"},
		{"empty", &Fields{Names: nil}, "EMPTY"},
		{"under the floor", &Fields{Names: []string{"a_b", "c_d"}}, "floor"},
		{
			"duplicated",
			&Fields{Names: []string{"a_b", "a_b", "c_d", "e_f", "g_h"}},
			"twice",
		},
		{
			"unsorted",
			&Fields{Names: []string{"z_z", "a_b", "c_d", "e_f", "g_h"}},
			"not sorted",
		},
		{
			// Sorted, as it happens: uppercase sorts before lowercase in byte
			// order, so this reaches the shape rule rather than the order rule.
			"not a json-tag shape",
			&Fields{Names: []string{"BadField", "a_b", "c_d", "e_f", "g_h"}},
			"shape",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.fields)
			if err == nil {
				t.Fatalf("Validate accepted %v", c.fields)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}

	// The positive control: a real-shaped list passes, so the cases above fail
	// for their own reasons and not because Validate refuses everything.
	if err := Validate(&Fields{Names: []string{"a_b", "c_d", "e_f", "g_h", "i_j"}}); err != nil {
		t.Errorf("Validate rejected a well-formed field list: %v", err)
	}
}

func TestUnshareableBlocksEveryStateThatIsNotPushed(t *testing.T) {
	no, yes := false, true
	cases := []struct {
		name string
		src  *Source
		want string
	}{
		{"no provenance", nil, "no provenance"},
		{"dirty", &Source{Path: "p", Dirty: true}, "uncommitted"},
		{"no upstream", &Source{Path: "p", Branch: "wip"}, "no upstream"},
		{"unpushed", &Source{Path: "p", Branch: "main", Unpushed: &yes}, "not pushed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			why := Unshareable(c.src)
			if why == "" {
				t.Fatalf("Unshareable allowed %+v", c.src)
			}
			if !strings.Contains(why, c.want) {
				t.Errorf("reason %q does not mention %q", why, c.want)
			}
		})
	}

	// The positive control. Without it, an Unshareable that refuses everything
	// would pass every case above and silently disable the drift check forever.
	if why := Unshareable(&Source{Path: "p", Branch: "main", Unpushed: &no}); why != "" {
		t.Errorf("a clean, pushed source was called unshareable: %s", why)
	}
}
