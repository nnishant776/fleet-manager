<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet repo add

Clone a repository for the tools to work in, check its keys and main, and give it a name.

The tools keep their own clone of each repository, under ~/.local/share/fleet/repos
(XDG_DATA_HOME), which nobody edits. fleet repo add clones it, keeps your copy of the
keys that sign its commits (the repository's own fleet/allowed_signers, or those of
the repository named by --signers-from, for one without), checks main and the hosts'
branches, and names the clone. After that --flake NAME and --repo NAME mean the clone,
which every command fetches first, taking main's newest commit only if a key you trust
signed it and it follows the commit you trusted last.

```
fleet repo add [URL] [flags]

fleet repo add git@github.com:example-org/fleet-manager.git
fleet repo add git@github.com:example-org/inventory.git
fleet repo add git@github.com:example-org/secrets.git --signers-from inventory
```

## Flags

| Flag | |
|---|---|
| `--url` | Its SSH or https address, as git clones it; a local one needs file:// |
| `--name` | What --flake and --repo take for it; the last part of its address by default |
| `--signers-from` | For a repository without fleet/allowed_signers, such as the secrets repository: the name of the repository whose keys sign its commits |
| `--signing-key` | The SSH private key that signs commits made in the clone; made by fleet setup |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Clone it.** The tools work in a clone of their own, kept under ~/.local/share/fleet/repos, so that what they read is not a checkout you edit. It is left on main's newest commit, detached.

2. **Trust its keys.** Commits are checked against your own copy of the keys that sign them, not against a file a pushed commit could change. A repository with a fleet/allowed_signers gives them; one without, such as the secrets repository, is signed with the keys of another, named by --signers-from.

3. **Check main and trust it.** Main's newest commit must be signed by a key you trust. It becomes the first commit you trust for main, and later ones are accepted only when they follow it.

4. **Record the hosts' branches.** Each host has a branch, host/NAME. The same is done for the ones signed by a key you trust, so that an old commit served again is noticed. The branches of users, user/NAME, are signed by their users, and are recorded the first time a command checks them.

5. **Sign the commits made in it.** Hosts accept only signed commits, so the clone is set to sign the ones made in it with your signing key, as fleet setup does for your checkouts.

6. **Install the secrets repository's commit check.** Its pre-commit hook refuses anything under hosts/ or users/ that is not ciphertext. Git runs a hook as you, with your agent at hand, so what is installed is a copy you have read, not the repository's own file, which anyone who can push could change.

7. **Name it.** So that --flake NAME or --repo NAME means this clone. A name names one repository only.

8. **Next.** How to use it.

   > Use the name wherever a command takes --flake or --repo: fleet inventory publish --flake NAME
   > Edit in a checkout of your own; never edit the tool's clone by hand.

It needs `git` on the PATH; `fleet doctor` says where to get them.
