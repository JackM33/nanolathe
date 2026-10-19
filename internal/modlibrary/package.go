package modlibrary

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// The checks below are shared by the libraries that install packages which
// carry content but no Nanolathe config: downloaded maps (§5.6) and official
// add-ons (§5.7).

// IdentityOnly reports whether a package's metadata names only who it is:
// schema 1 with no config and no field beyond schema, id, name, version,
// summary and homepage. The error reports a metadata file that cannot be
// read.
func IdentityOnly(root string, meta Metadata) (bool, error) {
	if meta.Schema != 1 || meta.Config != nil {
		return false, nil
	}
	raw, err := os.ReadFile(filepath.Join(root, MetadataFile))
	if err != nil {
		return false, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return false, err
	}
	for key := range fields {
		switch key {
		case "schema", "id", "name", "version", "summary", "homepage":
		default:
			return false, nil
		}
	}
	return true, nil
}

// RootDocument reports a file at a package's root that is documentation: a
// readme, licence or notes file, which a package may carry beside its
// content.
func RootDocument(name string) bool {
	if strings.Contains(name, "/") {
		return false
	}
	name = strings.ToLower(name)
	return path.Ext(name) == ".txt" || path.Ext(name) == ".md" || path.Ext(name) == ".rtf" || path.Ext(name) == ".html" || name == "readme" || name == "license" || name == "copying"
}

// OSClutter reports file-manager and archiver litter that a host can leave in
// an installed package after Nanolathe wrote it: Finder's .DS_Store and
// AppleDouble "._" companions, Explorer's Thumbs.db and desktop.ini, and
// anything under a __MACOSX folder. It is a host policy for mount-time audits
// only (docs/DESIGN_CONTENT_VFS.md "Downloaded community maps").
func OSClutter(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		if strings.EqualFold(part, "__MACOSX") {
			return true
		}
	}
	base := parts[len(parts)-1]
	return strings.HasPrefix(base, "._") || strings.EqualFold(base, ".DS_Store") ||
		strings.EqualFold(base, "Thumbs.db") || strings.EqualFold(base, "desktop.ini")
}
