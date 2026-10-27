package engine

import (
	"reflect"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/ui"
)

func TestGuideOf(t *testing.T) {
	f := &Flow{
		Path: []string{"repo", "add"}, Title: "Add a repository",
		Short: "Adds repositories", Long: "First paragraph\nstill the first.\n\nSecond.",
		Example: "  fleet repo add a\n  fleet repo add b",
		Args:    []string{"name"}, Rest: true,
		Needs: []string{"git", "sops"},
		Inputs: []Input{
			{Name: "name", Prompt: "Which repository?", Help: "the name", Many: true, Short: "n"},
			{Name: "token", Help: "the token", Secret: true},
			{Name: "force", Bool: true, Prompt: "Force it?"},
		},
		Steps: []Step{
			{Title: "Clone {{.url}}", Why: "So that {{index . \"url\"}} is here."},
			{Title: "Check"},
		},
	}
	got := guideOf(f)
	want := ui.Guide{
		Usage:    "fleet repo add [NAME]...",
		Examples: []string{"  fleet repo add a", "  fleet repo add b"},
		About:    []string{"Adds repositories.", "First paragraph\nstill the first.", "Second."},
		Steps:    []ui.GuideItem{{Name: "Clone URL", Text: "So that URL is here."}, {Name: "Check"}},
		Flags: []ui.GuideItem{
			{Name: "-n, --name", Text: "the name; may be given more than once"},
			{Name: "--token-stdin", Text: "read the token from standard input, instead of being asked. the token"},
			{Name: "--force", Text: "Force it?"},
		},
		Needs: []string{"git", "sops"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}
