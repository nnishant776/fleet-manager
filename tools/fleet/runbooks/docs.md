<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet docs

Write the runbook pages, tools/fleet/runbooks, from the flows themselves, and the parts of the guide that follow from its order and headings.

Every command's page says what it is for, lists its flags, and walks through
its steps with the commands they run, all taken from the command's definition.

The guide, in guide/, is written by hand in the order its SUMMARY.md gives.
This numbers its chapters, puts links to the pages before and after and to
each chapter's sections at the top and bottom of every page, writes the
contents into the preface, README.md, and writes the index, index.md, from
the glossary, the configuration reference and the commands.

Run it after changing a flow or the guide, and commit the pages with the
change; the tests fail while the runbooks are out of date, and --check fails
while any page is, or a link leads nowhere.

```
fleet docs [flags]

fleet docs
fleet docs --check
fleet docs --out /tmp/runbooks --guide /tmp/guide
```

## Flags

| Flag | |
|---|---|
| `--out` | Where the pages go; tools/fleet/runbooks in the checkout this is run in by default |
| `--guide` | The guide, with its SUMMARY.md; guide in the checkout this is run in by default |
| `--check` | Write nothing; fail if a page would change, a page and SUMMARY.md disagree, or a link leads nowhere |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Write the pages, or check them.** The runbooks, one page per command and an index, README.md, and the guide's generated parts: each chapter's number, the links at its top and bottom, the contents and the index. Pages that would not change are left alone, and pages of commands that no longer exist are removed. With --check nothing is written: it fails if a page would change, a page and SUMMARY.md disagree, or a link points at a file or a heading that does not exist.

