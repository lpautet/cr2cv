package cr2

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/lpautet/cr2cv/bufreader"
	"image"
	"sort"
)

type CR2File struct {
	TiffHeader      TiffHeader
	CR2Header       CR2Header
	ifd0            ImageFileDirectory
	exifSubIfd      ImageFileDirectory
	makerNodeSubIfd ImageFileDirectory
	ifd1            ImageFileDirectory
	ifd2            ImageFileDirectory
	ifd3            ImageFileDirectory
	ValuesToExtract []*IFDEntry
	ValuesByOffset  map[uint32]interface{}
	Image2, Image3  *image.RGBA64
	Image0, Image1  image.Image
}

func (cf *CR2File) Init() {
	cf.ValuesByOffset = make(map[uint32]interface{})
}

func (cf *CR2File) ReadFrom(reader *bufreader.BufferReader) {
	cf.TiffHeader.readFrom(reader)
	cf.CR2Header.readFrom(reader)

	if reader.Offset != int64(cf.TiffHeader.TiffOffset) {
		panic(fmt.Sprintf("Incomplete TIFF tiffHeader read expected offset %d, real %d", cf.TiffHeader.TiffOffset, reader.Offset))
	}

	cf.ifd0.Init("IFD#0", cf, GetExifTagName)
	cf.ifd0.readFrom(reader)
	exifTagOffset := cf.ifd0.TagsById[ExifImageExifTag].Uint32Value()
	ifd0StripOffset := cf.ifd0.TagsById[ExifImageStripOffset].Uint32Value()
	ifd0StripBytesCount := cf.ifd0.TagsById[ExifImageStripBytesCount].Uint32Value()
	cf.extractFields(reader, exifTagOffset)
	if exifTagOffset > cf.ifd0.NextIFDOffset {
		panic(fmt.Sprintf("ExifTagOffset > IFD0.NextIFDOffset: %d>%d !", exifTagOffset, cf.ifd0.NextIFDOffset))
	}

	reader.MoveTo("IFD#0.ExifTagPointer", int64(exifTagOffset))
	cf.exifSubIfd.Init("ExifSubIfd", cf, GetExifTagName)
	cf.exifSubIfd.readFrom(reader)
	makerNoteStartOffset := cf.exifSubIfd.TagsById[ExifPhotoMakerNote].DataOrOffset
	if cf.exifSubIfd.NextIFDOffset != 0 {
		panic("Unexpected next IFD Offset in exifSubIfd !")
	}
	cf.extractFields(reader, makerNoteStartOffset)

	reader.MoveTo("makerNoteOffset", int64(makerNoteStartOffset))
	cf.makerNodeSubIfd.Init("MakerNotesIFD", cf, GetCanonTagName)
	cf.makerNodeSubIfd.readFrom(reader)
	cf.extractFields(reader, cf.ifd0.NextIFDOffset)
	if cf.makerNodeSubIfd.NextIFDOffset != 0 {
		panic("Unexpected next IFD Offset in makerNodeSubIfd !")
	}

	reader.MoveTo("IFD#0.NextIFDOffset", int64(cf.ifd0.NextIFDOffset))
	cf.ifd1.Init("IFD#1", cf, GetExifTagName)
	cf.ifd1.readFrom(reader)
	cf.extractFields(reader, cf.ifd1.NextIFDOffset)
	ifd1ThumbnailOffset := cf.ifd1.TagsById[ExifImageThumbnailOffset].Uint32Value()
	idf1ThumbnailLength := cf.ifd1.TagsById[ExifImageThumbnailLength].Uint32Value()

	reader.MoveTo("IFD#1.NextIFDOffset", int64(cf.ifd1.NextIFDOffset))
	cf.ifd2.Init("IFD#2", cf, GetExifTagName)
	cf.ifd2.readFrom(reader)
	ifd2ImageWidth := cf.ifd2.TagsById[ExifImageWidth].Uint16Value()
	ifd2ImageHeight := cf.ifd2.TagsById[ExifImageHeight].Uint16Value()
	ifd2StripOffset := cf.ifd2.TagsById[ExifImageStripOffset].Uint32Value()
	cf.extractFields(reader, cf.ifd2.NextIFDOffset)

	reader.MoveTo("IFD#2.NextIFDOffset", int64(cf.ifd2.NextIFDOffset))
	if int64(cf.CR2Header.RawIfdOffset) != reader.Offset {
		panic("IDF#3 offset not equals to CR2 Header RawIdOffset !")
	}
	cf.ifd3.Init("IFD#3", cf, GetExifTagName)
	cf.ifd3.readFrom(reader)
	ifd3ImageWidth := cf.ifd3.TagsById[ExifImageWidth].Uint16Value()
	ifd3ImageHeight := cf.ifd3.TagsById[ExifImageHeight].Uint16Value()
	ifd3StripOffset := cf.ifd3.TagsById[ExifImageStripOffset].Uint32Value()
	ifd3StripBytesCount := cf.ifd3.TagsById[ExifImageStripBytesCount].Uint32Value()
	cf.extractFields(reader, ifd1ThumbnailOffset)
	uint16buffer := cf.ifd3.TagsById[ExifImageCR2Slice].Uint16ArrayValue(cf)
	ifd3CR2Slice := Slice{SliceCount: uint16buffer[0], SliceSize: uint16buffer[1], LastSliceSize: uint16buffer[2]}
	if cf.ifd3.NextIFDOffset != 0 {
		panic(fmt.Sprintf("Unexpected IFD after IFD#3 !"))
	}

	cf.ifd0.dumpTags(cf)
	cf.exifSubIfd.dumpTags(cf)
	cf.makerNodeSubIfd.dumpTags(cf)
	cf.ifd1.dumpTags(cf)
	cf.ifd2.dumpTags(cf)
	cf.ifd3.dumpTags(cf)

	reader.MoveTo("IFD#1.ThumbnailOffset", int64(ifd1ThumbnailOffset))
	cf.Image1 = readJpegImage(reader, idf1ThumbnailLength)

	reader.MoveTo("IFD#0.StripOffsets", int64(ifd0StripOffset))
	cf.Image0 = readJpegImage(reader, ifd0StripBytesCount)

	reader.MoveTo("IFD#2.StripOffsets", int64(ifd2StripOffset))
	cf.Image2 = readRGBAImage(reader, ifd2ImageWidth, ifd2ImageHeight)

	reader.MoveTo("IFD#3.StripOffsets", int64(ifd3StripOffset))
	rawDataBuffer := make([]byte, ifd3StripBytesCount)
	fmt.Printf("Reading %d bytes of RAW image...", ifd3StripBytesCount)
	reader.ReadInto(int64(ifd3StripBytesCount), &rawDataBuffer)
	fmt.Printf("done\n")
	rawReader := bufreader.BufferReader{
		Reader:    bytes.NewReader(rawDataBuffer),
		ByteOrder: binary.BigEndian,
		Offset:    0,
	}
	cf.Image3 = readRawImage(&rawReader, ifd3ImageWidth, ifd3ImageHeight, ifd3CR2Slice)
}

func (cf *CR2File) AddValueToExtract(entry *IFDEntry) {
	if cf.ValuesByOffset[entry.DataOrOffset] != nil {
		return
	}
	cf.ValuesToExtract = append(cf.ValuesToExtract, entry)
}

func (cf *CR2File) AddDataAtOffset(offset uint32, val interface{}) {
	value := cf.ValuesByOffset[offset]
	if value == nil {
		cf.ValuesByOffset[offset] = val
	} else {
		panic(fmt.Sprintf("Already got value for offset %x", offset))
	}
}

func (cf *CR2File) extractFields(reader *bufreader.BufferReader, limit uint32) {

	valuesToExtract := cf.ValuesToExtract
	cf.ValuesToExtract = make([]*IFDEntry, 0)
	sort.Sort(ByOffset{valuesToExtract})

	for i, entry := range valuesToExtract {

		name := GetExifTagName(entry.TagID)

		if limit != 0 && entry.DataOrOffset >= limit {
			//fmt.Printf("Requested offset for %s is past limit ! %d>=%d, stopping extraction there\n", name, entry.DataOrOffset, limit)
			cf.ValuesToExtract = valuesToExtract[i:]
			break
		}

		if reader.Offset > int64(entry.DataOrOffset) {
			//fmt.Printf("Field %s points to an offset BEFORE CURRENT, skipping field %d<%d\n", name, entry.DataOrOffset, reader.Offset)
			continue
		}

		reader.MoveTo(name, int64(entry.DataOrOffset))
		//fmt.Printf("@%d %d ", entry.DataOrOffset, reader.Offset)

		switch entry.TagType {
		case TagTypeString:
			buffer := reader.ReadBuffer(int64(entry.NumberOfValues))
			val := string(buffer[:len(buffer)-1])
			cf.AddDataAtOffset(entry.DataOrOffset, val)
			//fmt.Printf("%s=%s\n", name, val)
		case TagTypeUint16:
			val := make([]uint16, entry.NumberOfValues)
			reader.ReadInto(int64(2*entry.NumberOfValues), &val)
			cf.AddDataAtOffset(entry.DataOrOffset, val)
			//fmt.Printf("%s=%v\n", name, val)
		case TagTypeUint32:
			val := make([]uint32, entry.NumberOfValues)
			reader.ReadInto(int64(4*entry.NumberOfValues), &val)
			cf.AddDataAtOffset(entry.DataOrOffset, val)
			//fmt.Printf("%s=%v\n", name, val)
		case TagTypeUrational:
			var val Rational
			reader.ReadInto(8, &val)
			cf.AddDataAtOffset(entry.DataOrOffset, val)
			//fmt.Printf("%s=%d/%d\n", name, val.Numerator, val.Denominator)
		case TagTypeByteSequence:
			val := reader.ReadBuffer(int64(entry.NumberOfValues))
			cf.AddDataAtOffset(entry.DataOrOffset, val)
			//fmt.Printf("%s=%v\n", name, val)
		case TagTypeUbyte:
			val := make([]uint8, entry.NumberOfValues)
			reader.ReadInto(int64(entry.NumberOfValues), &val)
			cf.AddDataAtOffset(entry.DataOrOffset, val)
			//fmt.Printf("%s=%v\n", name, val)
		case TagTypeRational:
			var val SRational
			reader.ReadInto(8, &val)
			cf.AddDataAtOffset(entry.DataOrOffset, val)
			//fmt.Printf("%s=%d/%d\n", name, val.Numerator, val.Denominator)
		default:
			fmt.Printf("TagType cannot be extracted for %s: %d\n", name, entry.TagType)
		}
	}
}

type CR2Header struct {
	Cr2Magic     int16
	Cr2Major     byte
	Cr2Minor     byte
	RawIfdOffset uint32
}

func (h *CR2Header) readFrom(reader *bufreader.BufferReader) {
	reader.ReadInto(8, h)
	if h.Cr2Magic != 0x5243 {
		panic(fmt.Sprintf("Invalid CR magic: %x\n", h.Cr2Magic))
	}
	if (h.Cr2Major != 2) || (h.Cr2Minor != 0) {
		panic(fmt.Sprintf("Unsuported CR2 version\n"))
	}
}

type Rational struct {
	Numerator   uint32
	Denominator uint32
}

type SRational struct {
	Numerator   int32
	Denominator int32
}

type Slice struct {
	SliceCount    uint16
	SliceSize     uint16
	LastSliceSize uint16
}
