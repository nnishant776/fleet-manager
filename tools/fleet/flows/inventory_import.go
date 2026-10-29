package flows

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

// InventoryImport moves a host that fleet-manager defines into a private
// inventory: it copies the host's folder, and the folders of the users it
// declares, adjusted to their new place, checks the host there, and commits,
// pushes and publishes its branch.
var InventoryImport = &engine.Flow{
	Path:  []string{"inventory", "import"},
	Title: "Move a host from fleet-manager into the inventory",
	Short: "Copy a host's definition from fleet-manager into the inventory, adjusted, check it, commit, push and publish its branch",
	Long: "A host that fleet-manager defines can move to a private inventory. This copies what\n" +
		"the host is made of: its folder in hosts/, and the folders in users/ of the accounts it\n" +
		"declares. It reads them from a commit of fleet-manager, never from a working tree: the\n" +
		"tool's own clone of it by default (brought up to date, which only takes commits signed\n" +
		"by a key you trust), or the checkout you give with --from, whose newest commit must be\n" +
		"signed by a key you trust.\n" +
		"\n" +
		"The copy is adjusted in three ways and no other. The path to fleet/allowed_signers gets\n" +
		"one level deeper, since the inventory keeps hosts one folder lower. The address the host\n" +
		"fetches its configuration from, fleet-manager's, becomes the inventory's: --address, else\n" +
		"the one the inventory's other hosts use, else the inventory's origin. And the host must not\n" +
		"set conf.fleet.repo.ref, which the inventory sets to the host's own branch. A file that\n" +
		"reads something outside the host's folder stops the move, naming it, since the inventory\n" +
		"would not have it.\n" +
		"\n" +
		"fleet shows each change, then checks the host in the inventory: it fetches the inventory's\n" +
		"address, has its branch, its signers file is the inventory's, and every host still\n" +
		"evaluates. If any of that fails, the copy is taken out again. Then it commits, signed,\n" +
		"pushes to main once you approve, and publishes the host's branch. The machine itself\n" +
		"still runs from fleet-manager: the last step lists the commands that finish the move.",
	Example: "  fleet inventory import nas1 --flake inventory\n" +
		"  fleet inventory import nas1 --from ~/Projects/fleet-manager --flake inventory --address git@example.com:org/inventory.git",
	Args: []string{"host"},
	Inputs: []engine.Input{
		fleet.FlakeInput,
		{Name: "host", Prompt: "Host to move", Help: "A host of fleet-manager (hosts/<name>)", Pattern: `^[A-Za-z0-9][A-Za-z0-9-]*$`},
		{Name: "from", Prompt: "Fleet-manager to copy from",
			Help:     "A fleet-manager checkout, or a repository name; the tools' own clone of fleet-manager by default",
			Optional: true,
			Resolve:  func(x *engine.Ctx, v string) (string, error) { return fleet.ResolveName(x, "from", v) }},
		{Name: "address", Prompt: "Inventory address",
			Help:     "The address hosts fetch the inventory from; by default the one the inventory's other hosts use",
			Optional: true},
	},
	Needs: []string{"git", "nix", "tar"},
	Cleanup: func(x *engine.Ctx) {
		if d := x.Value("imp_tmp"); d != "" {
			_ = os.RemoveAll(d)
		}
	},
	Steps: []engine.Step{
		{
			Title: "Check the inventory",
			Why: "The host is committed on top of the inventory's main, so the checkout must be clean, level with origin's main, and that commit signed by a key you trust. " +
				"Its layout says where hosts and users go, and the host must not be in it yet.",
			Do: engine.Func(checkImportTarget),
		},
		{
			Title: "Read the host from fleet-manager",
			Why: "The host's files come from a commit of fleet-manager that a key you trust signed, not from a working tree. " +
				"The accounts the host declares are read from that commit too, and the folders of those in users/ come along.",
			Do: engine.Func(readImportSource),
		},
		{
			Title: "See what the host reads outside its folder",
			Why:   "The inventory has only what is copied. A file that reads something outside the host's folder, other than the signers file and the users that come along, would not find it there, so the move stops and names it.",
			Do:    engine.Func(seeImportReads),
		},
		{
			Title: "Copy it, adjusted",
			Why: "Writes the host's folder and the users' folders into the inventory, with the signers path, the address and nothing else changed. " +
				"Each change is shown first. If the run stops before the commit, what was written is taken out again.",
			Do: engine.Func(copyIntoInventory),
		},
		{
			Title: "Check it in the inventory",
			Why: "The host is evaluated there, as the hosts will see it: the address it fetches, its branch, the signers file it uses, and that every host still evaluates. " +
				"A failure takes the copy out again.",
			Do: engine.Func(checkImported),
		},
		{
			Title: "Commit it",
			Why:   "One commit that adds the host's folders. The repository's own git settings sign it.",
			Do:    engine.Func(commitImport),
		},
		{
			Title: "Push it",
			Why:   "Puts the commit on origin's main. A push that is not a fast-forward is refused: then someone else pushed first. What you push is the commit you trust on main from then on.",
			Do:    engine.Func(pushImport),
		},
		{
			Title: "Publish its branch",
			Why:   "The host fetches its own branch, host/{{.host}}, not main. Only that branch is cut, checked to build from its own files alone, signed and pushed.",
			When: func(x *engine.Ctx) (bool, string) {
				if x.Value("imp_host_branches") != "true" {
					return false, "the inventory does not set hostBranches: its hosts fetch main"
				}
				return true, ""
			},
			Do: engine.Func(publishImport),
		},
		{
			Title: "Next",
			Why:   "The inventory has the host now, but the machine still runs from fleet-manager. These finish the move.",
			Do:    engine.Func(showImportNext),
		},
	},
}

var accountName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

// importReloc is where the host's parts go, from the values the steps keep.
func importReloc(x *engine.Ctx) relocation {
	r := relocation{Host: x.Value("host"), HostsDir: x.Value("imp_hosts_dir"), UsersDir: x.Value("imp_users_dir"), Users: map[string]bool{}}
	for _, u := range strings.Fields(x.Value("imp_users")) {
		r.Users[u] = true
	}
	return r
}

func cleanLayoutDir(name, p string) (string, error) {
	c := path.Clean(p)
	if p == "" || c == "." || path.IsAbs(c) || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("the inventory's layout gives %s as %q, which is not a folder inside it", name, p)
	}
	return c, nil
}

func checkImportTarget(x *engine.Ctx) error {
	if err := checkInventoryCheckout(x); err != nil {
		return err
	}
	if _, err := engine.Cmd("nix", "eval", "--json", "{{.flake}}#fleetLayout").ReadOnly().Quiet().
		Capture("imp_layout").OK("read the inventory's layout").Run(x); err != nil {
		return err
	}
	var l layout
	if err := json.Unmarshal([]byte(x.Value("imp_layout")), &l); err != nil {
		return fmt.Errorf("fleetLayout: %w", err)
	}
	hosts, err := cleanLayoutDir("hosts", l.Hosts)
	if err != nil {
		return err
	}
	x.Values["imp_hosts_dir"] = hosts
	x.Values["imp_users_dir"] = ""
	if l.Users != nil {
		users, err := cleanLayoutDir("users", *l.Users)
		if err != nil {
			return err
		}
		x.Values["imp_users_dir"] = users
	}
	x.Values["imp_host_branches"] = fmt.Sprint(l.HostBranches)
	host := x.Value("host")
	if fileExists(filepath.Join(x.Value("flake"), filepath.FromSlash(hosts), host)) {
		return fmt.Errorf("%s is in the inventory already (%s/%s)", host, hosts, host)
	}
	x.UI.OK(host + " is not in the inventory yet; it goes to " + hosts + "/" + host)
	return nil
}

// fleetManagerClone is the name and the folder of the tools' clone of
// fleet-manager.
func fleetManagerClone(x *engine.Ctx) (name, clone string, err error) {
	id, err := fleet.RepoID(fleet.UpstreamURL)
	if err != nil {
		return "", "", err
	}
	if name = fleet.NameOf(id); name == "" {
		return "", "", errors.New("fleet repo add first: run it with no address and it adds fleet-manager")
	}
	if clone, err = fleet.ClonePathOfID(id); err != nil {
		return "", "", err
	}
	return name, clone, nil
}

func readImportSource(x *engine.Ctx) error {
	host := x.Value("host")
	var dir, commit string
	var err error
	switch from := x.Value("from"); {
	case from == "" || x.Value("from_name") != "":
		name := x.Value("from_name")
		if from == "" {
			if name, dir, err = fleetManagerClone(x); err != nil {
				return err
			}
		} else {
			dir = from
		}
		if !fileExists(dir) {
			return fmt.Errorf("the clone of %s is not in %s: run fleet repo add again", name, dir)
		}
		x.UI.OK(name + " is the tool's clone in " + dir)
		if err := fleet.SyncClone(x, dir); err != nil {
			return err
		}
		if commit, err = fleet.CommitOf(x, dir, fmMain); err != nil {
			return err
		}
	default:
		if dir, err = filepath.Abs(from); err != nil {
			return err
		}
		origin, err := fleet.OriginOf(x, dir)
		if err != nil {
			return err
		}
		if commit, err = fleet.CommitOf(x, dir, "HEAD"); err != nil {
			return fmt.Errorf("%s is not a git checkout of fleet-manager", dir)
		}
		signers, err := trustedSignersFor(x, origin, dir)
		if err != nil {
			return err
		}
		x.Values["imp_signers"], x.Values["imp_commit"] = signers, commit
		if _, err := engine.Cmd("git", "-C", dir, "-c", "gpg.ssh.allowedSignersFile={{.imp_signers}}", "verify-commit", "HEAD").ReadOnly().Quiet().
			Explain(func(engine.Result) string {
				return "the checkout's newest commit, {{.imp_commit}}, is not signed by a key you trust ({{.imp_signers}})"
			}).OK("{{.imp_commit}} is signed by a key you trust").Run(x); err != nil {
			return err
		}
		// What hosts have run is on fleet-manager's main.
		if _, err := engine.Cmd("git", "-C", dir, "fetch", "-q", "origin").ReadOnly().Quiet().OK("fetched origin").Run(x); err != nil {
			x.UI.Skip("could not fetch origin, so whether the commit is on main is not known")
		} else if main, err := fleet.CommitOf(x, dir, fmMain); err == nil {
			if on, err := fleet.Follows(x, dir, commit, main); err == nil && !on {
				x.UI.Note(fleet.Short(commit) + " is not on fleet-manager's main: hosts have never run it")
			}
		}
	}
	origin, err := fleet.OriginOf(x, dir)
	if err != nil {
		return err
	}
	id, err := fleet.RepoID(origin)
	if err != nil {
		return err
	}
	x.Values["imp_dir"], x.Values["imp_commit"], x.Values["imp_fm_id"] = dir, commit, id
	x.UI.OK("reading " + fleet.Short(commit) + " of " + origin)

	listed := func(args ...string) (string, error) {
		res, err := engine.Cmd("git", append([]string{"-C", dir, "ls-tree", "-d", "--name-only", commit, "--"}, args...)...).RunQuiet(x)
		return strings.TrimSpace(res.Stdout), err
	}
	if out, err := listed("hosts/" + host); err != nil || out == "" {
		all, _ := listed("hosts/")
		var names []string
		for _, l := range strings.Fields(all) {
			names = append(names, strings.TrimPrefix(l, "hosts/"))
		}
		return fmt.Errorf("fleet-manager has no hosts/%s at %s; hosts/ there holds: %s", host, fleet.Short(commit), strings.Join(names, ", "))
	}

	tmp, err := os.MkdirTemp("", "fleet-import-*")
	if err != nil {
		return err
	}
	x.Values["imp_tmp"] = tmp
	src := filepath.Join(tmp, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		return err
	}
	extract := func(rel string) error {
		_, err := engine.Cmd("sh", "-c", `git -C "$1" archive "$2" -- "$3" | tar -x -C "$4"`, "sh", dir, commit, rel, src).
			ReadOnly().Quiet().OK("read " + rel).Run(x)
		return err
	}
	if err := extract("hosts/" + host); err != nil {
		return err
	}

	accounts, kind, err := sourceAccounts(x, dir, commit, host)
	if err != nil {
		return err
	}
	x.Values["imp_kind"] = kind
	if kind == "nixos" {
		if err := requireSecrets(x, dir, commit, host); err != nil {
			return err
		}
	}
	var users []string
	for _, u := range accounts {
		if !accountName.MatchString(u) {
			return fmt.Errorf("the account name %q is not one to look for a folder of", u)
		}
		out, err := listed("users/" + u)
		if err != nil {
			return err
		}
		if out != "" {
			users = append(users, u)
		}
	}
	if len(users) > 0 && x.Value("imp_users_dir") == "" {
		return fmt.Errorf("%s declares accounts with folders in users/ (%s), and the inventory's layout has no users folder", host, strings.Join(users, ", "))
	}
	for _, u := range users {
		if err := extract("users/" + u); err != nil {
			return err
		}
	}
	x.Values["imp_users"] = strings.Join(users, "\n")
	if len(users) == 0 {
		x.UI.OK(host + " declares " + fmt.Sprint(len(accounts)) + " account(s), none with a folder in users/")
	} else {
		x.UI.OK(host + "'s accounts with a folder in users/, which come along: " + strings.Join(users, ", "))
	}
	return nil
}

// requireSecrets refuses a host without secrets, before anything is copied.
// In the inventory a host fetches its configuration over SSH with its own
// configuration key, which comes with the identity its secrets give it, and
// its login keys come from them: copying its files cannot give it either.
func requireSecrets(x *engine.Ctx, dir, commit, host string) error {
	ref := "git+file://" + dir + "?rev=" + commit
	res, err := engine.Cmd("nix", "eval", "--json", ref+"#nixosConfigurations."+host+".config.conf.secrets.repo.url").
		ReadOnly().Quiet().Run(x)
	if err != nil {
		return fmt.Errorf("reading %s's secrets address at %s: %w: %s", host, fleet.Short(commit), err, strings.TrimSpace(res.Stderr))
	}
	var url *string
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &url); err != nil {
		return fmt.Errorf("%s's secrets address: %w", host, err)
	}
	if url != nil && *url != "" {
		x.UI.OK(host + " has secrets, from " + *url)
		return nil
	}
	// The address the inventory's hosts use, to say what to set.
	set := "set conf.secrets.repo.url to your secrets repository's address"
	if res, err := engine.Cmd("nix", "eval", "--json", x.Value("flake")+"#nixosConfigurations",
		"--apply", "cs: builtins.mapAttrs (_: c: c.config.conf.secrets.repo.url) cs").ReadOnly().Quiet().Run(x); err == nil {
		var urls map[string]*string
		if json.Unmarshal([]byte(res.Stdout), &urls) == nil {
			common := ""
			for _, u := range urls {
				switch {
				case u == nil || *u == "":
				case common == "":
					common = *u
				case common != *u:
					common = "-"
				}
			}
			if common != "" && common != "-" {
				set = fmt.Sprintf("set conf.secrets.repo.url = %q, as the inventory's hosts do", common)
			}
		}
	}
	return fmt.Errorf("%s has no secrets: conf.secrets.repo.url is not set at %s. In the inventory a host fetches its "+
		"configuration over SSH with its own configuration key, which comes with the identity its secrets give it, and takes its "+
		"login keys from them; copying its files cannot give it either. In fleet-manager first: %s, turn on "+
		"conf.fleet.support.keysFromSecrets, move its login keys into the secrets, and enroll it (fleet host enroll %s). Then run this again",
		host, fleet.Short(commit), set, host)
}

// sourceAccounts is the accounts a host declares at a commit of fleet-manager,
// and whether it is a NixOS or a Darwin host.
func sourceAccounts(x *engine.Ctx, dir, commit, host string) ([]string, string, error) {
	ref := "git+file://" + dir + "?rev=" + commit
	for _, kind := range []string{"nixos", "darwin"} {
		res, err := engine.Cmd("nix", "eval", "--json", ref+"#"+kind+"Configurations."+host+".config.conf.users.accounts",
			"--apply", "builtins.attrNames").ReadOnly().Quiet().Run(x)
		if err != nil {
			if strings.Contains(res.Stderr, "does not provide attribute") || strings.Contains(res.Stderr, "attribute '"+host+"' missing") {
				continue
			}
			return nil, "", fmt.Errorf("reading %s's accounts at %s: %w: %s", host, fleet.Short(commit), err, strings.TrimSpace(res.Stderr))
		}
		var accounts []string
		if err := json.Unmarshal([]byte(res.Stdout), &accounts); err != nil {
			return nil, "", fmt.Errorf("%s's accounts: %w", host, err)
		}
		return accounts, kind, nil
	}
	return nil, "", fmt.Errorf("fleet-manager has hosts/%s at %s, but no nixosConfigurations.%s or darwinConfigurations.%s", host, fleet.Short(commit), host, host)
}

// importedFile is one file the host brings, as it is written in the
// inventory.
type importedFile struct {
	Old, New string // paths in fleet-manager's tree and in the inventory
	Data     []byte
	Link     string // the target, for a symbolic link
	Exec     bool
}

// buildImport reads the extracted folders below src and returns the files
// as they go to the inventory, with the path literals and, when address is
// not empty, the addresses adjusted. outside lists, per file, the path
// literals that reach beyond the folders that move.
func buildImport(src string, r relocation, fmID, address string) (files []importedFile, outside map[string][]string, err error) {
	outside = map[string][]string{}
	roots := []string{"hosts/" + r.Host}
	for u := range r.Users {
		roots = append(roots, "users/"+u)
	}
	sort.Strings(roots)
	for _, root := range roots {
		err := filepath.WalkDir(filepath.Join(src, filepath.FromSlash(root)), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(src, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			now, ok := r.newPlace(rel)
			if !ok {
				return fmt.Errorf("%s has nowhere to go in the inventory", rel)
			}
			f := importedFile{Old: rel, New: now}
			if d.Type()&fs.ModeSymlink != 0 {
				if f.Link, err = os.Readlink(p); err != nil {
					return err
				}
				files = append(files, f)
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if info, err := d.Info(); err == nil {
				f.Exec = info.Mode()&0o111 != 0
			}
			if strings.HasSuffix(rel, ".nix") {
				text, out := rewritePathLiterals(string(data), rel, r)
				if len(out) > 0 {
					outside[rel] = out
				}
				if address != "" {
					text, _ = replaceAddress(text, fmID, address)
				}
				data = []byte(text)
			}
			f.Data = data
			files = append(files, f)
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return files, outside, nil
}

func seeImportReads(x *engine.Ctx) error {
	files, outside, err := buildImport(filepath.Join(x.Value("imp_tmp"), "src"), importReloc(x), x.Value("imp_fm_id"), "")
	if err != nil {
		return err
	}
	if len(outside) > 0 {
		var files []string
		for f := range outside {
			files = append(files, f)
		}
		sort.Strings(files)
		var lines []string
		for _, f := range files {
			for _, p := range outside[f] {
				lines = append(lines, fmt.Sprintf("%s reads %s, outside its folder: copy it into the folder, or into the inventory and list it in shared in its flake.nix, then run this again", f, p))
			}
		}
		return errors.New(strings.Join(lines, "\n"))
	}
	x.UI.OK(fmt.Sprintf("%d files, none reading anything outside the host's folder or the users' that come along, other than fleet/allowed_signers", len(files)))
	return nil
}

// inventoryAddress is the address hosts fetch the inventory from.
func inventoryAddress(x *engine.Ctx) (string, error) {
	if a := x.Value("address"); a != "" {
		return a, nil
	}
	res, err := engine.Cmd("nix", "eval", "--json", "{{.flake}}#nixosConfigurations",
		"--apply", "cs: builtins.mapAttrs (_: c: c.config.conf.fleet.repo.url) cs").ReadOnly().Quiet().Run(x)
	switch {
	case err == nil:
		var urls map[string]string
		if err := json.Unmarshal([]byte(res.Stdout), &urls); err != nil {
			return "", fmt.Errorf("the hosts' repo.url: %w", err)
		}
		seen := map[string]bool{}
		var distinct []string
		for _, u := range urls {
			if u != "" && !seen[u] {
				seen[u] = true
				distinct = append(distinct, u)
			}
		}
		if len(distinct) == 1 {
			x.UI.OK("the inventory's other hosts fetch " + distinct[0])
			return distinct[0], nil
		}
	case !strings.Contains(res.Stderr, "does not provide attribute"):
		return "", fmt.Errorf("evaluating the inventory's hosts: %w: %s", err, strings.TrimSpace(res.Stderr))
	}
	a, err := addressFromOrigin(x.Value("inv_origin"))
	if err != nil {
		return "", err
	}
	x.UI.OK("the inventory's other hosts give no one address, so the host fetches " + a + ", made from its origin")
	return a, nil
}

func writeImported(root string, files []importedFile) error {
	for _, f := range files {
		dest := filepath.Join(root, filepath.FromSlash(f.New))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if f.Link != "" {
			if err := os.Symlink(f.Link, dest); err != nil {
				return err
			}
			continue
		}
		mode := os.FileMode(0o644)
		if f.Exec {
			mode = 0o755
		}
		if err := os.WriteFile(dest, f.Data, mode); err != nil {
			return err
		}
	}
	return nil
}

// sameFiles says whether the files already in dest are exactly these.
func sameFiles(dest string, files []importedFile, prefix string) bool {
	want := map[string]importedFile{}
	for _, f := range files {
		want[strings.TrimPrefix(f.New, prefix+"/")] = f
	}
	n := 0
	same := true
	_ = filepath.WalkDir(dest, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dest, p)
		f, ok := want[filepath.ToSlash(rel)]
		if !ok {
			same = false
			return nil
		}
		n++
		if f.Link != "" {
			if t, err := os.Readlink(p); err != nil || t != f.Link {
				same = false
			}
			return nil
		}
		if data, err := os.ReadFile(p); err != nil || string(data) != string(f.Data) {
			same = false
		}
		return nil
	})
	return same && n == len(want)
}

func copyIntoInventory(x *engine.Ctx) error {
	address, err := inventoryAddress(x)
	if err != nil {
		return err
	}
	x.Values["imp_address"] = address
	r := importReloc(x)
	src, flake := filepath.Join(x.Value("imp_tmp"), "src"), x.Value("flake")
	files, _, err := buildImport(src, r, x.Value("imp_fm_id"), address)
	if err != nil {
		return err
	}
	// A user the inventory has already, with the same files, is not copied again.
	var write []importedFile
	paths := []string{r.HostsDir + "/" + r.Host}
	for _, f := range files {
		if underPath(f.New, paths[0]) {
			write = append(write, f)
		}
	}
	users := strings.Fields(x.Value("imp_users"))
	sort.Strings(users)
	for _, u := range users {
		prefix := r.UsersDir + "/" + u
		var mine []importedFile
		for _, f := range files {
			if underPath(f.New, prefix) {
				mine = append(mine, f)
			}
		}
		dest := filepath.Join(flake, filepath.FromSlash(prefix))
		switch {
		case !fileExists(dest):
			write = append(write, mine...)
			paths = append(paths, prefix)
		case sameFiles(dest, mine, prefix):
			x.UI.Skip(prefix + " is in the inventory already, with the same files")
		default:
			return fmt.Errorf("%s is in the inventory already, with other files than fleet-manager's users/%s: make them the same, or take the one in the inventory out, then run this again", prefix, u)
		}
	}
	x.Values["imp_paths"] = strings.Join(paths, "\n")

	// What changes, shown from a copy beside the originals.
	scratch := filepath.Join(x.Value("imp_tmp"), "new")
	if err := writeImported(scratch, write); err != nil {
		return err
	}
	changed := 0
	for _, f := range write {
		orig, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(f.Old)))
		if err != nil || f.Link != "" || string(orig) == string(f.Data) {
			continue
		}
		changed++
		diff, _ := engine.Silently(x, nil, "git", "-C", x.Value("imp_tmp"), "diff", "--no-index", "--no-color", "--",
			"src/"+f.Old, "new/"+f.New)
		x.UI.Text(diff)
	}
	x.UI.OK(fmt.Sprintf("%d files to write, %d of them changed from fleet-manager's", len(write), changed))

	if ok, err := x.Approve(fmt.Sprintf("Write %d files into %s?", len(write), flake)); !ok || err != nil {
		return err
	}
	x.Undo("import files", engine.Func(func(x *engine.Ctx) error {
		if _, err := engine.Cmd("git", append([]string{"-C", "{{.flake}}", "reset", "-q", "--"}, paths...)...).Quiet().OK("unstaged what was added").Run(x); err != nil {
			return err
		}
		for _, p := range paths {
			if err := os.RemoveAll(filepath.Join(flake, filepath.FromSlash(p))); err != nil {
				return err
			}
			_ = os.Remove(filepath.Join(flake, filepath.FromSlash(path.Dir(p)))) // an empty folder this run made
		}
		x.UI.OK("removed " + strings.Join(paths, ", "))
		return nil
	}))
	if err := writeImported(flake, write); err != nil {
		return err
	}
	x.UI.OK("wrote " + strings.Join(paths, ", "))
	return nil
}

func checkImported(x *engine.Ctx) error {
	if x.Options.DryRun {
		x.UI.Skip("dry run: nothing is written, so there is nothing to evaluate")
		return nil
	}
	host, address, flake := x.Value("host"), x.Value("imp_address"), x.Value("flake")
	paths := strings.Fields(x.Value("imp_paths"))
	if _, err := engine.Cmd("git", append([]string{"-C", "{{.flake}}", "add", "--"}, paths...)...).Quiet().
		OK("added the new files, since a git flake sees only what is tracked").Run(x); err != nil {
		return err
	}
	attr := func(opt string) string {
		return "{{.flake}}#" + x.Value("imp_kind") + "Configurations." + host + ".config." + opt
	}
	evalString := func(opt string) (string, error) {
		res, err := engine.Cmd("nix", "eval", "--json", attr(opt)).ReadOnly().Quiet().Run(x)
		if err != nil {
			return "", fmt.Errorf("evaluating %s: %w: %s", opt, err, strings.TrimSpace(res.Stderr))
		}
		var s *string
		if err := json.Unmarshal([]byte(res.Stdout), &s); err != nil {
			return "", fmt.Errorf("%s: %w", opt, err)
		}
		if s == nil {
			return "", nil
		}
		return *s, nil
	}

	url, err := evalString("conf.fleet.repo.url")
	if err != nil {
		return err
	}
	if url != address {
		return fmt.Errorf("%s would fetch %q, not %s: something in its files sets conf.fleet.repo.url to another value; change it to %s", host, url, address, address)
	}
	x.UI.OK(host + " fetches " + address)

	if x.Value("imp_host_branches") == "true" {
		ref, err := evalString("conf.fleet.repo.ref")
		if err != nil {
			return err
		}
		if ref != "host/"+host {
			return fmt.Errorf("%s sets conf.fleet.repo.ref itself (%q): remove it; the inventory sets host/%s", host, ref, host)
		}
		x.UI.OK(host + " fetches the branch " + ref)
	}

	res, err := engine.Cmd("nix", "eval", "--raw", attr("conf.fleet.signing.allowedSignersFile"),
		"--apply", `f: if f == null then "" else builtins.readFile f`).ReadOnly().Quiet().Run(x)
	if err != nil {
		return fmt.Errorf("evaluating conf.fleet.signing.allowedSignersFile: %w: %s", err, strings.TrimSpace(res.Stderr))
	}
	if res.Stdout == "" {
		x.UI.Note(host + " has no conf.fleet.signing.allowedSignersFile, so its signers file was not compared")
	} else {
		want, err := os.ReadFile(filepath.Join(flake, "fleet", "allowed_signers"))
		if err != nil {
			return err
		}
		if res.Stdout != string(want) {
			return fmt.Errorf("%s's signers file is not the inventory's fleet/allowed_signers: it must set conf.fleet.signing.allowedSignersFile to the path of the inventory's file", host)
		}
		x.UI.OK(host + "'s signers file is the inventory's")
	}

	hosts, err := evalHosts(x)
	if err != nil {
		return fmt.Errorf("a host does not evaluate with %s added: %w", host, err)
	}
	if hosts[host] == "" {
		return fmt.Errorf("%s is not among the inventory's hosts once added: is it in the folder of hosts?", host)
	}
	x.UI.OK(fmt.Sprintf("%s and the other %d host(s) evaluate", host, len(hosts)-1))

	if res, err := engine.Cmd("nix", "eval", "--json", attr("conf.secrets.repo.url")).ReadOnly().Quiet().Run(x); err == nil {
		var s *string
		if json.Unmarshal([]byte(res.Stdout), &s) == nil && s != nil && *s != "" {
			x.UI.Note(host + " has secrets, in " + *s + ": its secrets stay where they are")
		}
	}
	return nil
}

func commitImport(x *engine.Ctx) error {
	host := x.Value("host")
	paths := strings.Fields(x.Value("imp_paths"))
	if len(paths) == 0 {
		paths = []string{x.Value("imp_hosts_dir") + "/" + host}
	}
	x.Values["imp_subject"] = "feat(hosts): " + host + " moves here from fleet-manager"
	x.Values["imp_body"] = fmt.Sprintf("Copied from fleet-manager at %s: it now fetches %s, and its signers file is this repository's.",
		fleet.Short(x.Value("imp_commit")), x.Value("imp_address"))
	if _, err := engine.Cmd("git", append([]string{"-C", "{{.flake}}", "commit", "-q", "-m", "{{.imp_subject}}", "-m", "{{.imp_body}}", "--"}, paths...)...).
		OK("committed: {{.imp_subject}}").Run(x); err != nil {
		return err
	}
	if x.Options.DryRun {
		return nil
	}
	x.Done("import files")
	x.Kept("push import", "the commit “{{.imp_subject}}” in {{.flake}}, not pushed yet (git -C {{.flake}} push origin HEAD:refs/heads/main)")
	return nil
}

func pushImport(x *engine.Ctx) error {
	commit := ""
	if !x.Options.DryRun {
		var err error
		if commit, err = fleet.CommitOf(x, x.Value("flake"), "HEAD"); err != nil {
			return err
		}
	}
	if _, err := engine.Cmd("git", "-C", "{{.flake}}", "push", "origin", "HEAD:refs/heads/main").OK("pushed").Run(x); err != nil {
		return err
	}
	if x.Options.DryRun {
		return nil
	}
	x.Kept("push import", "")
	// What the tool pushed is what you trust on main now.
	return fleet.WriteTrusted(x.Value("inv_origin"), "main", commit)
}

func publishImport(x *engine.Ctx) error {
	if x.Options.DryRun {
		x.UI.Skip("dry run: host/" + x.Value("host") + " is cut and pushed once the commit is on main")
		return nil
	}
	return publishHosts(x, x.Value("host"))
}

func showImportNext(x *engine.Ctx) error {
	host := x.Value("host")
	flake := x.Value("flake_given")
	secrets := "<your secrets checkout>"
	if names, err := fleet.ReadNames(); err == nil {
		if _, _, others := addedRepos(names); len(others) > 0 {
			secrets = others[0]
		}
	}
	rm := "hosts/" + host
	users := strings.Fields(x.Value("imp_users"))
	sort.Strings(users)
	for _, u := range users {
		rm += " users/" + u
	}
	args := []string{"-C", x.Value("imp_dir"), "grep", "-l", "-w", host, x.Value("imp_commit"), "--", ":(exclude)hosts/" + host}
	for _, u := range users {
		args = append(args, ":(exclude)users/"+u)
	}
	var named []string
	if out, _ := engine.Silently(x, nil, "git", args...); out != "" {
		for _, l := range strings.Split(out, "\n") {
			if _, p, ok := strings.Cut(l, ":"); ok {
				named = append(named, p)
			}
		}
	}
	others := "nothing else names it"
	if len(named) > 0 {
		others = strings.Join(named, ", ")
	}
	x.UI.Text(host + "'s definition is in the inventory and its branch is published, but the machine still runs it from fleet-manager. To finish:\n" +
		"  1. Move the running machine: it registers the host's key on the inventory, checks its secrets, switches it, and makes /etc/nixos a checkout of host/" + host + ":\n" +
		"       fleet host enroll " + host + " --flake " + flake + " --repo " + secrets + "\n" +
		"  2. Check it runs from the inventory:\n" +
		"       fleet host check " + host + " --flake " + flake + "\n" +
		"  3. Then remove it from fleet-manager, in a checkout of it:\n" +
		"       git rm -r " + rm + "\n" +
		"       git commit -m \"chore(hosts): " + host + " moves to a private fleet\"\n" +
		"     and change what else names it there: " + others)
	return nil
}
