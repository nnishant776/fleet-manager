package book

import (
	"fmt"
	"strings"
	"testing"
)

var indexBook = map[string]string{
	"SUMMARY.md": "# Summary\n\n[Preface](README.md)\n\n- [Hosts](hosts.md)\n- [Tools](tools.md)\n- [Accounts](accounts.md)\n\n" +
		"# Appendices\n\n[Configuration reference](configuration.md)\n[Glossary](glossary.md)\n[Index](index.md)\n",
	"README.md": "# Book\n\n<!-- book:contents -->\n<!-- /book:contents -->\n",
	"hosts.md": "# Hosts\n\nEvery Host Branch is fetched, and a fleet host syncs.\n\n" +
		"## Branches\n\nA host\nbranch spans lines. `host.name` sets the name.\n\n" +
		"## Plurals\n\nHost branches count, but not host-branch, host_branch or a host branchy word. The nixos-install command and\n" +
		"os-installer are something else, nor is [it](os-install.md) a link's target, and os-install in prose is not code.\n\n" +
		"## Code\n\n```\nos-install --disk main=/dev/sda\nhost branch in code does not count\n```\n",
	"tools.md": "# Tools\n\n## Installing\n\nRun `fleet install prep` first.\n\n" +
		"## Both\n\n`fleet install` and then fleet install prep, which in prose is not a command.\n\n" +
		"## Checking\n\n`fleet host check` looks at a host; a fleet of hosts is prose.\n\n" +
		"## Help\n\n`fleet <command> --help` shows more.\n\n" +
		"## Spans\n\nA code span may go on to the next line: `fleet\ninstall`.\n\n" +
		"## Fields\n\n`email` here is not an account's field.\n",
	"accounts.md": "# Accounts\n\n## Mail\n\nSet `email`; conf.host.name in prose does not count.\n",
	"configuration.md": "# Configuration reference\n\n" +
		"## `conf.host`\n\n| Option | Type |\n|---|---|\n| `host.name` | str |\n\n" +
		"## `conf.homebrew`\n\n| Option | Type |\n|---|---|\n| `homebrew.brews` / `.casks` | listOf str |\n\n" +
		"## `conf.users`\n\n| Option | Type |\n|---|---|\n| `users.accounts` | attrsOf account |\n\n" +
		"Each takes:\n\n| Field | Type |\n|---|---|\n| `email` | str |\n\n" +
		"## `conf.hardware`\n\n| Field | Type |\n|---|---|\n| `size` | str |\n",
	"glossary.md": "# Glossary\n\n## E\n\n- **`/etc/nixos`**: The checkout. See [Hosts](hosts.md).\n\n" +
		"## F\n\n- **`fleet`**: The tool. See [Tools](tools.md).\n- **fleet host**: A host that syncs. See [Hosts](hosts.md#branches).\n" +
		"- **`fleet install`**: Installs. See [Installing](tools.md#installing).\n\n" +
		"## H\n\n- **host branch**: The branch a host fetches. See\n  [Branches](hosts.md#branches).\n\n" +
		"## O\n\n- **`os-install`**: The installer.\n\n" +
		"## S\n\n- **`~/.ssh`**: Keys, [elsewhere](../README.md#keys).\n",
}

var indexCommands = []Command{
	{Name: "fleet install", Runbook: "../tools/fleet/runbooks/install.md"},
	{Name: "fleet install prep", Runbook: "../tools/fleet/runbooks/install-prep.md"},
	{Name: "fleet host check", Runbook: "../tools/fleet/runbooks/host-check.md"},
}

func TestIndexTakesItsTermsFromTheGlossaryTheOptionsAndTheCommands(t *testing.T) {
	b, err := Make(writeGuide(t, indexBook), indexCommands)
	if err != nil || len(b.Errors) > 0 {
		t.Fatal(err, b.Errors)
	}
	body := "\n\n" + indexIntro + "\n\n" + strings.Join([]string{
		"## Symbols\n",
		"- `~/.ssh`: **[elsewhere](../README.md#keys)**\n",
		"## C\n",
		"- `conf.homebrew.brews`: **[A › `conf.homebrew`](configuration.md#confhomebrew)**",
		"- `conf.homebrew.casks`: **[A › `conf.homebrew`](configuration.md#confhomebrew)**",
		"- `conf.host.name`: **[A › `conf.host`](configuration.md#confhost)**, [1 › Branches](hosts.md#branches)",
		"- `conf.users.accounts`: **[A › `conf.users`](configuration.md#confusers)**",
		"- `conf.users.accounts.<name>.email`: **[A › `conf.users`](configuration.md#confusers)**, [3 › Mail](accounts.md#mail)\n",
		"## E\n",
		"- `/etc/nixos`: **[1 › Hosts](hosts.md)**\n",
		"## F\n",
		"- `fleet`: **[2 › Tools](tools.md)**, [2 › Help](tools.md#help)",
		"- fleet host: **[1 › Branches](hosts.md#branches)**, [1 › Hosts](hosts.md)",
		"- `fleet host check`: **[runbook](../tools/fleet/runbooks/host-check.md)**, [2 › Checking](tools.md#checking)",
		"- `fleet install`: **[2 › Installing](tools.md#installing)**, [2 › Both](tools.md#both), [2 › Spans](tools.md#spans)",
		"- `fleet install prep`: **[runbook](../tools/fleet/runbooks/install-prep.md)**, [2 › Installing](tools.md#installing)\n",
		"## H\n",
		"- host branch: **[1 › Branches](hosts.md#branches)**, [1 › Hosts](hosts.md), [1 › Plurals](hosts.md#plurals)\n",
		"## O\n",
		"- `os-install`: **[B › O](glossary.md#o)**, [1 › Code](hosts.md#code)",
	}, "\n") + "\n\n" + bottomStart
	page := b.Pages["index.md"]
	if !strings.Contains(page, topEnd+body) {
		t.Errorf("index:\n%s\nwant, after the top:%s", page, body)
	}
	if !strings.HasPrefix(page, "# Appendix C. Index\n\n") ||
		!strings.Contains(page, "\n\nIn this chapter: [Symbols](#symbols) · [C](#c) · [E](#e) · [F](#f) · [H](#h) · [O](#o)\n"+topEnd) {
		t.Errorf("index's top:\n%s", page)
	}
	// The contents list neither the glossary's letters nor the index's.
	if c := b.Pages["README.md"]; !strings.Contains(c, "- [Appendix B. Glossary](glossary.md)\n- [Appendix C. Index](index.md)\n"+contentsEnd) {
		t.Errorf("contents:\n%s", c)
	}
}

func TestIndexEntryListsAtMostTwelvePlaces(t *testing.T) {
	var ch strings.Builder
	ch.WriteString("# Widgets\n\nA widget.\n")
	for i := range 15 {
		fmt.Fprintf(&ch, "\n## Part %d\n\nAnother widget.\n", i)
	}
	b, err := Make(writeGuide(t, map[string]string{
		"SUMMARY.md":  "[Preface](README.md)\n- [Widgets](widgets.md)\n[Glossary](glossary.md)\n[Index](index.md)\n",
		"README.md":   "<!-- book:contents -->\n<!-- /book:contents -->\n",
		"widgets.md":  ch.String(),
		"glossary.md": "# Glossary\n\n## W\n\n- **widget**: A thing. See [Part 3](widgets.md#part-3).\n",
	}), nil)
	if err != nil || len(b.Errors) > 0 {
		t.Fatal(err, b.Errors)
	}
	want := "- widget: **[1 › Part 3](widgets.md#part-3)**, [1 › Widgets](widgets.md), [1 › Part 0](widgets.md#part-0), " +
		"[1 › Part 1](widgets.md#part-1), [1 › Part 2](widgets.md#part-2), [1 › Part 4](widgets.md#part-4), " +
		"[1 › Part 5](widgets.md#part-5), [1 › Part 6](widgets.md#part-6), [1 › Part 7](widgets.md#part-7), " +
		"[1 › Part 8](widgets.md#part-8), [1 › Part 9](widgets.md#part-9), [1 › Part 10](widgets.md#part-10)\n"
	if page := b.Pages["index.md"]; !strings.Contains(page, want) {
		t.Errorf("index:\n%s\nwant:\n%s", page, want)
	}
}

func TestIndexWithoutAGlossaryHasTheOtherTerms(t *testing.T) {
	b, err := Make(writeGuide(t, map[string]string{
		"SUMMARY.md": "[Preface](README.md)\n- [One](one.md)\n[Glossary](glossary.md)\n[Index](index.md)\n",
		"README.md":  "<!-- book:contents -->\n<!-- /book:contents -->\n",
		"one.md":     "# One\n\nRun `fleet doctor` first.\n",
	}), []Command{{Name: "fleet doctor", Runbook: "../tools/fleet/runbooks/doctor.md"}})
	if err != nil || len(b.Errors) > 0 {
		t.Fatal(err, b.Errors)
	}
	if page := b.Pages["index.md"]; !strings.Contains(page, "## F\n\n- `fleet doctor`: **[runbook](../tools/fleet/runbooks/doctor.md)**, [1 › One](one.md)\n") {
		t.Errorf("index:\n%s", page)
	}
}

func TestCodeAtNeedsTheNameToEnd(t *testing.T) {
	for _, c := range []struct {
		text string
		at   int
	}{
		{"run os-install now", 4},
		{"`os-install`", 1},
		{"/run/current-system/sw/bin/os-install", 27},
		{"x.os-install", 2},
		{"nixos-install", -1},
		{"os-installer", -1},
		{"os-install.nix", -1},
		{"os-install-2", -1},
		{"os_install os-install", 11},
	} {
		if got := codeAt(c.text, "os-install", 0); got != c.at {
			t.Errorf("codeAt(%q) = %d, want %d", c.text, got, c.at)
		}
	}
}

func TestPhraseMayBePluralOrPossessive(t *testing.T) {
	ix := &indexer{byKey: map[string]*term{}}
	ix.add(&term{find: "host branch", kind: proseTerm})
	ix.add(&term{find: "recovery identity", kind: proseTerm})
	for s, want := range map[string]bool{
		"a host branch.":                true,
		"Host Branches":                 true,
		"the host\n  branches":          true,
		"host branchs":                  true,
		"the host branch's file":        true,
		"the host branch’s file":        true,
		"the recovery identity's key":   true,
		"host-branch":                   false,
		"a host branchy word":           false,
		"host branches_x":               false,
		"subhost branch":                false,
		"the host branchesque thing":    false,
		"the recovery identityless one": false,
	} {
		got := hasPhrase(s, ix.terms[0].re) || hasPhrase(s, ix.terms[1].re)
		if got != want {
			t.Errorf("%q: %v, want %v", s, got, want)
		}
	}
}
