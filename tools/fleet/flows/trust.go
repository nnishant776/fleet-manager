package flows

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

// TrustSigners keeps the maintainer's own copy of a checkout's
// fleet/allowed_signers, which fleet checks that repository's commits
// against (internal/fleet/trust.go). The first copy is taken as the
// checkout has it, once the keys are shown and approved. After that a change
// is taken only through a commit signed by a key already in the copy, so a
// pushed commit cannot add the key that signs it.
var TrustSigners = &engine.Flow{
	Path:  []string{"trust", "signers"},
	Title: "Trust a repository's signing keys",
	Short: "Keep your own copy of a checkout's fleet/allowed_signers, which its commits are checked against",
	Long: "fleet checks commits against your own copy of each repository's signers, kept in\n" +
		"$XDG_STATE_HOME/fleet (~/.local/state/fleet), not against the checkout's file: a\n" +
		"pushed commit could add the key that signs it. The first time, fleet shows the\n" +
		"checkout's keys and keeps them once you approve. After that it takes a change only\n" +
		"when the commit that made it is signed by a key you already trust, and shows the\n" +
		"keys added and removed first.",
	Example: "  fleet trust signers --flake ~/Projects/inventory\n" +
		"  fleet trust signers --flake /etc/nixos",
	Inputs: []engine.Input{fleet.FlakeInput},
	Needs:  []string{"git"},
	Steps: []engine.Step{
		{
			Title: "Read the keys the checkout lists",
			Why:   "Its fleet/allowed_signers, and which repository it is: the copy is kept per repository, named after its address.",
			Do:    engine.Func(readCheckoutSigners),
		},
		{
			Title: "Keep your copy of them",
			Why:   "The first copy is the one you approve now. A later change is taken only through a commit signed by a key already in your copy.",
			Do:    engine.Func(keepSignersCopy),
		},
	},
}

// checkoutSigners reads a checkout's origin and fleet/allowed_signers.
func checkoutSigners(x *engine.Ctx, dir string) (origin string, data []byte, keys []fleet.SignerKey, err error) {
	data, err = os.ReadFile(filepath.Join(dir, "fleet", "allowed_signers"))
	if err != nil {
		return "", nil, nil, fmt.Errorf("%s has no fleet/allowed_signers: %w", dir, err)
	}
	if keys, err = fleet.ParseSigners(data); err != nil {
		return "", nil, nil, fmt.Errorf("%s/fleet/allowed_signers: %w", dir, err)
	}
	origin, err = engine.Silently(x, nil, "git", "-C", dir, "remote", "get-url", "origin")
	if err != nil || origin == "" {
		return "", nil, nil, fmt.Errorf("%s has no origin: the copy is kept per repository, named after its address", dir)
	}
	return origin, data, keys, nil
}

func readCheckoutSigners(x *engine.Ctx) error {
	origin, data, keys, err := checkoutSigners(x, x.Value("flake"))
	if err != nil {
		return err
	}
	copyPath, err := fleet.SignersCopy(origin)
	if err != nil {
		return err
	}
	id, _ := fleet.RepoID(origin)
	x.Values["signers_repo"], x.Values["signers_copy"], x.Values["signers_data"] = id, copyPath, string(data)
	x.Values["signers_origin"] = origin
	mine := yourSigningKey(x, x.Value("flake"))
	var lines []string
	for _, k := range keys {
		line := "  " + k.String()
		if k.Blob == mine {
			line += "  (yours)"
		}
		lines = append(lines, line)
	}
	noun := "keys"
	if len(keys) == 1 {
		noun = "key"
	}
	x.UI.OK(fmt.Sprintf("%s lists %d %s for %s:", filepath.Join(x.Value("flake"), "fleet", "allowed_signers"), len(keys), noun, id))
	x.UI.Text(strings.Join(lines, "\n"))
	return nil
}

// yourSigningKey is the key git signs with in the checkout, base64, or "".
func yourSigningKey(x *engine.Ctx, dir string) string {
	v, err := engine.Silently(x, nil, "git", "-C", dir, "config", "user.signingKey")
	if err != nil || v == "" {
		return ""
	}
	line := strings.TrimPrefix(v, "key::")
	if line == v {
		if strings.HasPrefix(v, "~/") {
			home, _ := os.UserHomeDir()
			v = filepath.Join(home, v[2:])
		}
		if !strings.HasSuffix(v, ".pub") {
			v += ".pub"
		}
		data, err := os.ReadFile(v)
		if err != nil {
			return ""
		}
		line = string(data)
	}
	if f := strings.Fields(line); len(f) >= 2 {
		return f[1]
	}
	return ""
}

func keepSignersCopy(x *engine.Ctx) error {
	_, trusted, err := fleet.TrustedSigners(x.Value("signers_origin"))
	now, _ := fleet.ParseSigners([]byte(x.Value("signers_data")))
	switch {
	case errors.Is(err, fleet.ErrNoSignersCopy):
		x.UI.Note("You have no copy of the keys for " + x.Value("signers_repo") + " yet. Check them above: the one marked as yours should be your own key.")
		return writeSignersCopy(x, "Trust these keys for "+x.Value("signers_repo")+"?")
	case err != nil:
		return err
	}
	added, removed := fleet.DiffSigners(trusted, now)
	if len(added) == 0 && len(removed) == 0 {
		x.UI.OK("your copy already has exactly these keys")
		return nil
	}
	x.UI.Note("The checkout's keys differ from the ones you trust:")
	x.UI.Text(signersChange(added, removed))
	// The newest commit that changed the file must be signed by a key the
	// copy already has.
	res, err := engine.Cmd("git", "-C", "{{.flake}}", "log", "-1", "--format=%H", "--", "fleet/allowed_signers").
		ReadOnly().Quiet().Capture("signers_change").OK("changed in {{.signers_change}}").Run(x)
	if err != nil {
		return err
	}
	if strings.TrimSpace(res.Stdout) == "" && !x.Options.DryRun {
		return errors.New("no commit changed fleet/allowed_signers: the file differs from HEAD's; commit or discard it")
	}
	if _, err := engine.Cmd("git", "-C", "{{.flake}}", "-c", "gpg.ssh.allowedSignersFile={{.signers_copy}}", "verify-commit", "{{.signers_change}}").
		ReadOnly().Quiet().
		Explain(func(engine.Result) string {
			return "the change, in {{.signers_change}}, is not signed by a key you trust, so your copy stays as it is. " +
				"If you expected it, check it with git log --show-signature -1 {{.signers_change}}; " +
				"to start over from what the checkout has, remove {{.signers_copy}} and run this again"
		}).OK("{{.signers_change}} is signed by a key you trust").Run(x); err != nil {
		return err
	}
	return writeSignersCopy(x, "Update your copy to these keys?")
}

func writeSignersCopy(x *engine.Ctx, question string) error {
	ok, err := x.Approve(question)
	if !ok || err != nil {
		return err
	}
	if err := fleet.WriteSignersCopy(x.Value("signers_copy"), []byte(x.Value("signers_data"))); err != nil {
		return err
	}
	x.UI.OK("kept in " + x.Value("signers_copy"))
	return nil
}

// signersChange lists keys added and removed, one per line.
func signersChange(added, removed []fleet.SignerKey) string {
	var lines []string
	for _, k := range added {
		lines = append(lines, "  + "+k.String())
	}
	for _, k := range removed {
		lines = append(lines, "  - "+k.String())
	}
	return strings.Join(lines, "\n")
}
