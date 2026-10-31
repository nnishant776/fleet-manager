# 7. Signing commits and trusting them

<!-- book:top — written by `fleet docs` from SUMMARY.md and the headings; do not edit -->
[← 6. Networking](networking.md) · [Contents](README.md) · [8. Secrets →](secrets.md)
<!-- /book:top -->

A fleet host builds only commits signed by a key it trusts, and never goes
back to an older one. The maintainer's tools hold themselves to the same
rules, with their own copy of the keys and of the commits they trusted last.
This chapter covers the signers file, signing your commits, those copies, the
tools' own clones, reviewing new commits, and rotating keys. Read it before
[Secrets](secrets.md) and [A private fleet](private-fleet.md), which build on
it.

<!-- book:sections -->
In this chapter:

- [The signers file](#the-signers-file)
- [Signing your commits](#signing-your-commits)
- [Your own copy of the keys](#your-own-copy-of-the-keys)
- [Your own copy of the trusted commits](#your-own-copy-of-the-trusted-commits)
- [The tools' own clones](#the-tools-own-clones)
- [Reviewing new commits](#reviewing-new-commits)
- [Keys in your agent](#keys-in-your-agent)
- [Merging](#merging)
- [Rotating or removing a key](#rotating-or-removing-a-key)
<!-- /book:sections -->

## The signers file

A fleet host refuses to build anything it fetched unless the commit is signed by
a key in `conf.fleet.signing.allowedSignersFile`. This repository keeps that
list in `fleet/allowed_signers`, one key per line:

```
<label> namespaces="git" <key-type> <key>
```

The label is free text. Git does not compare it with the commit's author, so it
does not have to be an email address. The file holds public keys only, and it
is committed so every machine carries it. A host uses it with
`conf.fleet.signing.allowedSignersFile = ../../fleet/allowed_signers;`.

## Signing your commits

To sign your commits to this repository only, run this in the checkout:

```bash
git config gpg.format ssh
git config user.signingKey ~/.ssh/<signing key>.pub   # the private key must be in your agent
git config commit.gpgSign true
git config tag.gpgSign true
git config gpg.ssh.allowedSignersFile ~/.local/state/fleet/repos/github.com/<owner>/<repo>/allowed_signers
```

## Your own copy of the keys

Point git at a copy of the keys kept outside
every checkout, not at the checkout's `fleet/allowed_signers`. A pushed commit
could add a key to that file and be signed with it; after a `git pull`, the
checkout's file would vouch for the very commit that changed it. Hosts are not
fooled this way, because they check against the file of the system they
already run. The `fleet` tool keeps one copy per repository, in
`$XDG_STATE_HOME/fleet/repos/<host>/<owner>/<repo>/` (`~/.local/state` by
default):

- `fleet trust signers --flake <checkout>` shows the checkout's keys with their
  fingerprints, marks yours, and keeps them once you approve. Later, it takes
  a change only through a commit signed by a key already in your copy, and
  shows the keys added and removed first. It also points the checkout's git
  (`gpg.ssh.allowedSignersFile`) at the copy, so that `git verify-commit` there
  checks against it too.
- `fleet setup` makes the copy and points git there, for the configuration
  checkout and the secrets checkout alike.
- Every command that reads a checkout starts by comparing its keys with your
  copy, and warns when they differ. If you expected the change, update the
  repository's trust with `fleet trust signers`.
- `fleet install prep` and `fleet host enroll` check commits against your
  copies. The secrets tools refuse to run while git checks against a file
  inside a checkout.

## Your own copy of the trusted commits

A signed commit is not always a new
one: someone who can push can serve an old signed commit again. `fleet inventory
publish` would then cut every host's branch from it as a new commit signed by
you, which the hosts accept, so your signature would hide the rollback. Hosts
refuse an older commit with their own records; your machine keeps the same, one
per repository and branch, next to the copy of the keys:
`$XDG_STATE_HOME/fleet/repos/<host>/<owner>/<repo>/trusted/<branch>` (a branch
with a `/` is a nested path, such as `trusted/host/laptop1`). The file holds one
line, a full commit hash, and is readable by you only.

- The first time a branch is checked, its tip is accepted and recorded, and the
  tool says so. After that a tip is accepted only when it is the recorded
  commit or follows it. Otherwise the tool stops and names the record, because
  the branch could be an old commit served again. The signature is always
  checked first.
- A tip that passes moves the record to it. After a tool pushes a commit of its
  own, the record moves to what it pushed. A commit that was not pushed moves
  nothing.
- A fleet-manager commit that is pinned (the one `fleet install prep` takes
  the installer from, or in an inventory's `flake.lock`) must be on the
  trusted line of fleet-manager's `main`: the recorded commit, one before it,
  or one after it, which moves the record. The pin and every commit it brings
  over the previous pin (or else over the recorded commit) must each be signed
  by a key you trust: a signed commit on top does not vouch for unsigned ones
  under it.
- `fleet install prep`, `fleet inventory publish` (`main` and each host's
  branch), `fleet host enroll`, the commands that change a host's or user's
  files in the secrets repository (`fleet host secret`, `fleet secrets …`) and
  `fleet user …` check and record. So does the shell `fleet-secrets`, in the
  same files. `host-retire` and `user-retire` remove the branch's record.
- If the history was rewritten on purpose, a repository that has a name
  (`fleet repo add`) is reviewed and accepted with `fleet repo sync <name>
  --accept-rewrite <branch>`, described below. For a checkout without a name,
  remove the record file the message names and run the command again: the tip
  you see is then trusted afresh.
- The records go by the address of the clone's `origin`, so a clone of a
  repository on this machine needs a `file://` address
  (`git remote set-url origin file:///path/to/repo.git`). A shallow clone
  cannot tell whether a tip follows the record, and says so: fetch the rest
  with `git fetch --unshallow`.

## The tools' own clones

Checking a checkout you work in is not the same as
checking what the tools read, because you may have edited it. `fleet repo add
<address>` keeps a clone of a repository just for the tools, under
`$XDG_DATA_HOME/fleet/repos/<host>/<owner>/<repo>` (`~/.local/share` by
default), and gives it a name. You never edit it by hand. `--flake <name>` and `--repo
<name>` then mean that clone, and every command that is given a name first
fetches it, takes `main`'s newest commit only if a key you trust signed it and
it follows the commit you trusted last, and leaves the clone there. A path
still works as before, and is not fetched. `fleet repo list` shows the names,
and the commits you trust.

Run `fleet repo add` three times. Each time it says which repository it adds
next: fleet-manager first, whose address it knows, then your inventory, then
your secrets repository, and asks for the address of the one it needs. You can
also give each address yourself, in the same order:

```
fleet repo add git@github.com:example-org/fleet-manager.git
fleet repo add git@github.com:example-org/inventory.git
fleet repo add git@github.com:example-org/secrets.git --signers-from inventory
```

Each clone is named after the last part of its address, unless you give
`--name`. The secrets repository has no `fleet/allowed_signers`, so its
commits are checked against your inventory's keys, as the hosts check them.
When `fleet repo add` asks for the secrets repository itself, it takes the
inventory's name. With an address you give, `--signers-from` names the
inventory (or fleet-manager, if its own hosts use the secrets), and on a
terminal you are asked if you leave it out. Edit in a checkout of your own, as
before. In the clone only the tools work: they fetch it, and the secrets
commands commit there and push from there, as they do in a checkout. A commit
a command does not push stays in the clone, and the command says how to push
it.

## Reviewing new commits

`fleet repo sync [NAME...]` is how the commits you trust move forward. It
fetches the clone of each repository you name (every one by default), and for
`main`, each host's branch and each user's branch it lists the new commits
since the ones you trusted: date, author, subject, and the key that signed
each. A repository whose keys have their own file is looked at first, because
the others, such as the secrets repository, are checked against its keys. You
are asked "Accept these?", or give `--yes`; then the commits you trust and the
clone move to what you saw, and nothing is written before that. A repository is
refused whole, and nothing is written for it, when a branch went backwards or
was rewritten, when the newest commit of `main` or of a host's branch is not
signed by a key you trust, or when the list of keys changed in a commit that no
key you already trust signed. Every new commit of `main` and of a host's
branch must be signed by a key you trust, not only the newest, because a signed
commit on top does not vouch for the ones under it. If one is not, the
repository is refused; at a terminal, but not with `--yes`, you can accept
them by typing the branch's name. If the history was rewritten on purpose and
you know why, `fleet repo sync <name> --accept-rewrite <branch>` shows the commits
that were dropped and the new ones, and takes the branch once you type its
name; this needs a terminal, even with `--yes`. A user's branch is signed by
the user's own key, which only the encrypted files name, so when it is not
signed by one of your keys it is shown and left alone: the secrets commands
check it against the user's key. Accepting a rewrite of a user's branch drops
its record, and the next secrets command takes the branch afresh after that
check.

## Keys in your agent

While the key is in your agent, any program running as you can sign with it.
`ssh-add -c` makes the agent ask you before each signature, and `ssh-add -t`
removes the key after a set time.

## Merging

Merge pull requests on your own machine and push the result, for example with
`git merge --ff-only`. GitHub's merge buttons either drop your signature or
replace it with GitHub's own, and the fleet does not trust GitHub's key.

## Rotating or removing a key

A host checks a new commit against the file in the configuration it already
runs, not the one it just fetched, so a pushed commit cannot add a key and
approve itself. Rotating a key means adding the new one in a commit signed by a
key already trusted, waiting for the fleet to converge, then removing the old
one. A lost or stolen key is removed the same way, in a
commit signed by a key still trusted. Do not rely on `valid-before` for that:
Git checks it against the commit's own date, which the signer chooses, so a
stolen key can still sign backdated commits; the option only suits retiring a
key on a planned date.

<!-- book:bottom -->
---

[← 6. Networking](networking.md) · [Contents](README.md) · [8. Secrets →](secrets.md)
<!-- /book:bottom -->
