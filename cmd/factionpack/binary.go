package main

// Binary renames. Both edits keep every authored byte except the names they
// change: a 3DO primitive's texture name is an absolute offset to a
// NUL-terminated string, so a renamed texture is appended to the file and the
// offset repointed [fmt 3do]; a GAF entry's name is a fixed 32-byte field,
// rewritten in place [fmt gaf].

import (
	"encoding/binary"
	"fmt"
)

const (
	objectRecordBytes    = 52
	primitiveRecordBytes = 32
	gafNameBytes         = 32
)

func u32(data []byte, off int) (uint32, bool) {
	if off < 0 || off+4 > len(data) {
		return 0, false
	}
	return binary.LittleEndian.Uint32(data[off:]), true
}

func cstring(data []byte, off int) (string, bool) {
	if off <= 0 || off >= len(data) {
		return "", false
	}
	end := off
	for end < len(data) && data[end] != 0 {
		end++
	}
	if end >= len(data) {
		return "", false
	}
	return string(data[off:end]), true
}

// modelPrimitive is a primitive's texture-name field: where it is, and the
// name it points at.
type modelPrimitive struct {
	field   int // byte offset of the texture-name offset word
	texture string
}

// modelTextures walks a 3DO's object tree from object zero along child and
// sibling links and returns every primitive that names a texture.
func modelTextures(data []byte) ([]modelPrimitive, error) {
	var out []modelPrimitive
	seen := map[int]bool{}
	var walk func(off int, depth int) error
	walk = func(off int, depth int) error {
		for off >= 0 {
			if seen[off] || depth > 256 {
				return fmt.Errorf("3do object links loop at %d", off)
			}
			seen[off] = true
			if off+objectRecordBytes > len(data) {
				return fmt.Errorf("3do object at %d is truncated", off)
			}
			count, _ := u32(data, off+8)
			prims, _ := u32(data, off+40)
			for i := 0; i < int(count); i++ {
				p := int(prims) + i*primitiveRecordBytes
				if p+primitiveRecordBytes > len(data) {
					return fmt.Errorf("3do primitive at %d is truncated", p)
				}
				texOff, _ := u32(data, p+16)
				if texOff == 0 {
					continue
				}
				if name, ok := cstring(data, int(texOff)); ok && name != "" {
					out = append(out, modelPrimitive{field: p + 16, texture: name})
				}
			}
			child, _ := u32(data, off+48)
			if child != 0 {
				if err := walk(int(child), depth+1); err != nil {
					return err
				}
			}
			sibling, _ := u32(data, off+44)
			if sibling == 0 {
				return nil
			}
			off = int(sibling)
		}
		return nil
	}
	if err := walk(0, 0); err != nil {
		return nil, err
	}
	return out, nil
}

// renameModelTextures returns a copy of a 3DO whose primitives naming a
// texture in renames name its replacement instead.
func renameModelTextures(data []byte, renames func(string) string) ([]byte, error) {
	prims, err := modelTextures(data)
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), data...)
	added := map[string]uint32{}
	for _, p := range prims {
		next := renames(p.texture)
		if next == p.texture {
			continue
		}
		off, ok := added[next]
		if !ok {
			off = uint32(len(out))
			out = append(out, next...)
			out = append(out, 0)
			added[next] = off
		}
		binary.LittleEndian.PutUint32(out[p.field:], off)
	}
	return out, nil
}

// gafEntryNames returns each entry's name field offset and current name.
func gafEntryNames(data []byte) ([]int, []string, error) {
	count, ok := u32(data, 4)
	if !ok || count > 1<<16 {
		return nil, nil, fmt.Errorf("gaf header is truncated or names %d entries", count)
	}
	fields := make([]int, 0, count)
	names := make([]string, 0, count)
	for i := 0; i < int(count); i++ {
		ptr, ok := u32(data, 12+4*i)
		if !ok {
			return nil, nil, fmt.Errorf("gaf entry table is truncated")
		}
		field := int(ptr) + 8
		if field+gafNameBytes > len(data) {
			return nil, nil, fmt.Errorf("gaf entry %d is truncated", i)
		}
		raw := data[field : field+gafNameBytes]
		end := 0
		for end < len(raw) && raw[end] != 0 {
			end++
		}
		fields = append(fields, field)
		names = append(names, string(raw[:end]))
	}
	return fields, names, nil
}

// renameGAFEntries returns a copy of a GAF whose entries are renamed by
// renames; a new name must fit the 31 bytes the field holds before its NUL.
func renameGAFEntries(data []byte, renames func(string) string) ([]byte, error) {
	fields, names, err := gafEntryNames(data)
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), data...)
	for i, field := range fields {
		next := renames(names[i])
		if next == names[i] {
			continue
		}
		if len(next) >= gafNameBytes {
			return nil, fmt.Errorf("gaf entry name %q is longer than %d bytes", next, gafNameBytes-1)
		}
		for j := 0; j < gafNameBytes; j++ {
			out[field+j] = 0
		}
		copy(out[field:], next)
	}
	return out, nil
}
