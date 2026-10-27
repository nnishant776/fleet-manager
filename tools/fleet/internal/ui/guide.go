package ui

// Guide is what the menu shows of a command beside the list: how to run it,
// what it is for, its steps and its flags. The engine builds it from the
// flow, the same source as the runbooks; ui only draws it.
type Guide struct {
	Usage    string      // such as "fleet host check [HOST]"
	Examples []string    // one per line, with their leading spaces
	About    []string    // paragraphs
	Steps    []GuideItem // Name is the step's title, Text why it is done
	Flags    []GuideItem // Name is the flag, Text what it takes
	Needs    []string    // tools the command cannot work without
}

// GuideItem is a name with a text under it.
type GuideItem struct{ Name, Text string }
