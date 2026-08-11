package pv3

import "errors"

const (
	MessageAcknowledgement = 1
	MessageStatus          = 3
	MessageImage           = 5
	MessageTouch           = 6
)

// ApplicationMessageType returns the logical message type shared by observed
// tablet application payloads. The selected decoder must still validate the
// complete record shape.
func ApplicationMessageType(rec Record) (uint32, error) {
	if rec.Type != PacketApplication || rec.Word2 != 0 || len(rec.Payload) < 24 {
		return 0, errors.New("not a tablet application message")
	}
	return le.Uint32(rec.Payload[20:24]), nil
}

type Acknowledgement struct {
	UUID      string
	UUIDBytes [16]byte
	Sequence  uint32
}

// ParseAcknowledgement decodes the observed post-image type-1 acknowledgement.
// The trailing 8,0,1 words are validated but remain unnamed until their exact
// semantics are established.
func ParseAcknowledgement(rec Record) (Acknowledgement, error) {
	if rec.Type != PacketApplication || rec.Word2 != 0 || len(rec.Payload) != 44 {
		return Acknowledgement{}, errors.New("invalid acknowledgement record shape")
	}
	p := rec.Payload
	if le.Uint32(p[0:4]) != 0 || le.Uint32(p[20:24]) != MessageAcknowledgement ||
		le.Uint32(p[28:32]) != 8 || le.Uint32(p[32:36]) != 0 ||
		le.Uint32(p[36:40]) != 1 || le.Uint32(p[40:44]) != 0 {
		return Acknowledgement{}, errors.New("invalid acknowledgement payload")
	}
	var ack Acknowledgement
	copy(ack.UUIDBytes[:], p[4:20])
	ack.UUID = formatUUID(ack.UUIDBytes)
	ack.Sequence = le.Uint32(p[24:28])
	return ack, nil
}
