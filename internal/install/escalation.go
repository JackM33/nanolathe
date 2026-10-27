package install

import (
	"os"
	"regexp"
	"strings"
)

// TA: Escalation's upstream installer copies its archives and renamed
// content folders into a Total Annihilation install. Mounted with the base
// game that way, Escalation replaces shared art while its renamed folders go
// unread, which blanks build buttons; it plays only as Nanolathe's own
// package in the mod slot (docs/ESCALATION_SUPPORT.md, "Unconfigured upstream
// installations"). These predicates name those files, so a base install can
// be mounted as the plain game it was. The Android launcher applies the same
// rule when it copies a folder (docs/ANDROID.md §2).

var escalationArchive = regexp.MustCompile(`^(taesc\.gp3|t[0-9x]esc[0-9]*\.ufo)$`)

// UpstreamEscalationArchive reports whether an install folder's archive file
// is one of TA: Escalation's own: TAESC.gp3 or a T*ESC*.ufo.
func UpstreamEscalationArchive(name string) bool {
	return escalationArchive.MatchString(strings.ToLower(name))
}

// UpstreamEscalationFolder reports whether a top-level folder of an install
// is one of TA: Escalation's renamed content folders.
func UpstreamEscalationFolder(name string) bool {
	switch strings.ToLower(name) {
	case "unitse", "weapone", "gamedate", "unitpice", "guie", "downloadse", "ae":
		return true
	}
	return false
}

// UpstreamEscalationFiles lists the top-level entries of dir that belong to a
// TA: Escalation copied over it, in directory order.
func UpstreamEscalationFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && UpstreamEscalationFolder(e.Name()) || !e.IsDir() && UpstreamEscalationArchive(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}
