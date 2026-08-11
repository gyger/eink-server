package pv3

import (
	"errors"
	"hash/crc32"

	"github.com/pierrec/lz4/v4"
)

const chunkSize = 4800

func CompressChunks(src []byte) ([]byte, error) {
	var out []byte
	var compressor lz4.CompressorHC
	finalIndex := (len(src) - 1) / chunkSize
	for index, off := 0, 0; off < len(src); index, off = index+1, off+chunkSize {
		end := off + chunkSize
		if end > len(src) {
			end = len(src)
		}
		block := src[off:end]
		buf := make([]byte, lz4.CompressBlockBound(len(block)))
		n, err := compressor.CompressBlock(block, buf)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			buf = literalBlock(block)
			n = len(buf)
		}
		header := make([]byte, 24)
		le.PutUint32(header[0:4], uint32(index))
		le.PutUint32(header[4:8], uint32(finalIndex))
		le.PutUint32(header[8:12], uint32(n))
		le.PutUint32(header[12:16], uint32(len(block)))
		out = append(out, header...)
		out = append(out, buf[:n]...)
	}
	return out, nil
}

type ImagePrimitive struct {
	X, Y          int
	Width, Height int
	Pixels        []byte
}

// literalBlock creates a valid raw LZ4 block containing a single final literal
// sequence. It is used when compression would be larger than noisy source data.
func literalBlock(src []byte) []byte {
	if len(src) < 15 {
		out := make([]byte, 1, len(src)+1)
		out[0] = byte(len(src) << 4)
		return append(out, src...)
	}
	out := []byte{0xf0}
	remaining := len(src) - 15
	for remaining >= 255 {
		out = append(out, 255)
		remaining -= 255
	}
	out = append(out, byte(remaining))
	return append(out, src...)
}

func BuildImage(uuid [16]byte, statusKind, frameID uint32, width, height int, pixels []byte) ([]byte, error) {
	return BuildImagePrimitives(uuid, statusKind, frameID, []ImagePrimitive{{Width: width, Height: height, Pixels: pixels}})
}

func BuildImagePrimitives(uuid [16]byte, statusKind, frameID uint32, primitives []ImagePrimitive) ([]byte, error) {
	if len(primitives) == 0 {
		return nil, errors.New("image requires at least one primitive")
	}
	payloadLength := 0
	for _, primitive := range primitives {
		pixels := primitive.Width * primitive.Height
		if primitive.X < 0 || primitive.Y < 0 || primitive.X > 65535 || primitive.Y > 65535 ||
			primitive.Width <= 0 || primitive.Height <= 0 || primitive.Width > 65535 || primitive.Height > 65535 ||
			primitive.X+primitive.Width > 65536 || primitive.Y+primitive.Height > 65536 ||
			pixels%2 != 0 || len(primitive.Pixels) != pixels/2 {
			return nil, errors.New("invalid packed image primitive")
		}
		payloadLength += 24 + len(primitive.Pixels)
	}
	if frameID == 0 {
		hash := crc32.NewIEEE()
		for _, primitive := range primitives {
			_, _ = hash.Write(primitive.Pixels)
		}
		frameID = hash.Sum32()
		if frameID == 0 {
			frameID = 1
		}
	}
	logical := make([]byte, 56+payloadLength)
	copy(logical[4:20], uuid[:])
	le.PutUint32(logical[20:24], 5)
	le.PutUint32(logical[24:28], statusKind)
	le.PutUint32(logical[28:32], uint32(len(logical)-36))
	le.PutUint32(logical[36:40], frameID)
	le.PutUint32(logical[40:44], uint32(len(primitives)))
	le.PutUint32(logical[48:52], uint32(len(logical)-56))
	off := 56
	for _, primitive := range primitives {
		le.PutUint32(logical[off:off+4], 1)
		le.PutUint32(logical[off+4:off+8], uint32(primitive.Y)<<16|uint32(primitive.X))
		le.PutUint16(logical[off+8:off+10], uint16(primitive.Width))
		le.PutUint16(logical[off+10:off+12], uint16(primitive.Height))
		le.PutUint32(logical[off+12:off+16], 0x102)
		le.PutUint32(logical[off+16:off+20], 4)
		le.PutUint32(logical[off+20:off+24], uint32(len(primitive.Pixels)))
		copy(logical[off+24:], primitive.Pixels)
		off += 24 + len(primitive.Pixels)
	}
	payload, err := CompressChunks(logical)
	if err != nil {
		return nil, err
	}
	return MarshalRecord(Record{Type: PacketApplication, Word2: 1, Payload: payload})
}
