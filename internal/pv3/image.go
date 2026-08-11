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
		le.PutUint32(header[4:8], 80)
		le.PutUint32(header[8:12], uint32(n))
		le.PutUint32(header[12:16], uint32(len(block)))
		out = append(out, header...)
		out = append(out, buf[:n]...)
	}
	return out, nil
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
	if width <= 0 || height <= 0 || width > 65535 || height > 65535 || width*height/2 != len(pixels) {
		return nil, errors.New("invalid packed image dimensions")
	}
	if frameID == 0 {
		frameID = crc32.ChecksumIEEE(pixels)
		if frameID == 0 {
			frameID = 1
		}
	}
	logical := make([]byte, 80+len(pixels))
	copy(logical[4:20], uuid[:])
	le.PutUint32(logical[20:24], 5)
	le.PutUint32(logical[24:28], statusKind)
	le.PutUint32(logical[28:32], uint32(len(logical)-36))
	le.PutUint32(logical[36:40], frameID)
	le.PutUint32(logical[40:44], 1)
	le.PutUint32(logical[48:52], uint32(len(logical)-56))
	le.PutUint32(logical[56:60], 1)
	le.PutUint16(logical[64:66], uint16(width))
	le.PutUint16(logical[66:68], uint16(height))
	le.PutUint32(logical[68:72], 0x102)
	le.PutUint32(logical[72:76], 4)
	le.PutUint32(logical[76:80], uint32(len(pixels)))
	copy(logical[80:], pixels)
	payload, err := CompressChunks(logical)
	if err != nil {
		return nil, err
	}
	return MarshalRecord(Record{Type: PacketApplication, Word2: 1, Payload: payload})
}
