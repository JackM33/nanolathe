package main

// Raw TDF text handling. The converter keeps every authored section byte for
// byte apart from the names it renames, so it cuts sections out of the
// original text and rewrites single values instead of re-serialising a
// parsed document.

import (
	"regexp"
	"strings"
)

// rawSection is one top-level section of a TDF file: its authored name and
// the byte span from its opening bracket to its closing brace.
type rawSection struct {
	Name       string
	Start, End int
}

// topSections lists the top-level sections of TDF text, skipping // and /* */
// comments, in file order. Text that does not close a section ends the scan.
func topSections(data []byte) []rawSection {
	var out []rawSection
	n := len(data)
	i := 0
	skip := func() bool { // skips a comment at i; reports whether it did
		if i+1 < n && data[i] == '/' && data[i+1] == '/' {
			for i < n && data[i] != '\n' {
				i++
			}
			return true
		}
		if i+1 < n && data[i] == '/' && data[i+1] == '*' {
			i += 2
			for i+1 < n && !(data[i] == '*' && data[i+1] == '/') {
				i++
			}
			i += 2
			return true
		}
		return false
	}
	for i < n {
		if skip() {
			continue
		}
		if data[i] != '[' {
			i++
			continue
		}
		start := i
		end := strings.IndexByte(string(data[i:]), ']')
		if end < 0 {
			return out
		}
		name := strings.TrimSpace(string(data[i+1 : i+end]))
		i += end + 1
		for i < n && data[i] != '{' {
			if skip() {
				continue
			}
			i++
		}
		if i >= n {
			return out
		}
		depth := 0
		for i < n {
			if skip() {
				continue
			}
			switch data[i] {
			case '{':
				depth++
			case '}':
				depth--
			}
			i++
			if depth == 0 {
				break
			}
		}
		if depth != 0 {
			return out
		}
		out = append(out, rawSection{Name: name, Start: start, End: i})
	}
	return out
}

// sectionText returns a section's text.
func sectionText(data []byte, s rawSection) string { return string(data[s.Start:s.End]) }

// renameSection replaces a section's bracketed name.
func renameSection(text, name string) string {
	end := strings.IndexByte(text, ']')
	if end < 0 || !strings.HasPrefix(text, "[") {
		return text
	}
	return "[" + name + text[end:]
}

// assignment matches one key=value; assignment. The key must follow the
// start of the text, whitespace, a brace or a semicolon, so that a key that
// is the tail of a longer one never matches.
var assignment = regexp.MustCompile(`(?i)(^|[\s{;])([A-Za-z0-9_]+)([ \t]*=[ \t]*)([^;\r\n]*)`)

// rewriteValues replaces the value of every assignment whose key fold is in
// keys, calling keys[key](value) for the new text. A function returning its
// argument leaves the assignment untouched. Comments are not parsed: the
// keys rewritten here never appear inside the comments of the corpus
// converted.
func rewriteValues(text string, keys map[string]func(string) string) string {
	return assignment.ReplaceAllStringFunc(text, func(m string) string {
		sub := assignment.FindStringSubmatch(m)
		fn, ok := keys[strings.ToLower(sub[2])]
		if !ok {
			return m
		}
		value := strings.TrimSpace(sub[4])
		next := fn(value)
		if next == value {
			return m
		}
		return sub[1] + sub[2] + sub[3] + next
	})
}

// replaceSectionList rebuilds a numbered list subsection, such as a builder's
// canbuild%d keys, from names.
func numberedList(prefix string, names []string, indent string) string {
	var b strings.Builder
	for i, name := range names {
		b.WriteString(indent)
		b.WriteString(prefix)
		b.WriteString(itoa(i + 1))
		b.WriteString("=")
		b.WriteString(name)
		b.WriteString(";\r\n")
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
