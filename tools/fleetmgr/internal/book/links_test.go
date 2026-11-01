package book

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLinksMustLeadToAFileOrAHeading(t *testing.T) {
	dir := writeGuide(t, map[string]string{
		"SUMMARY.md": "[Preface](README.md)\n- [A](a.md)\n- [B](b.md)\n[Missing](missing.md)\n[Index](index.md)\n",
		"README.md":  "<!-- book:contents -->\n<!-- /book:contents -->\n",
		"a.md": "# A\n\n[fine](b.md#two) [no file](nowhere.md) [no heading](b.md#three)\n" +
			"`[in code](nowhere.md)`, [web](https://example.com/x), [plain web](http://example.com), [mail](mailto:a@example.com)\n\n" +
			"```\n[in a fence](nowhere.md)\n```\n\n" +
			"[a directory](../src/) [a file](../src/main.nix#L3) [its own heading](#1-a) [not its own](#a)\n" +
			"[the index](index.md) [escaped](b%20c.md) ![a picture](missing.png \"title\")\n",
		"b.md":            "# B\n\n## Two\n",
		"b c.md":          "# Stray\n",
		"../README.md":    "[the guide](guide/README.md) [gone](guide/gone.md#x)\n",
		"../src/main.nix": "{ }\n",
	})
	b, err := Make(dir, nil)
	if err != nil || len(b.Errors) > 0 {
		t.Fatal(err, b.Errors)
	}
	files, err := b.Files()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files[filepath.Join(dir, "SUMMARY.md")]; ok {
		t.Error("SUMMARY.md's links are checked as links")
	}
	top := filepath.Dir(dir) + "/"
	var got []string
	for _, p := range Links(files) {
		got = append(got, strings.TrimPrefix(p.String(), top))
	}
	// The lines are those of the pages as they will be: a.md's third line is
	// its second, under the links at its top.
	want := []string{
		"README.md:1: links to guide/gone.md#x, which does not exist",
		"guide/a.md:7: links to nowhere.md, which does not exist",
		"guide/a.md:7: links to b.md#three, which is not a heading there",
		"guide/a.md:14: links to #a, which is not a heading there",
		"guide/a.md:15: links to missing.png, which does not exist",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("problems\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if _, err := os.Stat(filepath.Join(dir, "index.md")); err == nil {
		t.Error("index.md was written")
	}
}
