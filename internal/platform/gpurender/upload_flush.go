package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Uploads come first (DESIGN_GPU_RENDERER §11.5).
//
// Ebitengine holds an image's WritePixels until the first draw that reads or
// writes the image and queues the upload there, and its OpenGL backend
// finishes every earlier draw on the device before an upload that follows a
// draw. The minimap's rewrite, queued where the HUD drew it, after the model
// lane's page passes, and the parameter image therefore made the render thread
// wait for most of the frame's GPU work, and the GPU then idled while the next
// frame was built. Writing both before anything draws, and reading them in one
// one-pixel draw, queues their uploads together ahead of the frame's first real
// draw; the one wait left is for the previous frame, which the GPU ran while
// this one was being built. Each source is written once, with the same bytes,
// before the draws that read it, so no pixel changes.

// uploadFlushShaderSource writes nothing a frame reads; its draw exists only
// to name its sources.
const uploadFlushShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	return vec4(0)
}
`

// uploadSurfacesFirst writes the frame's identified surfaces (the minimap and
// the megamap) before anything draws. Replay finds each at its revision and
// does not upload it again; a surface without an identity keeps its upload
// there.
func (r *Renderer) uploadSurfacesFirst(list *drawlist.List) {
	r.uploaded = r.uploaded[:0]
	list.VisitSurfaces(func(sf drawlist.Surface) {
		if sf.Identity == 0 || len(sf.Pixels) == 0 {
			return
		}
		if e := r.uploadSurface(sf); e.ok {
			r.uploaded = append(r.uploaded, r.scene.pageImage(e))
		}
	})
}

// flushUploads reads every image in r.uploaded in a one-pixel draw, four
// sources a draw, so their pending uploads are queued now, ahead of any draw
// that follows.
func (r *Renderer) flushUploads() {
	imgs := r.uploaded
	r.uploaded = r.uploaded[:0]
	shader, err := compileShader(uploadFlushShaderSource)
	if len(imgs) == 0 || err != nil || shader == nil {
		return
	}
	if r.uploadTarget == nil {
		r.uploadTarget = newRendererImage(1, 1)
	}
	// One triangle over the target's single pixel centre.
	vertices := [3]ebiten.Vertex{{DstX: 0, DstY: 0}, {DstX: 2, DstY: 0}, {DstX: 0, DstY: 2}}
	indices := [3]uint32{0, 1, 2}
	for len(imgs) > 0 {
		var opts ebiten.DrawTrianglesShaderOptions
		n := copy(opts.Images[:], imgs)
		imgs = imgs[n:]
		r.uploadTarget.DrawTrianglesShader32(vertices[:], indices[:], shader, &opts)
	}
}
