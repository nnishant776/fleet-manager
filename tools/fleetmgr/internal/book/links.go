package book

import (
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Files is every page whose links are checked, by path: the book's pages as
// they will be, the guide's other Markdown files as they are, and the README
// above the guide. SUMMARY.md is left out: its entries are checked as entries.
func (b *Book) Files() (map[string]string, error) {
	files := map[string]string{}
	entries, err := os.ReadDir(b.Dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == Summary {
			continue
		}
		text, err := os.ReadFile(filepath.Join(b.Dir, e.Name()))
		if err != nil {
			return nil, err
		}
		files[filepath.Join(b.Dir, e.Name())] = string(text)
	}
	for name, text := range b.Pages {
		files[filepath.Join(b.Dir, name)] = text
	}
	readme := filepath.Join(filepath.Dir(b.Dir), "README.md")
	if text, err := os.ReadFile(readme); err == nil {
		files[readme] = string(text)
	}
	return files, nil
}

// Links checks the relative links of the files given, by path: each must
// point at a file or a directory, given or on disk, and an anchor in a
// Markdown file at one of its headings. Links in fenced code, in code spans
// and in the blocks the book writes itself are left out, as are web and mail
// addresses.
func Links(files map[string]string) []Problem {
	var problems []Problem
	anchors := map[string]map[string]bool{}
	anchorsOf := func(path string) map[string]bool {
		if a, ok := anchors[path]; ok {
			return a
		}
		text, ok := files[path]
		if !ok {
			b, _ := os.ReadFile(path)
			text = string(b)
		}
		a := map[string]bool{}
		for _, h := range headings(lines(text)) {
			a[h.anchor] = true
		}
		anchors[path] = a
		return a
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, path := range paths {
		ls := lines(files[path])
		code := codeLines(ls)
		end := ""
		for i, l := range ls {
			t := strings.TrimSpace(l)
			switch {
			case code[i]:
				continue
			case end != "":
				if t == end {
					end = ""
				}
				continue
			case strings.HasPrefix(t, "<!-- book:"):
				if name, _, ok := strings.Cut(strings.TrimPrefix(t, "<!-- book:"), " "); ok {
					end = "<!-- /book:" + name + " -->"
				}
				continue
			}
			for _, m := range link.FindAllStringSubmatchIndex(maskCode(l), -1) {
				target := strings.Trim(l[m[4]:m[5]], "<>")
				if target == "" || strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") {
					continue
				}
				file, anchor, _ := strings.Cut(target, "#")
				if f, err := url.PathUnescape(file); err == nil {
					file = f
				}
				to := path
				if file != "" {
					to = filepath.Join(filepath.Dir(path), filepath.FromSlash(file))
				}
				if _, given := files[to]; !given {
					if _, err := os.Stat(to); err != nil {
						problems = append(problems, Problem{path, i + 1, "links to " + target + ", which does not exist"})
						continue
					}
				}
				if anchor != "" && strings.HasSuffix(to, ".md") && !anchorsOf(to)[anchor] {
					problems = append(problems, Problem{path, i + 1, "links to " + target + ", which is not a heading there"})
				}
			}
		}
	}
	return problems
}
