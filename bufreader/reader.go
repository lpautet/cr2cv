package bufreader

import (
	"encoding/binary"
	"fmt"
	"io"
)

type BufferReader struct {
	Reader    io.Reader
	ByteOrder binary.ByteOrder
	Offset    int64
}

func (fr *BufferReader) ReadInto(limit int64, data interface{}) {
	limitedReader := io.LimitedReader{R: fr.Reader, N: limit}
	err := binary.Read(&limitedReader, fr.ByteOrder, data)
	if err != nil {
		panic(err)
	}
	if limitedReader.N != 0 {
		panic("Incomplete Read")
	}
	fr.Offset += limit
}

func (fr *BufferReader) ReadByte() byte {
	var ret byte
	err := binary.Read(fr.Reader, fr.ByteOrder, &ret)
	if err != nil {
		panic(err)
	}
	fr.Offset += 1
	return ret
}

func (fr *BufferReader) ReadUint16() uint16 {
	var ret uint16
	err := binary.Read(fr.Reader, fr.ByteOrder, &ret)
	if err != nil {
		panic(err)
	}
	fr.Offset += 2
	return ret
}

func (fr *BufferReader) ReadUint32() uint32 {
	var ret uint32
	err := binary.Read(fr.Reader, fr.ByteOrder, &ret)
	if err != nil {
		panic(err)
	}
	fr.Offset += 4
	return ret
}

func (fr *BufferReader) ReadBuffer(size int64) []byte {
	buffer := make([]byte, size)
	fr.ReadInto(size, &buffer)
	return buffer
}

func (fr *BufferReader) MoveTo(name string, targetOffset int64) {
	if fr.Offset == targetOffset {
		return
	}
	if fr.Offset > targetOffset {
		panic(fmt.Sprintf("moveTo: target offset PASSED ALREADY for %s:%d<%d!\n", name, targetOffset, fr.Offset))
	}

	size := targetOffset - fr.Offset
	buffer := fr.ReadBuffer(size)
	for i, b := range buffer {
		if b != 0x0 {
			fmt.Printf("Discarded %d bytes while moving to %s:@%d %v\n", size, name, targetOffset, buffer[i:])
			break
		}
	}
}
