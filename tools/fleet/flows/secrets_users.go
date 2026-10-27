package flows

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/secrets"
)

// A user listed in hosts/<host>.yaml keeps their files on a branch of their
// own, user/<user> (see internal/secrets/users.go). user-enroll lists a user
// on the hosts and moves their files there; user-retire takes them off the
// hosts again.

var userAgePattern = regexp.MustCompile(`^age1[0-9a-z]+$`)

// UserFromInput is where a user's enrollment file comes from.
var UserFromInput = engine.Input{
	Name:   "from",
	Prompt: "Enrollment file",
	Help:   "The user's enrollment file (version, user, age_recipient and signing_key), or - for standard input",
}

func repoGit(args ...string) *engine.Command { return engine.Cmd("git", args...).In("{{.repo}}") }

// hasPath says whether the ref holds the path.
func hasPath(x *engine.Ctx, ref, path string) bool {
	_, err := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "cat-file", "-e", ref+":"+path)
	return err == nil
}

// userHostPlan is one host user-enroll works on, and what is left to do there.
type userHostPlan struct {
	Name      string
	Ref       string // the host's branch
	Recipient string // the host's age recipient
	Listed    bool   // hosts/<host>.yaml lists the user already
	HasFile   bool   // the host's branch holds users/<user>/<host>.yaml
	UserFile  bool   // user/<user> holds it
}

func loadPlan(x *engine.Ctx) (plan []userHostPlan) {
	_ = json.Unmarshal([]byte(x.Value("plan")), &plan)
	return plan
}

func userFilePath(user, host string) string { return "users/" + user + "/" + host + ".yaml" }

// SecretsUserEnroll lists a user on hosts and moves their files to their branch.
var SecretsUserEnroll = &engine.Flow{
	Path:  []string{"secrets", "user-enroll"},
	Title: "Enroll a user in the secrets repository",
	Short: "List a user on hosts and move their files to a branch of their own",
	Long: "Takes the user's enrollment file (version, user, age_recipient, signing_key) and gives the user a branch,\n" +
		"user/<user>, which holds that file as enroll and, for each host, users/<user>/<host>.yaml encrypted to\n" +
		"the user, the host and your recovery recipient. The hosts are every host whose branch holds a file of\n" +
		"the user's, and each --host: a file there is re-encrypted with the user's recipient added and moved\n" +
		"to the user's branch, and a host without one gets a new, empty file. On each host's branch the user is\n" +
		"listed in hosts/<host>.yaml (users.<user>, encrypted: their recipient and signing key) and the file is\n" +
		"removed. Commits (user-enroll: <user>, and user-enroll: <user> on <host>) and pushes user/<user> first,\n" +
		"then each host's branch. The same identity again changes nothing; another identity is refused. Hosts\n" +
		"must run a sync that reads user branches before a user is enrolled, or they lose the user's files.",
	Example: "  fleet secrets user-enroll alice --from alice.enroll\n  fleet secrets user-enroll alice --from alice.enroll --host laptop1 --host nas",
	Args:    []string{"user"},
	Inputs: []engine.Input{secrets.RepoInput, secrets.RecoveryInput, secrets.UserName(), UserFromInput,
		{Name: "host", Prompt: "Hosts to list the user on besides those holding their files", Optional: true, Many: true, Pattern: secrets.NamePattern,
			Help:    "A host to list the user on, whether or not its branch holds a file of theirs; may be given more than once",
			Default: func(*engine.Ctx) (string, error) { return "", nil }},
	},
	Needs: []string{"git", "yq", "sops", "age", "ssh-keygen"},
	Steps: []engine.Step{
		secrets.Open(true),
		{
			Title: "Read the enrollment file",
			Why:   "It holds the user's two public keys: the age recipient their files are encrypted to, and the key their commits are signed with.",
			Do:    engine.Func(readUserEnrollment),
		},
		{
			Title: "Look up user/{{.user}}",
			Why:   "A user's branch is user/<name>; a user that has one is enrolled. The same identity again changes nothing there; another identity is re-keyed, not enrolled over.",
			Do:    engine.Func(lookUpUserBranch),
		},
		secrets.Unlock(),
		{
			Title: "Find the hosts and what is left to do on them",
			Why: "Every host whose branch holds a file of {{.user}}'s, and each --host. The lists in their files are encrypted, so this needs your recovery identity. " +
				"A host that lists the user with another identity stops the run.",
			Do: engine.Func(planUserHosts),
		},
		secrets.Unless(engine.Step{
			Title: "Write user/{{.user}}",
			Why: "In a temporary worktree: the enrollment file as given, and for each host the user's file, re-encrypted with the user's recipient added when the host's branch had one " +
				"and new and empty when it had none. Then one commit.",
			Do: engine.Func(writeUserBranch),
		}),
		secrets.Unless(engine.Step{
			Title: "List {{.user}} on the hosts and take their files off the hosts' branches",
			Why:   "In a temporary worktree of each host's branch: users.<user> in hosts/<host>.yaml, which is encrypted, and the file removed. One commit per host.",
			Do:    engine.Func(listOnHosts),
		}),
		secrets.Unless(engine.Step{
			Title: "Push user/{{.user}}",
			Why:   "First, so that the hosts' branches never point at a user branch that is not there.",
			When: func(x *engine.Ctx) (bool, string) {
				if x.Value("user_committed") != "true" {
					return false, "nothing was committed on it"
				}
				return true, ""
			},
			Do: engine.Cmd("git", "push", "origin", "user/{{.user}}").In("{{.repo}}").After(func(x *engine.Ctx) error {
				x.Kept("push user/"+x.Value("user"), "")
				return secrets.Pushed(x, "user/"+x.Value("user"))
			}),
		}),
		secrets.Unless(engine.Step{
			Title: "Push the hosts' branches",
			Why:   "A host reads the user from its branch on its next sync, which checks the commit is signed by an allowed key.",
			When: func(x *engine.Ctx) (bool, string) {
				if x.Value("hosts_committed") == "" {
					return false, "nothing was committed on them"
				}
				return true, ""
			},
			Do: engine.Func(func(x *engine.Ctx) error {
				for _, h := range strings.Fields(x.Value("hosts_committed")) {
					if _, err := repoGit("push", "origin", "host/"+h).Run(x); err != nil {
						return err
					}
					x.Kept("push host/"+h, "")
					if err := secrets.Pushed(x, "host/"+h); err != nil {
						return err
					}
				}
				return nil
			}),
		}),
	},
}

// readUserEnrollment reads and checks the user's enrollment file, and keeps
// it, their age recipient and their signing key as values.
func readUserEnrollment(x *engine.Ctx) error {
	var doc []byte
	var err error
	if from := x.Value("from"); from == "-" {
		doc, err = io.ReadAll(x.Stdin)
	} else if _, serr := os.Stat(from); serr != nil {
		return fmt.Errorf("no such file: %s", from)
	} else {
		doc, err = os.ReadFile(from)
	}
	if err != nil {
		return err
	}
	x.Values["enrollment"] = string(doc)
	field := func(expr string) (string, error) { return yqField(x, "enrollment", expr) }
	version, err := field(".version")
	if err != nil {
		return err
	}
	user, err := field(".user")
	if err != nil {
		return err
	}
	recipient, err := field(".age_recipient")
	if err != nil {
		return err
	}
	key, err := field(".signing_key")
	if err != nil {
		return err
	}
	if version != "1" {
		return fmt.Errorf("unsupported enrollment file version '%s'", version)
	}
	if user != x.Value("user") {
		return fmt.Errorf("enrollment file is for user '%s', not '%s'", user, x.Value("user"))
	}
	if !userAgePattern.MatchString(recipient) {
		return errors.New("malformed age_recipient in the enrollment file")
	}
	if key == "" || key == "null" || strings.ContainsAny(key, "\r\n") {
		return errors.New("signing_key in the enrollment file is not one SSH public key line")
	}
	if _, err := engine.Cmd("ssh-keygen", "-l", "-f", "/dev/stdin").Stdin(key).StdinNote("the key").RunQuiet(x); err != nil {
		return errors.New("signing_key in the enrollment file is not an SSH public key: " + key)
	}
	x.Values["user_recipient"], x.Values["signing_key"] = recipient, key
	x.UI.OK("for " + user + ", recipient " + recipient)
	return nil
}

// yqField reads one field of a document kept as the value from.
func yqField(x *engine.Ctx, from, expr string) (string, error) {
	res, err := engine.Cmd("yq", expr).StdinFrom(from).StdinNote("the enrollment file").RunQuiet(x)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Stdout), nil
}

// lookUpUserBranch finds user/<user>; a branch that holds this very identity
// sets the value known, one that holds another stops the run.
func lookUpUserBranch(x *engine.Ctx) error {
	user := x.Value("user")
	ref := secrets.UserBranchRef(x, user)
	x.Values["user_ref"] = ref
	if ref == "" {
		x.UI.OK("user/" + user + " does not exist yet")
		return nil
	}
	if err := secrets.VerifyUserTip(x, ref, user, x.Value("signing_key")); err != nil {
		return err
	}
	doc, err := repoGit("show", ref+":enroll").RunQuiet(x)
	if err != nil {
		return fmt.Errorf("%s has no enroll file", ref)
	}
	x.Values["old_enrollment"] = doc.Stdout
	recipient, err := yqField(x, "old_enrollment", ".age_recipient")
	if err != nil {
		return err
	}
	key, err := yqField(x, "old_enrollment", ".signing_key")
	if err != nil {
		return err
	}
	if recipient != x.Value("user_recipient") || key != x.Value("signing_key") {
		return fmt.Errorf("user '%s' is already enrolled with another identity", user)
	}
	x.Values["known"] = "true"
	x.UI.OK(ref + " holds this identity already")
	return nil
}

// planUserHosts works out the hosts and what is to be done on each, keeps it
// as the value plan, and sets done when there is nothing to do at all.
func planUserHosts(x *engine.Ctx) error {
	user, uref := x.Value("user"), x.Value("user_ref")
	id := secrets.Identity{AgeRecipient: x.Value("user_recipient"), SigningKey: x.Value("signing_key")}
	names, err := enrolledHosts(x)
	if err != nil {
		return err
	}
	var hosts []string
	for _, h := range names {
		if hasPath(x, secrets.BranchRef(x, h), userFilePath(user, h)) {
			hosts = append(hosts, h)
		}
	}
	for _, h := range strings.Fields(x.Value("host")) {
		if !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	slices.Sort(hosts)
	var plan []userHostPlan
	for _, h := range hosts {
		ref := secrets.BranchRef(x, h)
		if ref == "" {
			return fmt.Errorf("host '%s' is not enrolled; use host-enroll first", h)
		}
		if err := secrets.VerifyTip(x, ref); err != nil {
			return err
		}
		rec, err := secrets.HostRecipient(x, ref, h, x.Value("recovery_pub"))
		if err != nil {
			return err
		}
		listed, got, err := secrets.ReadListing(x, ref, h, user)
		if err != nil {
			return err
		}
		if listed && got != id {
			return fmt.Errorf("hosts/%s.yaml lists user '%s' with another identity", h, user)
		}
		e := userHostPlan{Name: h, Ref: ref, Recipient: rec, Listed: listed,
			HasFile:  hasPath(x, ref, userFilePath(user, h)),
			UserFile: uref != "" && hasPath(x, uref, userFilePath(user, h))}
		if e.Listed && !e.HasFile && e.UserFile {
			x.UI.OK(h + ": done already")
			continue
		}
		plan = append(plan, e)
		var todo []string
		switch {
		case e.UserFile:
		case e.HasFile:
			todo = append(todo, "move its file to user/"+user)
		default:
			todo = append(todo, "create an empty file on user/"+user)
		}
		if !e.Listed {
			todo = append(todo, "list "+user)
		}
		if e.HasFile {
			todo = append(todo, "remove the file from host/"+h)
		}
		x.UI.OK(h + ": " + strings.Join(todo, ", "))
	}
	raw, _ := json.Marshal(plan)
	x.Values["plan"] = string(raw)
	if x.Value("known") == "true" && len(plan) == 0 {
		x.Values["done"] = "true"
		x.UI.OK(user + " is enrolled on every host already; nothing to do")
	} else if len(plan) == 0 {
		x.UI.OK("no host holds a file of " + user + "'s and none was given with --host: the branch gets the enrollment file only")
	}
	return nil
}

// writeUserBranch makes the commit on user/<user>: the enrollment file, and
// the user's file for each host that lacks one there.
func writeUserBranch(x *engine.Ctx) error {
	user, plan := x.Value("user"), loadPlan(x)
	known := x.Value("known") == "true"
	need := !known
	for _, e := range plan {
		need = need || !e.UserFile
	}
	if !need {
		x.UI.Skip("user/" + user + " holds all its files already")
		return nil
	}
	branch := "user/" + user
	dir, err := secrets.WorktreeAt(x, branch, x.Value("user_ref"))
	if err != nil {
		return err
	}
	x.Values["work"] = dir
	var add []string
	if !known {
		if !x.Options.DryRun {
			if err := os.WriteFile(filepath.Join(dir, "enroll"), []byte(strings.TrimRight(x.Value("enrollment"), "\n")+"\n"), 0o644); err != nil {
				return err
			}
		}
		x.UI.OK("wrote enroll")
		add = append(add, "enroll")
	}
	for _, e := range plan {
		if e.UserFile {
			continue
		}
		rel := userFilePath(user, e.Name)
		if e.HasFile {
			err = moveUserFile(x, e, rel)
		} else {
			err = secrets.CreateEmptyFile(x, rel, e.Recipient+","+x.Value("user_recipient"))
		}
		if err != nil {
			return err
		}
		add = append(add, rel)
	}
	if _, err := engine.Cmd("git", append([]string{"add"}, add...)...).In("{{.work}}").Run(x); err != nil {
		return err
	}
	msg := "user-enroll: " + user
	if _, err := engine.Cmd("git", "commit", "-q", "-m", msg).In("{{.work}}").OK("committed: " + msg).Run(x); err != nil {
		return err
	}
	x.Values["user_committed"] = "true"
	x.Kept("push "+branch, "the commit “"+msg+"” on "+branch+" in {{.repo}}, not pushed yet (git -C {{.repo}} push origin "+branch+")")
	return secrets.CloseWorktree(x, branch, dir)
}

// moveUserFile puts the host branch's file of the user in the user's worktree,
// re-encrypted with the user's recipient added, and checks it is encrypted to
// the user, the host and the recovery recipient and no one else.
func moveUserFile(x *engine.Ctx, e userHostPlan, rel string) error {
	doc, err := repoGit("show", e.Ref+":"+rel).RunQuiet(x)
	if err != nil {
		return err
	}
	if !x.Options.DryRun {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(x.Value("work"), rel)), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(x.Value("work"), rel), []byte(doc.Stdout), 0o644); err != nil {
			return err
		}
	}
	if _, err := engine.Cmd("sops", "rotate", "-i", "--add-age", "{{.user_recipient}}", rel).In("{{.work}}").Run(x); err != nil {
		return err
	}
	if x.Options.DryRun {
		return nil
	}
	res, err := engine.Cmd("yq", ".sops.age[].recipient", rel).In("{{.work}}").RunQuiet(x)
	if err != nil {
		return err
	}
	got := strings.Fields(res.Stdout)
	want := []string{e.Recipient, x.Value("user_recipient"), x.Value("recovery_pub")}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return fmt.Errorf("%s is encrypted to %s, not to exactly the host, the user and your recovery recipient (%s): re-key the host first",
			rel, strings.Join(got, ", "), strings.Join(want, ", "))
	}
	x.UI.OK("moved " + rel + ", now also encrypted to the user")
	return nil
}

// listOnHosts commits, on each host's branch that needs it, the user in the
// host's file and the removal of the user's file.
func listOnHosts(x *engine.Ctx) error {
	user := x.Value("user")
	var committed []string
	for _, e := range loadPlan(x) {
		if e.Listed && !e.HasFile {
			continue
		}
		branch, rel, file := "host/"+e.Name, userFilePath(user, e.Name), "hosts/"+e.Name+".yaml"
		dir, err := secrets.WorktreeAt(x, branch, e.Ref)
		if err != nil {
			return err
		}
		x.Values["work"] = dir
		if !e.Listed {
			x.Values["user_json"] = `{"age_recipient":` + secrets.JSON(x.Value("user_recipient")) + `,"signing_key":` + secrets.JSON(x.Value("signing_key")) + `}`
			if _, err := secrets.SetAt(file, `["users"]["`+user+`"]`, "user_json", "the user's identity").Run(x); err != nil {
				return err
			}
			if _, err := engine.Cmd("git", "add", file).In("{{.work}}").Run(x); err != nil {
				return err
			}
		}
		if e.HasFile {
			if _, err := engine.Cmd("git", "rm", "-q", rel).In("{{.work}}").Run(x); err != nil {
				return err
			}
		}
		msg := "user-enroll: " + user + " on " + e.Name
		if _, err := engine.Cmd("git", "commit", "-q", "-m", msg).In("{{.work}}").OK("committed: " + msg).Run(x); err != nil {
			return err
		}
		committed = append(committed, e.Name)
		x.Values["hosts_committed"] = strings.Join(committed, "\n")
		x.Kept("push "+branch, "the commit “"+msg+"” on "+branch+" in {{.repo}}, not pushed yet (git -C {{.repo}} push origin "+branch+")")
		if err := secrets.CloseWorktree(x, branch, dir); err != nil {
			return err
		}
	}
	if len(committed) == 0 {
		x.UI.Skip("every host lists the user already, and none holds a file of theirs")
	}
	return nil
}

// SecretsUserRetire takes a user off the hosts that list them.
var SecretsUserRetire = &engine.Flow{
	Path:  []string{"secrets", "user-retire"},
	Title: "Retire a user from the secrets repository",
	Short: "Take a user off the hosts that list them and delete their branch",
	Long: "Removes users.<user> from the file of every host that lists the user (a commit on the host's branch,\n" +
		"user-retire: <user> on <host>, pushed), deletes user/<user> in your clone after saying what it held,\n" +
		"and shows what is still to do: deleting it on the forge. Take the user's accounts out of the\n" +
		"inventory first.",
	Example: "  fleet secrets user-retire alice",
	Args:    []string{"user"},
	Inputs:  []engine.Input{secrets.RepoInput, secrets.RecoveryInput, secrets.UserName()},
	Needs:   []string{"git", "yq", "sops", "age"},
	Steps: []engine.Step{
		secrets.Open(false),
		secrets.Unlock(),
		{
			Title: "Find the hosts that list {{.user}}",
			Why:   "The hosts whose file has users.<user>. The keys of an encrypted file are in clear, so the list is read as it is in git.",
			Do:    engine.Func(findListingHosts),
		},
		{
			Title: "Remove {{.user}} from their files",
			Why:   "In a temporary worktree of each host's branch: users.<user> removed from hosts/<host>.yaml, which sops does with your recovery identity. One commit per host. From the next sync on the host takes the user's files from its own branch again, if it has any.",
			Do:    engine.Func(unlistOnHosts),
		},
		{
			Title: "Push the hosts' branches",
			Why:   "A host stops reading the user's branch on its next sync, which checks the commit is signed by an allowed key.",
			When: func(x *engine.Ctx) (bool, string) {
				if x.Value("hosts_committed") == "" {
					return false, "no host listed the user"
				}
				return true, ""
			},
			Do: engine.Func(func(x *engine.Ctx) error {
				for _, h := range strings.Fields(x.Value("hosts_committed")) {
					if _, err := repoGit("push", "origin", "host/"+h).Run(x); err != nil {
						return err
					}
					x.Kept("push host/"+h, "")
					if err := secrets.Pushed(x, "host/"+h); err != nil {
						return err
					}
				}
				return nil
			}),
		},
		{
			Title: "Show what user/{{.user}} held",
			Why:   "The files that go with the branch.",
			When: func(x *engine.Ctx) (bool, string) {
				if x.Value("user_ref") == "" {
					return false, "there is no branch user/" + x.Value("user") + " in your clone, local or fetched"
				}
				return true, ""
			},
			Do: engine.Cmd("git", "ls-tree", "-r", "--name-only", "{{.user_ref}}").In("{{.repo}}").ReadOnly(),
		},
		{
			Title: "Delete the local branch user/{{.user}}",
			Why:   "With it go the user's files on it.",
			When: func(x *engine.Ctx) (bool, string) {
				if x.Value("user_ref") != "refs/heads/user/"+x.Value("user") {
					return false, "there is no local branch; only origin's copy remains, or none"
				}
				return true, ""
			},
			Do: engine.Cmd("git", "branch", "-D", "-q", "user/{{.user}}").In("{{.repo}}").OK("deleted"),
		},
		{
			Title: "Forget the commit you trusted on user/{{.user}}",
			Why:   "The branch is going away, so the record of its last trusted commit (a file in your fleet state folder) goes too; if the user is enrolled again, their branch is trusted afresh.",
			Do: engine.Func(func(x *engine.Ctx) error {
				if err := secrets.Retired(x, "user/"+x.Value("user")); err != nil {
					return err
				}
				x.UI.OK("removed")
				return nil
			}),
		},
		{
			Title: "Still to do",
			Why:   "The branch is gone from your clone only.",
			Do: engine.Show("{{.user}} is retired. Still to do:\n" +
				"  1. Delete the branch on the forge, which takes the user's files away from the hosts:\n" +
				"       git -C {{.repo}} push origin --delete user/{{.user}}\n" +
				"  2. Rotate what the user could decrypt: their password and SSH keys on the hosts they were listed on,\n" +
				"     and revoke their public keys on the forge now."),
		},
	},
}

// findListingHosts keeps the hosts that list the user as the value
// listing_hosts, one per line, and the user's branch as user_ref.
func findListingHosts(x *engine.Ctx) error {
	user := x.Value("user")
	names, err := enrolledHosts(x)
	if err != nil {
		return err
	}
	var hosts []string
	for _, h := range names {
		listed, err := secrets.IsListed(x, secrets.BranchRef(x, h), h, user)
		if err != nil {
			return err
		}
		if listed {
			if err := secrets.VerifyTip(x, secrets.BranchRef(x, h)); err != nil {
				return err
			}
			hosts = append(hosts, h)
		}
	}
	x.Values["listing_hosts"] = strings.Join(hosts, "\n")
	x.Values["user_ref"] = secrets.UserBranchRef(x, user)
	if len(hosts) == 0 {
		x.UI.OK("no host lists " + user)
	} else {
		x.UI.OK("listed on " + strings.Join(hosts, ", "))
	}
	return nil
}

// unlistOnHosts commits, on the branch of each host that lists the user, the
// removal of users.<user>.
func unlistOnHosts(x *engine.Ctx) error {
	user := x.Value("user")
	var committed []string
	for _, h := range strings.Fields(x.Value("listing_hosts")) {
		branch, file := "host/"+h, "hosts/"+h+".yaml"
		dir, err := secrets.WorktreeAt(x, branch, secrets.BranchRef(x, h))
		if err != nil {
			return err
		}
		x.Values["work"] = dir
		if _, err := engine.Cmd("sops", "unset", file, `["users"]["`+user+`"]`).In("{{.work}}").Run(x); err != nil {
			return err
		}
		if _, err := engine.Cmd("git", "add", file).In("{{.work}}").Run(x); err != nil {
			return err
		}
		msg := "user-retire: " + user + " on " + h
		if _, err := engine.Cmd("git", "commit", "-q", "-m", msg).In("{{.work}}").OK("committed: " + msg).Run(x); err != nil {
			return err
		}
		committed = append(committed, h)
		x.Values["hosts_committed"] = strings.Join(committed, "\n")
		x.Kept("push "+branch, "the commit “"+msg+"” on "+branch+" in {{.repo}}, not pushed yet (git -C {{.repo}} push origin "+branch+")")
		if err := secrets.CloseWorktree(x, branch, dir); err != nil {
			return err
		}
	}
	if len(committed) == 0 {
		x.UI.Skip("no host lists the user")
	}
	return nil
}

// rekeyListedUsers replaces the host's old recipient (host_recipient) by its
// new one (recipient) in the file for the host of each user that the host's
// file lists, on the user's branch, and commits each: host-rekey: <host>.
func rekeyListedUsers(x *engine.Ctx) error {
	host, ref := x.Value("host"), x.Value("ref")
	doc, err := repoGit("show", ref+":hosts/"+host+".yaml").RunQuiet(x)
	if err != nil {
		return err
	}
	x.Values["host_file"] = doc.Stdout
	res, err := engine.Cmd("yq", `(.users // {}) | keys | .[]`).StdinFrom("host_file").StdinNote("the host's file").RunQuiet(x)
	if err != nil {
		return err
	}
	users := strings.Fields(res.Stdout)
	if len(users) == 0 {
		x.UI.Skip("the host lists no user")
		return nil
	}
	for _, u := range users {
		uref := secrets.UserBranchRef(x, u)
		if uref == "" {
			return fmt.Errorf("%s is listed in hosts/%s.yaml, but there is no user/%s in %s, local or fetched from origin: fetch it", u, host, u, x.Value("repo"))
		}
		key, err := secrets.UserSigningKey(x, u)
		if err != nil {
			return err
		}
		if err := secrets.VerifyUserTip(x, uref, u, key); err != nil {
			return err
		}
		rel := userFilePath(u, host)
		if !hasPath(x, uref, rel) {
			x.UI.Skip(u + " has no file for " + host + " on user/" + u)
			continue
		}
		recipients, err := repoGit("show", uref+":"+rel).RunQuiet(x)
		if err != nil {
			return err
		}
		x.Values["user_file"] = recipients.Stdout
		res, err := engine.Cmd("yq", ".sops.age[].recipient").StdinFrom("user_file").StdinNote("the user's file").RunQuiet(x)
		if err != nil {
			return err
		}
		if slices.Contains(strings.Fields(res.Stdout), x.Value("recipient")) {
			x.UI.Skip(rel + " on user/" + u + " has the new recipient already")
			continue
		}
		branch := "user/" + u
		dir, err := secrets.WorktreeAt(x, branch, uref)
		if err != nil {
			return err
		}
		x.Values["work"] = dir
		if _, err := engine.Cmd("sops", "rotate", "-i", "--rm-age", "{{.host_recipient}}", "--add-age", "{{.recipient}}", rel).In("{{.work}}").Run(x); err != nil {
			return err
		}
		if _, err := engine.Cmd("git", "add", rel).In("{{.work}}").Run(x); err != nil {
			return err
		}
		msg := "host-rekey: " + host
		if _, err := engine.Cmd("git", "commit", "-q", "-m", msg).In("{{.work}}").OK("committed: " + msg).Run(x); err != nil {
			return err
		}
		x.Kept("push "+branch, "the commit “"+msg+"” on "+branch+" in {{.repo}}, not pushed yet (git -C {{.repo}} push origin "+branch+")")
		if err := secrets.CloseWorktree(x, branch, dir); err != nil {
			return err
		}
	}
	return nil
}
