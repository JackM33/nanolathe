package client

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
)

func survivalMarkerClient(width, height int) *Client {
	return &Client{width: width, height: height, indexed: bytes.Repeat([]byte{29}, width*height)}
}

func TestMinimapEntryMarkerPointsInward(t *testing.T) {
	layout := camera.LayoutMinimap(1024, 1024)
	dst := hud.Rect{X2: 125, Y2: 125}
	// Unit compass vectors point OUT from the base. The marker's further
	// endpoint must be in the opposite direction, with fill inside its outline.
	for i, from := range [][2]int32{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}} {
		c := survivalMarkerClient(126, 126)
		c.DrawMinimapEntryMarker(dst, layout, 63, 63, uint16(i*8192), 14, 0)
		c.replayForTest()
		inward, outward, fill, outline := int32(0), int32(0), 0, 0
		for y := int32(0); y < 126; y++ {
			for x := int32(0); x < 126; x++ {
				p := c.indexed[y*126+x]
				if p == 29 {
					continue
				}
				dot := (x-63)*from[0] + (y-63)*from[1]
				inward, outward = max(inward, -dot), max(outward, dot)
				if p == 14 {
					fill++
				} else if p == 0 {
					outline++
				}
			}
		}
		if inward <= outward || fill == 0 || outline == 0 {
			t.Fatalf("compass %d points outward or lacks contrast: inward=%d outward=%d fill=%d outline=%d", i, inward, outward, fill, outline)
		}
	}
}

func TestMinimapEntryMarkerLetterboxAndScale(t *testing.T) {
	for _, aspect := range [][2]int32{{1024, 1024}, {1024, 384}, {384, 1024}} {
		t.Run(fmt.Sprint(aspect), func(t *testing.T) {
			layout := camera.LayoutMinimap(aspect[0], aspect[1])
			// Edge and corner anchors also exercise the whole-triangle inset.
			entries := [][3]int32{
				{layout.Right(), layout.PadY + layout.H/2, 0},
				{layout.Right(), layout.Bottom(), 8192},
				{layout.PadX + layout.W/2, layout.Bottom(), 16384},
				{layout.PadX, layout.Bottom(), 24576},
				{layout.PadX, layout.PadY + layout.H/2, 32768},
				{layout.PadX, layout.PadY, 40960},
				{layout.PadX + layout.W/2, layout.PadY, 49152},
				{layout.Right(), layout.PadY, 57344},
			}
			var canonical []byte
			for scale := int32(1); scale <= 3; scale++ {
				const width, height = 150, 154
				c := survivalMarkerClient(width*int(scale), height*int(scale))
				dst := hud.Rect{X1: 7 * scale, Y1: 11 * scale, X2: 133*scale - 1, Y2: 137*scale - 1}
				for _, entry := range entries {
					c.DrawMinimapEntryMarker(dst, layout, entry[0], entry[1], uint16(entry[2]), 14, 0)
				}
				c.replayForTest()
				if scale == 1 {
					canonical = append([]byte(nil), c.indexed...)
				}
				for y := 0; y < c.height; y++ {
					for x := 0; x < c.width; x++ {
						pixel := c.indexed[y*c.width+x]
						if pixel != canonical[(y/int(scale))*width+x/int(scale)] {
							t.Fatalf("scale %d changed canonical pixel at %d,%d", scale, x, y)
						}
						if pixel != 29 && (!dst.Contains(int32(x), int32(y)) || !layout.HitTest(int32(x)/scale-7, int32(y)/scale-11)) {
							t.Fatalf("marker crossed fitted picture at %d,%d", x, y)
						}
					}
				}
			}
		})
	}
}

func TestMinimapEntryMarkerClipsAndHandlesMissingPicture(t *testing.T) {
	for _, layout := range []camera.Minimap{camera.LayoutMinimap(1000, 1000), camera.LayoutMinimap(1, 1000), {}} {
		for _, dst := range []hud.Rect{{X1: -27, Y1: -35, X2: 97, Y2: 113}, {X1: 15, Y1: 7, X2: 82, Y2: 92}, {X1: 150, Y1: 150, X2: 275, Y2: 275}} {
			c := survivalMarkerClient(96, 96)
			c.DrawMinimapEntryMarker(dst, layout, layout.Right(), layout.Bottom(), 8192, 14, 0)
			c.replayForTest()
			for y := int32(0); y < 96; y++ {
				for x := int32(0); x < 96; x++ {
					if c.indexed[y*96+x] == 29 {
						continue
					}
					cx, cy, _ := layout.DisplayToCanvas(x, y, dst.X1, dst.Y1, dst.X2-dst.X1+1, dst.Y2-dst.Y1+1)
					if !dst.Contains(x, y) || !layout.HitTest(cx, cy) {
						t.Fatalf("clipped marker escaped fitted radar at %d,%d", x, y)
					}
				}
			}
		}
	}
	var absent *Client
	absent.DrawMinimapEntryMarker(hud.Rect{}, camera.Minimap{}, 0, 0, 0, 14, 0)
}
