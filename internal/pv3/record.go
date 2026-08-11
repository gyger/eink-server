package pv3

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

const (
	PacketBootloader  = 2
	PacketApplication = 3
	MaxPayload        = 32 << 20
)

var le = binary.LittleEndian

type Record struct {
	Type, Word1, Word2 uint32
	Payload            []byte
}

func ReadRecord(r io.Reader) (Record, error) {
	var header [20]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Record{}, err
	}
	n := le.Uint32(header[12:16])
	if n > MaxPayload {
		return Record{}, errors.New("pv3 payload exceeds limit")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return Record{}, err
	}
	rec := Record{Type: le.Uint32(header[0:4]), Word1: le.Uint32(header[4:8]), Word2: le.Uint32(header[8:12]), Payload: payload}
	check := le.Uint32(header[16:20])
	var want uint32
	if rec.Word2 == 0 {
		want = crc32.ChecksumIEEE(header[:16])
	} else {
		want = crc32.ChecksumIEEE(payload)
	}
	if check != want {
		return Record{}, errors.New("pv3 checksum mismatch")
	}
	return rec, nil
}

func MarshalRecord(rec Record) ([]byte, error) {
	if len(rec.Payload) > MaxPayload {
		return nil, errors.New("pv3 payload exceeds limit")
	}
	out := make([]byte, 20+len(rec.Payload))
	le.PutUint32(out[0:4], rec.Type)
	le.PutUint32(out[4:8], rec.Word1)
	le.PutUint32(out[8:12], rec.Word2)
	le.PutUint32(out[12:16], uint32(len(rec.Payload)))
	copy(out[20:], rec.Payload)
	if rec.Word2 == 0 {
		le.PutUint32(out[16:20], crc32.ChecksumIEEE(out[:16]))
	} else {
		le.PutUint32(out[16:20], crc32.ChecksumIEEE(rec.Payload))
	}
	return out, nil
}
