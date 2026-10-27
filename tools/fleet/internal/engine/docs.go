package engine

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/ui"
)

// The runbooks: every flow as a Markdown page, written from its definition,
// so that they say what the commands do and cannot drift from them.

// placeholder is a {{.name}} or {{index . "name"}} in a flow's text.
var placeholder = regexp.MustCompile(`\{\{\s*(?:index \. "([^"]+)"|\.([A-Za-z0-9_-]+))\s*\}\}`)

// docText writes a flow's placeholders as the names they stand for, in
// capitals, as in a usage line: {{.host}} becomes HOST.
func docText(s string) string {
	return placeholder.ReplaceAllStringFunc(s, func(m string) string {
		p := placeholder.FindStringSubmatch(m)
		return strings.ToUpper(p[1] + p[2])
	})
}

// RunbookFile is the page a flow is written to, such as host-check.md.
func RunbookFile(f *Flow) string { return strings.Join(f.Path, "-") + ".md" }

// Runbooks renders a page for every flow and an index, README.md, keyed by
// file name.
func Runbooks(flows []*Flow) map[string]string {
	pages := map[string]string{}
	var index strings.Builder
	index.WriteString(generated)
	index.WriteString("# fleet runbooks\n\n")
	index.WriteString("One page per command: what it is for, its flags, and its steps with the\n" +
		"commands they run. `fleet` shows the same as it goes, and with `--dry-run` it\n" +
		"shows every step and command for a real host and changes nothing. `fleet` with\n" +
		"no arguments offers these as a menu.\n\n")
	index.WriteString("| Command | What it does |\n|---|---|\n")
	for _, f := range flows {
		pages[RunbookFile(f)] = Runbook(f)
		fmt.Fprintf(&index, "| [`fleet %s`](%s) | %s |\n", strings.Join(f.Path, " "), RunbookFile(f), cell(f.Short))
	}
	pages["README.md"] = index.String()
	return pages
}

const generated = "<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->\n\n"

// Runbook renders one flow's page.
func Runbook(f *Flow) string {
	var b strings.Builder
	b.WriteString(generated)
	fmt.Fprintf(&b, "# fleet %s\n\n", strings.Join(f.Path, " "))
	fmt.Fprintf(&b, "%s.\n\n", prose(f.Short))
	if f.Long != "" {
		b.WriteString(prose(f.Long) + "\n\n")
	}

	usage := "fleet " + strings.Join(f.Path, " ")
	for _, a := range f.Args {
		usage += " [" + strings.ToUpper(a) + "]"
	}
	if f.restArg() != "" {
		usage += "..."
	}
	b.WriteString("```\n" + usage + " [flags]\n")
	if f.Example != "" {
		b.WriteString("\n")
		for _, line := range strings.Split(f.Example, "\n") {
			b.WriteString(strings.TrimPrefix(line, "  ") + "\n")
		}
	}
	b.WriteString("```\n\n")

	if len(f.Inputs) > 0 {
		b.WriteString("## Flags\n\n| Flag | |\n|---|---|\n")
		for _, in := range f.Inputs {
			flag := "`--" + in.Name + "`"
			switch {
			case in.Secret:
				flag = "`--" + in.Name + "-stdin`"
			case in.Short != "":
				flag = "`-" + in.Short + "`, " + flag
			}
			help := flagHelp(in)
			if in.Secret {
				help = "read the " + in.Name + " from standard input, instead of being asked. " + help
			}
			if in.Many {
				help += "; may be given more than once"
			}
			fmt.Fprintf(&b, "| %s | %s |\n", flag, cell(help))
		}
		b.WriteString("\n")
	}
	b.WriteString("Every command also takes `--dry-run` (show every step and command; look, but\n" +
		"change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.\n")
	if len(f.Inputs) > 0 {
		b.WriteString("A value that is neither given nor has a default is asked for on a terminal.\n")
	}
	b.WriteString("\n")

	b.WriteString("## Steps\n\n")
	if f.Report {
		b.WriteString("Every step runs, whatever an earlier one found; the command fails at the end\n" +
			"if any found a problem.\n\n")
	}
	for i, s := range f.Steps {
		fmt.Fprintf(&b, "%d. **%s.**", i+1, prose(docText(s.Title)))
		if s.Why != "" {
			b.WriteString(" " + prose(docText(s.Why)))
		}
		b.WriteString("\n")
		if cmds := commands(s.Do); len(cmds) > 0 {
			b.WriteString("\n   ```\n")
			for _, c := range cmds {
				b.WriteString("   $ " + c + "\n")
			}
			b.WriteString("   ```\n")
		}
		for _, text := range shows(s.Do) {
			b.WriteString("\n")
			for _, line := range strings.Split(prose(docText(string(text))), "\n") {
				b.WriteString("   > " + strings.TrimSpace(line) + "\n")
			}
		}
		b.WriteString("\n")
	}
	if len(f.Needs) > 0 {
		b.WriteString("It needs `" + strings.Join(f.Needs, "`, `") + "` on the PATH; `fleet doctor` says where to get them.\n")
	}
	return b.String()
}

// guideOf is what the menu shows of a flow beside the list, from the same
// text as its runbook: the usage line, the examples, what it is for, the steps
// with their reasons (not their commands: --dry-run shows those), the flags.
// It is plain text, so no prose escaping.
func guideOf(f *Flow) ui.Guide {
	usage := "fleet " + strings.Join(f.Path, " ")
	for _, a := range f.Args {
		usage += " [" + strings.ToUpper(a) + "]"
	}
	if f.restArg() != "" {
		usage += "..."
	}
	g := ui.Guide{Usage: usage, Needs: f.Needs}
	if f.Example != "" {
		g.Examples = strings.Split(f.Example, "\n")
	}
	g.About = append(g.About, f.Short+".")
	if f.Long != "" {
		g.About = append(g.About, strings.Split(f.Long, "\n\n")...)
	}
	for _, s := range f.Steps {
		g.Steps = append(g.Steps, ui.GuideItem{Name: docText(s.Title), Text: docText(s.Why)})
	}
	for _, in := range f.Inputs {
		flag := "--" + in.Name
		help := flagHelp(in)
		switch {
		case in.Secret:
			flag = "--" + in.Name + "-stdin"
			help = "read the " + in.Name + " from standard input, instead of being asked. " + help
		case in.Short != "":
			flag = "-" + in.Short + ", " + flag
		}
		if in.Many {
			help += "; may be given more than once"
		}
		g.Flags = append(g.Flags, ui.GuideItem{Name: flag, Text: docText(help)})
	}
	return g
}

// commands is what an action runs, as a person would type it, for the
// actions that are plain commands; a step written in Go shows its commands
// only when it runs.
func commands(a Action) []string {
	switch a := a.(type) {
	case *Command:
		return []string{a.doc()}
	case seq:
		var all []string
		for _, s := range a {
			all = append(all, commands(s)...)
		}
		return all
	case checks:
		var all []string
		for _, p := range a {
			all = append(all, p.Cmd.doc())
		}
		return all
	}
	return nil
}

func (c *Command) doc() string {
	s := Spec{Name: docText(c.name), Dir: docText(c.dir)}
	for _, a := range c.args {
		s.Args = append(s.Args, docText(a))
	}
	for _, kv := range c.env {
		s.Env = append(s.Env, docText(kv))
	}
	return c.shown(nil, s)
}

// cell keeps text inside one table cell.
func cell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(prose(s), "|", `\|`), "\n", " ")
}

// prose escapes what Markdown would take for HTML outside code, such as the
// <name> in hosts/<name>.
func prose(s string) string { return strings.ReplaceAll(s, "<", `\<`) }

// shows is the fixed text an action shows, on its own or within a sequence.
func shows(a Action) []show {
	switch a := a.(type) {
	case show:
		return []show{a}
	case seq:
		var all []show
		for _, s := range a {
			all = append(all, shows(s)...)
		}
		return all
	}
	return nil
}
