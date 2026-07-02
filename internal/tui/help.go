package tui

import "fmt"

// HelpSection is a titled group of key bindings for a help overlay.
type HelpSection struct {
	Title string
	Keys  [][2]string // each entry is {key, description}
}

// RenderHelp renders help sections as aligned "key  description" rows beneath dim
// section titles — the body of a full-screen help overlay. Keys are padded to a
// common width across all sections so the descriptions line up.
func RenderHelp(sections []HelpSection) []string {
	kw := 0
	for _, s := range sections {
		for _, k := range s.Keys {
			if len(k[0]) > kw {
				kw = len(k[0])
			}
		}
	}
	var out []string
	for i, s := range sections {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, Cyan(s.Title))
		for _, k := range s.Keys {
			out = append(out, fmt.Sprintf("  %s  %s", Pad(k[0], kw), Dim(k[1])))
		}
	}
	return out
}
