package cr2

import (
	"fmt"
	"github.com/lpautet/cr2cv/bufreader"
)

type TiffHeader struct {
	ByteOrder  [2]byte
	TiffMagic  int16
	TiffOffset uint32
}

func (th *TiffHeader) readFrom(reader *bufreader.BufferReader) {
	reader.ReadInto(8, th)
	if (th.ByteOrder[0] != 'I') || (th.ByteOrder[1] != 'I') {
		panic(fmt.Sprintf("Unsuported byte order\n"))
	}
	if th.TiffMagic != 0x002a {
		panic(fmt.Sprintf("Invalid TIFF magic\n"))
	}
}
