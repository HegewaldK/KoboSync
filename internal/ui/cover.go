package ui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"strings"
)

// coverMode selects how covers are drawn. Both use the Kitty graphics
// protocol; pixel art is a hidden extra behind the c key.
type coverMode int

const (
	coverKitty coverMode = iota // the cover image as it is
	coverPixel                  // coarse square pixels in full colour
	coverModes
)

var coverModeKeys = [coverModes]string{"kitty", "pixel"}

// pixelGrid is the pixel art resolution: pixels per cell horizontally (and
// twice that vertically, so they are square).
const pixelGrid = 2

// renderCover draws img into at most maxW cells by maxRows lines.
func renderCover(mode coverMode, img image.Image, maxW, maxRows int, id uint32) string {
	cols, rows := cellSize(img, maxW, maxRows)
	if cols == 0 {
		return ""
	}
	if mode == coverPixel {
		return kittyImage(img, cols, rows, id, pixelGrid)
	}
	return kittyImage(img, cols, rows, id, 0)
}

// cellSize fits the image into the cell box, assuming cells twice as tall as wide.
func cellSize(img image.Image, maxW, maxRows int) (cols, rows int) {
	b := img.Bounds()
	iw, ih := b.Dx(), b.Dy()
	if iw == 0 || ih == 0 || maxW < 1 || maxRows < 1 {
		return 0, 0
	}
	scale := min(float64(maxW)/float64(iw), float64(maxRows*2)/float64(ih))
	return max(1, int(float64(iw)*scale)), max(1, int(float64(ih)*scale)/2)
}

// sample scales img to w×h pixels by averaging.
func sample(img image.Image, w, h int) [][3]uint32 {
	b := img.Bounds()
	iw, ih := b.Dx(), b.Dy()
	px := make([][3]uint32, w*h)
	for y := 0; y < h; y++ {
		y0, y1 := b.Min.Y+y*ih/h, b.Min.Y+(y+1)*ih/h
		for x := 0; x < w; x++ {
			x0, x1 := b.Min.X+x*iw/w, b.Min.X+(x+1)*iw/w
			px[y*w+x] = average(img, x0, y0, max(x1, x0+1), max(y1, y0+1))
		}
	}
	return px
}

// diacritics encode row and column numbers of Kitty Unicode placeholders
// (from kitty's gen/rowcolumn-diacritics.txt).
var diacritics = []rune{
	0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F, 0x0346, 0x034A, 0x034B, 0x034C, 0x0350, 0x0351, 0x0352, 0x0357,
	0x035B, 0x0363, 0x0364, 0x0365, 0x0366, 0x0367, 0x0368, 0x0369, 0x036A, 0x036B, 0x036C, 0x036D, 0x036E, 0x036F, 0x0483, 0x0484,
	0x0485, 0x0486, 0x0487, 0x0592, 0x0593, 0x0594, 0x0595, 0x0597, 0x0598, 0x0599, 0x059C, 0x059D, 0x059E, 0x059F, 0x05A0, 0x05A1,
	0x05A8, 0x05A9, 0x05AB, 0x05AC, 0x05AF, 0x05C4, 0x0610, 0x0611, 0x0612, 0x0613, 0x0614, 0x0615, 0x0616, 0x0617, 0x0657, 0x0658,
}

// kittyImage transmits the image with the Kitty graphics protocol as a
// virtual placement (U=1) and draws it with Unicode placeholder cells. The
// placeholders are ordinary text to Bubble Tea, so redraws keep the image in
// place. The transmission rides at the start of the first line; the renderer
// only rewrites a line when it changes, so it is not sent on every frame.
//
// With grid > 0 the image becomes pixel art: it is reduced to grid×2grid
// square pixels per cell and scaled back up without smoothing.
func kittyImage(img image.Image, cols, rows int, id uint32, grid int) string {
	cols, rows = min(cols, len(diacritics)), min(rows, len(diacritics))
	// About 10×20 pixels per cell is plenty for a cover.
	w, h := cols*10, rows*20
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	if grid > 0 {
		gw, gh := cols*grid, rows*grid*2
		px := sample(img, gw, gh)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				c := px[(y*gh/h)*gw+x*gw/w]
				i := (y*w + x) * 4
				out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = uint8(c[0]), uint8(c[1]), uint8(c[2]), 255
			}
		}
	} else {
		for i, c := range sample(img, w, h) {
			out.Pix[i*4], out.Pix[i*4+1], out.Pix[i*4+2], out.Pix[i*4+3] = uint8(c[0]), uint8(c[1]), uint8(c[2]), 255
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, out)
	data := base64.StdEncoding.EncodeToString(buf.Bytes())

	var sb strings.Builder
	for i := 0; i < len(data); i += 4096 {
		chunk := data[i:min(i+4096, len(data))]
		more := 0
		if i+4096 < len(data) {
			more = 1
		}
		if i == 0 {
			fmt.Fprintf(&sb, "\x1b_Ga=T,U=1,f=100,q=2,i=%d,c=%d,r=%d,m=%d;%s\x1b\\", id, cols, rows, more, chunk)
		} else {
			fmt.Fprintf(&sb, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}
	// The foreground colour carries the image id.
	color := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", id>>16&255, id>>8&255, id&255)
	for y := 0; y < rows; y++ {
		if y > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(color)
		for x := 0; x < cols; x++ {
			sb.WriteRune(0x10EEEE)
			sb.WriteRune(diacritics[y])
			sb.WriteRune(diacritics[x])
		}
		sb.WriteString("\x1b[39m")
	}
	return sb.String()
}

// average returns the mean colour of a rectangle, sampling at most ~6x6 pixels.
func average(img image.Image, x0, y0, x1, y1 int) [3]uint32 {
	sx, sy := max(1, (x1-x0)/6), max(1, (y1-y0)/6)
	var r, g, bl, n uint32
	for y := y0; y < y1; y += sy {
		for x := x0; x < x1; x += sx {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			r, g, bl, n = r+cr>>8, g+cg>>8, bl+cb>>8, n+1
		}
	}
	return [3]uint32{r / n, g / n, bl / n}
}
