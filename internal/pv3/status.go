package pv3

import (
	"encoding/hex"
	"errors"
	"fmt"
)

type Status struct {
	UUID             string
	UUIDBytes        [16]byte
	Protocol         uint32
	Kind             uint32
	Fields           map[uint32]uint32
	Battery          uint32
	Temperature      int32
	Humidity         uint32
	Width            uint32
	Height           uint32
	Firmware         string
	HeartbeatMinutes uint32
	DisplayState     uint32
}

func ParseStatus(rec Record) (Status, error) {
	if rec.Type != PacketApplication || len(rec.Payload) < 32 {
		return Status{}, errors.New("not an application status record")
	}
	p := rec.Payload
	var s Status
	copy(s.UUIDBytes[:], p[4:20])
	s.UUID = formatUUID(s.UUIDBytes)
	s.Protocol, s.Kind = le.Uint32(p[20:24]), le.Uint32(p[24:28])
	if s.Protocol != MessageStatus {
		return Status{}, fmt.Errorf("unsupported protocol version %d", s.Protocol)
	}
	n := int(le.Uint32(p[28:32]))
	if n < 0 || n%8 != 0 || 32+n+4 > len(p) {
		return Status{}, errors.New("invalid status table length")
	}
	s.Fields = make(map[uint32]uint32, n/8)
	for off := 32; off < 32+n; off += 8 {
		value, key := le.Uint32(p[off:off+4]), le.Uint32(p[off+4:off+8])
		s.Fields[key] = value
	}
	s.Battery = s.Fields[11]
	s.Temperature = int32(s.Fields[13])
	s.Humidity = s.Fields[15]
	s.DisplayState = s.Fields[10]
	s.Width, s.Height = s.Fields[39], s.Fields[40]
	s.HeartbeatMinutes = s.Fields[29]
	s.Firmware = fmt.Sprintf("%d.%d.%d", s.Fields[17], s.Fields[18], s.Fields[19])
	return s, nil
}

func formatUUID(b [16]byte) string {
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func StatusResponse(s Status, compressed bool) ([]byte, error) {
	logical := make([]byte, 44)
	copy(logical[4:20], s.UUIDBytes[:])
	le.PutUint32(logical[20:24], 1)
	le.PutUint32(logical[24:28], s.Kind)
	le.PutUint32(logical[28:32], 8)
	le.PutUint32(logical[36:40], 1)
	payload := logical
	var err error
	if compressed {
		payload, err = CompressChunks(logical)
		if err != nil {
			return nil, err
		}
	}
	return MarshalRecord(Record{Type: PacketApplication, Word2: 1, Payload: payload})
}
