<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet repo sync

Fetch the tools' clones, show the new commits and who signed them, and accept them.

Fetches the tools' clone of each repository (or of the ones you name) and shows what
is new since the commits you trusted: for main, each host's branch and each user's
branch, the new commits with their date, author and the key that signed them. If you
accept, the commits you trust move to them and the clone moves to main.

A repository is refused, and nothing is written for it, when a branch went backwards
or was rewritten, when the newest commit of main or of a host's branch is not signed
by a key you trust, or when its list of keys changed in a commit that no key you
already trust signed. A user's branch is signed by its user, with a key that only the
encrypted files name; if the newest commit is not signed by one of your keys, the
branch is left alone, and the secrets commands check it against the user's own key.

If a history was rewritten on purpose and you know why, --accept-rewrite BRANCH
shows what was dropped and what is new, and takes it once you type the branch's name
at a terminal. For a user's branch it forgets the record instead, so that the next
secrets command takes the branch afresh after checking the user's key.

```
fleet repo sync [NAME]... [flags]

fleet repo sync
fleet repo sync inventory secrets
fleet repo sync inventory --accept-rewrite host/nas
```

## Flags

| Flag | |
|---|---|
| `--name` | The name of a repository to sync (see fleet repo list); may be given more than once. Every named repository by default; may be given more than once |
| `--accept-rewrite` | A branch that no longer follows the commit you trusted, which you know was rewritten on purpose; needs exactly one repository name and a terminal |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Fetch each repository and review what is new.** Each clone is fetched and compared with the commits you trust. A repository with a refusal gets nothing written; for the others you are asked before the commits you trust, your copy of its keys if they changed, and the clone move on.

It needs `git` on the PATH; `fleet doctor` says where to get them.
