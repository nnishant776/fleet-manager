package flows

import (
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// The runbooks in the repository are the ones the flows write.
func TestRunbooksAreCurrent(t *testing.T) {
	changed, gone := staleRunbooks("../runbooks", engine.Runbooks(All()))
	if len(changed) > 0 || len(gone) > 0 {
		t.Errorf("the runbooks are out of date (to write: %v, to remove: %v): run `go run . docs --yes` in tools/fleet and commit them",
			changed, gone)
	}
}

func TestRunbookShowsFlagsStepsAndCommands(t *testing.T) {
	page := engine.Runbook(SupportKeyRotate)
	for _, want := range []string{
		"# fleet support-key rotate\n",
		"fleet support-key rotate [HOST] [flags]",
		"| `--old-key` | Its private key file |",
		"3. **Sync HOST with the old key.**",
		"   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 -o IdentitiesOnly=yes -o IdentityAgent=none -i OLD-KEY support@ADDRESS 'sudo -n systemctl start config-auto-update'",
		"whoever holds it is still root on HOST.",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q:\n%s", want, page)
		}
	}
	if install := engine.Runbook(Install); !strings.Contains(install, "| `-d`, `--disk` |") ||
		!strings.Contains(install, "may be given more than once") {
		t.Errorf("install's flags:\n%s", install)
	}
}

// Every flow is on the menu once, but docs, which is for changing fleet.
func TestMenuListsEveryFlowOnce(t *testing.T) {
	seen := map[*engine.Flow]int{}
	for _, s := range Menu {
		for _, f := range s.Flows {
			seen[f]++
		}
	}
	for _, f := range All() {
		want := 1
		if f == Docs {
			want = 0
		}
		if seen[f] != want {
			t.Errorf("fleet %s is on the menu %d times, want %d", strings.Join(f.Path, " "), seen[f], want)
		}
	}
}
