package drawlist

import "github.com/nanolathe-gg/nanolathe/internal/camera"

// cloneStore is the reusable storage behind CloneInto. It belongs to the
// destination list: every CloneInto into that list overwrites the previous
// copy, which therefore must no longer be referenced.
type cloneStore struct {
	geometry []*ModelGeometry
	reveals  []*ModelReveal
	cams     []*camera.Camera
	used     struct{ geometry, reveals, cams int }
	faces    []ModelFace
	vertices []ModelVertex
	children []ModelChild
}

// emptyFaces keeps CloneInto's zero-length face lists non-nil, as Clone's are.
var emptyFaces = []ModelFace{}

// CloneInto is Clone into dst's existing storage: the same deep copy, with the
// same sharing of immutable loaded resources, but once dst has grown to the
// largest list it receives, copying allocates nothing. dst's previous contents
// (and anything borrowed from them) are invalid after the call.
func (l *List) CloneInto(dst *List) {
	if dst.store == nil {
		dst.store = &cloneStore{}
	}
	s := dst.store
	s.used = struct{ geometry, reveals, cams int }{}
	s.faces, s.vertices, s.children = s.faces[:0], s.vertices[:0], s.children[:0]
	dst.classicImageNext = 0
	dst.order = append(dst.order[:0], l.order...)
	dst.terrain = append(dst.terrain[:0], l.terrain...)
	for i, terrain := range l.terrain {
		if terrain.Cam != nil {
			captured := s.cam()
			*captured = *terrain.Cam
			dst.terrain[i].Cam = captured
		}
	}
	dst.sprite = append(dst.sprite[:0], l.sprite...)
	dst.lightSources = append(dst.lightSources[:0], l.lightSources...)
	dst.glyphs = append(dst.glyphs[:0], l.glyphs...)
	dst.fill = append(dst.fill[:0], l.fill...)
	dst.line = append(dst.line[:0], l.line...)
	dst.model = resizeClone(dst.model, len(l.model))
	for i, m := range l.model {
		dst.model[i] = m
		dst.model[i].Classic = m.Classic.Clone()
		dst.model[i].Geometry = s.geometryCopy(m.Geometry)
	}
	dst.cursor = append(dst.cursor[:0], l.cursor...)
	dst.points = resizeClone(dst.points, len(l.points))
	for i, p := range l.points {
		dst.points[i] = Points{Kind: p.Kind, Points: appendOwned(dst.points[i].Points, p.Points)}
	}
	dst.fog = resizeClone(dst.fog, len(l.fog))
	for i, f := range l.fog {
		dst.fog[i] = Fog{Ops: appendOwned(dst.fog[i].Ops, f.Ops), Gray: f.Gray, Black: f.Black}
	}
	dst.surface = resizeClone(dst.surface, len(l.surface))
	for i, sf := range l.surface {
		dst.surface[i] = Surface{Pixels: appendOwned(dst.surface[i].Pixels, sf.Pixels), SrcW: sf.SrcW, SrcH: sf.SrcH,
			Dst: sf.Dst, Clip: sf.Clip, HasClip: sf.HasClip, Identity: sf.Identity, Revision: sf.Revision}
	}
	dst.world = append(dst.world[:0], l.world...)
	dst.markers = resizeClone(dst.markers, len(l.markers))
	for i, mk := range l.markers {
		dst.markers[i] = Markers{Marks: appendOwned(dst.markers[i].Marks, mk.Marks)}
	}
	dst.flash = append(dst.flash[:0], l.flash...)
	dst.halo = append(dst.halo[:0], l.halo...)
	dst.lens = append(dst.lens[:0], l.lens...)
	dst.featureLayers = append(dst.featureLayers[:0], l.featureLayers...)
	dst.surfaceWakes = resizeClone(dst.surfaceWakes, len(l.surfaceWakes))
	for i, wakes := range l.surfaceWakes {
		dst.surfaceWakes[i] = SurfaceWakes{Marks: appendOwned(dst.surfaceWakes[i].Marks, wakes.Marks)}
	}
	dst.scorchMarks = resizeClone(dst.scorchMarks, len(l.scorchMarks))
	for i, batch := range l.scorchMarks {
		dst.scorchMarks[i] = ScorchMarks{Marks: appendOwned(dst.scorchMarks[i].Marks, batch.Marks)}
	}
	dst.trails = resizeClone(dst.trails, len(l.trails))
	for i, tr := range l.trails {
		dst.trails[i] = Trails{Marks: appendOwned(dst.trails[i].Marks, tr.Marks)}
	}
}

// resizeClone keeps elements beyond len, so their nested arrays are reused.
func resizeClone[T any](v []T, n int) []T {
	if cap(v) < n {
		v = append(v[:cap(v)], make([]T, n-cap(v))...)
	}
	return v[:n]
}

// appendOwned copies src into dst's array, keeping Clone's nil result for an
// empty source.
func appendOwned[T any](dst, src []T) []T {
	if len(src) == 0 {
		return nil
	}
	return append(dst[:0], src...)
}

func (s *cloneStore) cam() *camera.Camera {
	if s.used.cams == len(s.cams) {
		s.cams = append(s.cams, new(camera.Camera))
	}
	s.used.cams++
	return s.cams[s.used.cams-1]
}

// geometryCopy mirrors ModelGeometry.Clone. Face and vertex lists are capped
// sub-slices of the store's arenas, so an append by a reader cannot reach a
// neighbouring packet's storage.
func (s *cloneStore) geometryCopy(g *ModelGeometry) *ModelGeometry {
	if g == nil {
		return nil
	}
	if s.used.geometry == len(s.geometry) {
		s.geometry = append(s.geometry, new(ModelGeometry))
	}
	out := s.geometry[s.used.geometry]
	s.used.geometry++
	*out = *g
	out.Shadow = s.geometryCopy(g.Shadow)
	out.Supersample = s.geometryCopy(g.Supersample)
	start := len(s.children)
	s.children = append(s.children, g.Children...)
	children := s.children[start:len(s.children):len(s.children)]
	for i, ch := range g.Children {
		children[i] = ModelChild{Geometry: s.geometryCopy(ch.Geometry), KeyDelta: ch.KeyDelta}
	}
	if len(children) == 0 {
		children = []ModelChild{}
	}
	out.Children = children
	if g.Reveal != nil {
		if s.used.reveals == len(s.reveals) {
			s.reveals = append(s.reveals, new(ModelReveal))
		}
		reveal := s.reveals[s.used.reveals]
		s.used.reveals++
		*reveal = *g.Reveal
		out.Reveal = reveal
	}
	out.Outline = s.faceCopy(g.Outline)
	out.Faces = s.faceCopy(g.Faces)
	out.LiveFaces = s.faceCopy(g.LiveFaces)
	return out
}

func (s *cloneStore) faceCopy(src []ModelFace) []ModelFace {
	if len(src) == 0 {
		return emptyFaces
	}
	start := len(s.faces)
	s.faces = append(s.faces, src...)
	out := s.faces[start:len(s.faces):len(s.faces)]
	for i := range out {
		if len(src[i].Vertices) == 0 {
			out[i].Vertices = nil
			continue
		}
		first := len(s.vertices)
		s.vertices = append(s.vertices, src[i].Vertices...)
		out[i].Vertices = s.vertices[first:len(s.vertices):len(s.vertices)]
	}
	return out
}
