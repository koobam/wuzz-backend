package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
)

// What an upload can be refused for. The handler turns each into its own status and message.
var (
	ErrEmpty       = errors.New("media: the file is empty")
	ErrTooLarge    = errors.New("media: the file is over the size limit")
	ErrUnsupported = errors.New("media: not a JPEG, PNG, WebP or AVIF picture")
	ErrCorrupt     = errors.New("media: the picture cannot be read")
	ErrDimensions  = errors.New("media: the picture is too large in pixels")
)

const (
	// MaxSide is the longest edge an upload may have, and MaxPixels its area: a small file can unpack into gigabytes (a decompression
	// bomb), so the size is read from the header before anything is decoded.
	MaxSide   = 8000
	MaxPixels = 25_000_000
	jpegQ     = 88
)

// Image is an upload that passed inspection: the bytes to store (re-encoded without metadata for JPEG and PNG), what they are, and
// their size in pixels as a viewer will draw them.
type Image struct {
	Data        []byte
	ContentType string
	Ext         string
	Width       int
	Height      int
}

// Kind is what the first bytes say a file is, whatever its name or its declared type claims.
func Kind(b []byte) (contentType, ext string) {
	switch {
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg", "jpg"
	case len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png", "png"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp", "webp"
	case len(b) >= 16 && string(b[4:8]) == "ftyp" && hasBrand(b[8:min(len(b), 64)], "avif", "avis"):
		return "image/avif", "avif"
	}
	return "", ""
}

func hasBrand(b []byte, brands ...string) bool {
	for _, br := range brands {
		if bytes.Contains(b, []byte(br)) {
			return true
		}
	}
	return false
}

// Process inspects an upload: it must be one of the four picture types by its bytes, no larger than max, and not absurd in pixels.
// A JPEG is re-encoded with its orientation applied and every other piece of metadata (EXIF, GPS, camera) left out; a PNG is re-encoded,
// which drops its text and profile chunks. WebP and AVIF are stored as they are: the standard library cannot decode them, so their
// metadata stays (their size is still read from the header and checked).
func Process(data []byte, max int64) (*Image, error) {
	if len(data) == 0 {
		return nil, ErrEmpty
	}
	if int64(len(data)) > max {
		return nil, ErrTooLarge
	}
	ct, ext := Kind(data)
	if ct == "" {
		return nil, ErrUnsupported
	}
	w, h, err := dimensions(ct, data)
	if err != nil {
		return nil, ErrCorrupt
	}
	if w <= 0 || h <= 0 {
		return nil, ErrCorrupt
	}
	if w > MaxSide || h > MaxSide || w*h > MaxPixels {
		return nil, ErrDimensions
	}

	switch ct {
	case "image/jpeg":
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, ErrCorrupt
		}
		img = orient(img, jpegOrientation(data))
		var out bytes.Buffer
		if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: jpegQ}); err != nil {
			return nil, ErrCorrupt
		}
		b := img.Bounds()
		return &Image{Data: out.Bytes(), ContentType: ct, Ext: ext, Width: b.Dx(), Height: b.Dy()}, nil
	case "image/png":
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, ErrCorrupt
		}
		var out bytes.Buffer
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if err := enc.Encode(&out, img); err != nil {
			return nil, ErrCorrupt
		}
		b := img.Bounds()
		return &Image{Data: out.Bytes(), ContentType: ct, Ext: ext, Width: b.Dx(), Height: b.Dy()}, nil
	}
	return &Image{Data: data, ContentType: ct, Ext: ext, Width: w, Height: h}, nil
}

// dimensions reads the pixel size from the header, without decoding the picture.
func dimensions(ct string, b []byte) (int, int, error) {
	switch ct {
	case "image/jpeg", "image/png":
		cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
		return cfg.Width, cfg.Height, err
	case "image/webp":
		return webpSize(b)
	case "image/avif":
		return avifSize(b)
	}
	return 0, 0, ErrUnsupported
}

func webpSize(b []byte) (int, int, error) {
	if len(b) < 30 {
		return 0, 0, ErrCorrupt
	}
	switch string(b[12:16]) {
	case "VP8 ": // lossy: frame tag (3 bytes), start code 9d 01 2a, then 14-bit width and height
		if b[23] != 0x9d || b[24] != 0x01 || b[25] != 0x2a {
			return 0, 0, ErrCorrupt
		}
		return int(binary.LittleEndian.Uint16(b[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(b[28:30]) & 0x3fff), nil
	case "VP8L": // lossless: signature 0x2f, then 14-bit width-1 and height-1
		if b[20] != 0x2f {
			return 0, 0, ErrCorrupt
		}
		bits := binary.LittleEndian.Uint32(b[21:25])
		return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1, nil
	case "VP8X": // extended: 24-bit canvas width-1 and height-1
		return int(uint32(b[24])|uint32(b[25])<<8|uint32(b[26])<<16) + 1, int(uint32(b[27])|uint32(b[28])<<8|uint32(b[29])<<16) + 1, nil
	}
	return 0, 0, ErrCorrupt
}

// avifSize finds the first `ispe` (image spatial extents) property: version/flags, then width and height as 32-bit numbers.
func avifSize(b []byte) (int, int, error) {
	i := bytes.Index(b, []byte("ispe"))
	if i < 0 || i+16 > len(b) {
		return 0, 0, ErrCorrupt
	}
	w := binary.BigEndian.Uint32(b[i+8 : i+12])
	h := binary.BigEndian.Uint32(b[i+12 : i+16])
	if w > 1<<20 || h > 1<<20 { // absurd: reject as too large rather than overflow int maths below
		return int(min(w, 1<<20)), int(min(h, 1<<20)), nil
	}
	return int(w), int(h), nil
}

// jpegOrientation reads the EXIF orientation (1-8; 1 = as shot) from the APP1 segment, or 1 when there is none.
func jpegOrientation(b []byte) int {
	i := 2
	for i+4 <= len(b) && b[i] == 0xFF {
		marker := b[i+1]
		if marker == 0xDA || marker == 0xD9 { // start of scan / end: no more headers
			break
		}
		size := int(binary.BigEndian.Uint16(b[i+2 : i+4]))
		if size < 2 || i+2+size > len(b) {
			break
		}
		if marker == 0xE1 && size > 16 && string(b[i+4:i+10]) == "Exif\x00\x00" {
			return exifOrientation(b[i+10 : i+2+size])
		}
		i += 2 + size
	}
	return 1
}

func exifOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	off := int(order.Uint32(t[4:8]))
	if off < 8 || off+2 > len(t) {
		return 1
	}
	n := int(order.Uint16(t[off : off+2]))
	for k := 0; k < n; k++ {
		e := off + 2 + k*12
		if e+12 > len(t) {
			break
		}
		if order.Uint16(t[e:e+2]) == 0x0112 { // Orientation, a SHORT
			if o := int(order.Uint16(t[e+8 : e+10])); o >= 1 && o <= 8 {
				return o
			}
			return 1
		}
	}
	return 1
}

// orient turns a picture as the camera stored it into the way it was meant to be seen (EXIF orientation 1-8).
func orient(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	// Decoded JPEGs are YCbCr: draw into plain NRGBA first so every pixel reads the same way.
	flat := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(flat, flat.Bounds(), src, b.Min, draw.Src)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch o {
			case 2: // mirrored horizontally
				nx, ny = w-1-x, y
			case 3: // rotated 180
				nx, ny = w-1-x, h-1-y
			case 4: // mirrored vertically
				nx, ny = x, h-1-y
			case 5: // transposed
				nx, ny = y, x
			case 6: // rotated 90 clockwise
				nx, ny = h-1-y, x
			case 7: // transversed
				nx, ny = h-1-y, w-1-x
			case 8: // rotated 90 counter-clockwise
				nx, ny = y, w-1-x
			}
			dst.SetNRGBA(nx, ny, flat.NRGBAAt(x, y))
		}
	}
	return dst
}
