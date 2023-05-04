package message

import (
	"github.com/aler9/mediamtx/internal/rtmp/bytecounter"
	"github.com/aler9/mediamtx/internal/rtmp/rawmessage"
)

// Writer is a message writer.
type Writer struct {
	w *rawmessage.Writer
}

// NewWriter allocates a Writer.
func NewWriter(w *bytecounter.Writer, checkAcknowledge bool) *Writer {
	return &Writer{
		w: rawmessage.NewWriter(w, checkAcknowledge),
	}
}

// SetAcknowledgeValue sets the value of the last received acknowledge.
func (w *Writer) SetAcknowledgeValue(v uint32) {
	w.w.SetAcknowledgeValue(v)
}

// Write writes a message.
func (w *Writer) Write(msg Message) error {
	raw, err := msg.Marshal()
	if err != nil {
		return err
	}

	err = w.w.Write(raw)
	if err != nil {
		return err
	}

	switch tmsg := msg.(type) {
	case *SetChunkSize:
		w.w.SetChunkSize(tmsg.Value)

	case *SetWindowAckSize:
		w.w.SetWindowAckSize(tmsg.Value)
	}

	return nil
}
