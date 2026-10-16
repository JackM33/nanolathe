package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
)

// ResetSources retires one terrain generation after the host has joined the
// recorder and finished submitting the previous frame. A restart loads fresh
// source identities, so keeping their maps for the process lifetime retains
// every earlier battle. Shaders, palette tables, output surfaces, frame scratch
// (the model lane's pages, the reflection planes), recycled fixed-size source
// pages and options survive (DESIGN_GPU_RENDERER §2.3 "Source lifetime").
func (r *Renderer) ResetSources() {
	r.resetSources(func(img *ebiten.Image) { img.Deallocate() })
}

// release is injectable so the ownership walk can be verified without opening
// a graphics device. Several source caches can share the same image.
func (r *Renderer) resetSources(release func(*ebiten.Image)) {
	if r == nil {
		return
	}
	r.joinModelPages()
	if r.preview != nil {
		r.preview.resetSources(release)
	}
	seen := make(map[*ebiten.Image]struct{})
	retire := func(img *ebiten.Image) {
		if img == nil {
			return
		}
		if _, ok := seen[img]; ok {
			return
		}
		seen[img] = struct{}{}
		release(img)
	}
	// Fixed-size pages go back to the pool for the next generation's packers.
	recycle := func(img *ebiten.Image, w, h int) {
		if img == nil {
			return
		}
		if _, ok := seen[img]; ok {
			return
		}
		seen[img] = struct{}{}
		if !r.pool().keep(img, w, h) {
			release(img)
		}
	}
	// These maps are resource owners only. Retirement order cannot change any
	// submitted pixels or simulation state; all previous work is enqueued [I6].
	for _, a := range r.tileAtlases {
		if a == nil {
			continue
		}
		for _, img := range a.pages {
			if a.cells != nil {
				recycle(img, a.cols*a.stride, a.rowsPer*a.stride)
			} else {
				retire(img)
			}
		}
	}
	for _, img := range r.gafImages {
		retire(img)
	}
	for _, slot := range r.scene.transient.slots {
		retire(slot.image)
	}
	for _, p := range r.scene.pages {
		if p != nil && p.shared {
			recycle(p.img, p.w, p.h)
		} else if p != nil {
			retire(p.img)
		}
	}
	// The pages go back before the slots are walked: an in-page slot names
	// its page, and retiring it there would release a page the pool should
	// keep. What remains are the standalone textures of oversized frames.
	for _, page := range r.textureAtlas.pages {
		recycle(page, modelTexturePageSize, modelTexturePageSize)
	}
	for _, slot := range r.textureAtlas.slots {
		retire(slot.img)
	}
	// The model lane's 4096² pages are frame scratch, not sources: each frame
	// clears the rows it uses before rasterizing into them. They survive like
	// the output surfaces, so a new battle or settings preview does not
	// allocate (and on unified memory wire) two fresh 64 MiB planes per page
	// inside its first frame.
	pages := r.modelDirect.pages
	retire(r.modelDirect.params.img)
	retire(r.modelDirect.groups.key)
	retire(r.modelDirect.groups.colour)
	retire(r.fog.atlas)
	retire(r.fog.grid)
	// A mask is replaced wholesale by the next map's, so one of the same size
	// is reused from the pool.
	recycle(r.water.mask, r.water.w, r.water.h)
	retire(r.pointPlane.img)
	// The flash disc page and its staging bytes are kept: its frames are keyed
	// by generated table and frame, and every frame packed rewrites its
	// region and border before it is uploaded, so a texel of the old
	// generation is never sampled.
	flash := flashDiscAtlas{img: r.sched.flash.img, buf: r.sched.flash.buf}
	for _, atlas := range r.markerAtlases {
		retire(atlas.image)
	}
	r.markerAtlases = [4]markerAtlasUpload{}

	r.tileAtlases = make(map[tileAtlasKey]*tileAtlas)
	r.gafImages = make(map[*formats.GAFFrame]*ebiten.Image)
	r.scene = sceneAtlas{
		pool:   r.pool(),
		frames: make(map[*formats.GAFFrame]sceneEntry),
		pcx:    make(map[*formats.PCX]sceneEntry),
		fonts:  make(map[*formats.FNT]*fntAtlas),
	}
	r.textureAtlas = modelTextureAtlas{}
	r.modelPrep = modelPrepScratch{}
	r.lighting = battleLighting{modelDisabled: r.lighting.modelDisabled, groundDisabled: r.lighting.groundDisabled, groundStrengthOffset: r.lighting.groundStrengthOffset}
	r.heat = treeHeat{treeDisabled: r.heat.treeDisabled, wreckDisabled: r.heat.wreckDisabled}
	r.water = waterLayer{surfaceDisabled: r.water.surfaceDisabled, motionDisabled: r.water.motionDisabled, foamDisabled: r.water.foamDisabled,
		shader: r.water.shader, stillShader: r.water.stillShader, wakeShader: r.water.wakeShader}
	// The reflection planes are surface-sized scratch, cleared before every
	// use, and survive like the output surfaces.
	r.reflections = waterReflections{disabled: r.reflections.disabled, sourceShader: r.reflections.sourceShader, resolveShader: r.reflections.resolveShader, softResolveShader: r.reflections.softResolveShader,
		source: r.reflections.source, height: r.reflections.height}
	for _, img := range r.modelDirect.sprites.sheets {
		retire(img)
	}
	for _, p := range r.layers.pages {
		retire(p.img)
	}
	r.terrainLevels.release(retire)
	r.terrainLevels = terrainLevelSet{}
	r.layers = featureLayerStore{shader: r.layers.shader, shaderErr: r.layers.shaderErr}
	r.modelDirect = modelDirectLane{shared: r.modelDirect.shared, keyShader: r.modelDirect.keyShader, colourShader: r.modelDirect.colourShader, shaderErr: r.modelDirect.shaderErr, groups: modelGroupMergeLane{shader: r.modelDirect.groups.shader}, sprites: modelSpriteStore{shader: r.modelDirect.sprites.shader}}
	for i, pg := range pages {
		r.modelDirect.pages[i] = modelDirectPage{key: pg.key, colour: pg.colour}
	}
	r.fog = fogPass{shader: r.fog.shader, shaderErr: r.fog.shaderErr, compiled: r.fog.compiled}
	r.arrival = arrivalLayer{shader: r.arrival.shader}
	// Retained compiled runs and options also reference source images. Drop
	// those and frame scratch together; nothing from the old frame is replayable.
	r.sched = scheduler{flash: flash}
	r.sceneOpts = ebiten.DrawTrianglesShaderOptions{}
	r.surfaceDynamic = surfaceUpload{}
	r.surfaceCache = [4]surfaceUpload{}
	r.pointPlane = pointPlane{}
	r.pointRows = nil
	r.pointRuns = nil
	r.pointGroups = nil
	r.pointGroupIdx = nil
	r.lastDest = nil
}
