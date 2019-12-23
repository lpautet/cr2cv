package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
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
}

func (cf *CR2File) init() {
	cf.ValuesByOffset = make(map[uint32]interface{})
}

func (cf *CR2File) readFrom(reader *BufferReader) {
	cf.TiffHeader.readFrom(reader)
	cf.CR2Header.readFrom(reader)

	if reader.Offset != int64(cf.TiffHeader.TiffOffset) {
		panic(fmt.Sprintf("Incomplete TIFF tiffHeader read expected offset %d, real %d", cf.TiffHeader.TiffOffset, reader.Offset))
	}

	cf.ifd0.init("IFD#0", cf, getExifTagName)
	cf.ifd0.readFrom(reader)
	exifTagOffset := cf.ifd0.TagsById[ExifImageExifTag].uint32Value()
	ifd0StripOffset := cf.ifd0.TagsById[ExifImageStripOffset].uint32Value()
	ifd0StripBytesCount := cf.ifd0.TagsById[ExifImageStripBytesCount].uint32Value()
	cf.extractFields(reader, exifTagOffset)
	if exifTagOffset > cf.ifd0.NextIFDOffset {
		panic(fmt.Sprintf("ExifTagOffset > IFD0.NextIFDOffset: %d>%d !", exifTagOffset, cf.ifd0.NextIFDOffset))
	}

	reader.moveTo("IFD#0.ExifTagPointer", int64(exifTagOffset))
	cf.exifSubIfd.init("ExifSubIfd", cf, getExifTagName)
	cf.exifSubIfd.readFrom(reader)
	makerNoteStartOffset := cf.exifSubIfd.TagsById[ExifPhotoMakerNote].DataOrOffset
	if cf.exifSubIfd.NextIFDOffset != 0 {
		panic("Unexpected next IFD Offset in exifSubIfd !")
	}
	cf.extractFields(reader, makerNoteStartOffset)

	reader.moveTo("makerNoteOffset", int64(makerNoteStartOffset))
	cf.makerNodeSubIfd.init("MakerNotesIFD", cf, getCanonTagName)
	cf.makerNodeSubIfd.readFrom(reader)
	cf.extractFields(reader, cf.ifd0.NextIFDOffset)
	if cf.makerNodeSubIfd.NextIFDOffset != 0 {
		panic("Unexpected next IFD Offset in makerNodeSubIfd !")
	}

	reader.moveTo("IFD#0.NextIFDOffset", int64(cf.ifd0.NextIFDOffset))
	cf.ifd1.init("IFD#1", cf, getExifTagName)
	cf.ifd1.readFrom(reader)
	cf.extractFields(reader, cf.ifd1.NextIFDOffset)
	ifd1ThumbnailOffset := cf.ifd1.TagsById[ExifImageThumbnailOffset].uint32Value()
	idf1ThumbnailLength := cf.ifd1.TagsById[ExifImageThumbnailLength].uint32Value()

	reader.moveTo("IFD#1.NextIFDOffset", int64(cf.ifd1.NextIFDOffset))
	cf.ifd2.init("IFD#2", cf, getExifTagName)
	cf.ifd2.readFrom(reader)
	ifd2ImageWidth := cf.ifd2.TagsById[ExifImageWidth].uint16Value()
	ifd2ImageHeight := cf.ifd2.TagsById[ExifImageHeight].uint16Value()
	ifd2StripOffset := cf.ifd2.TagsById[ExifImageStripOffset].uint32Value()
	cf.extractFields(reader, cf.ifd2.NextIFDOffset)

	reader.moveTo("IFD#2.NextIFDOffset", int64(cf.ifd2.NextIFDOffset))
	if int64(cf.CR2Header.RawIfdOffset) != reader.Offset {
		panic("IDF#3 offset not equals to CR2 Header RawIdOffset !")
	}
	cf.ifd3.init("IFD#3", cf, getExifTagName)
	cf.ifd3.readFrom(reader)
	ifd3ImageWidth := cf.ifd3.TagsById[ExifImageWidth].uint16Value()
	ifd3ImageHeight := cf.ifd3.TagsById[ExifImageHeight].uint16Value()
	ifd3StripOffset := cf.ifd3.TagsById[ExifImageStripOffset].uint32Value()
	ifd3StripBytesCount := cf.ifd3.TagsById[ExifImageStripBytesCount].uint32Value()
	cf.extractFields(reader, ifd1ThumbnailOffset)
	uint16buffer := cf.ifd3.TagsById[ExifImageCR2Slice].uint16ArrayValue(cf)
	ifd3CR2Slice := CR2Slice{SliceCount: uint16buffer[0], SliceSize: uint16buffer[1], LastSliceSize: uint16buffer[2]}
	if cf.ifd3.NextIFDOffset != 0 {
		panic(fmt.Sprintf("Unexpected IFD after IFD#3 !"))
	}

	cf.ifd0.dumpTags(cf)
	cf.exifSubIfd.dumpTags(cf)
	cf.makerNodeSubIfd.dumpTags(cf)
	cf.ifd1.dumpTags(cf)
	cf.ifd2.dumpTags(cf)
	cf.ifd3.dumpTags(cf)

	reader.moveTo("IFD#1.ThumbnailOffset", int64(ifd1ThumbnailOffset))
	image1 = readJpegImage(reader, idf1ThumbnailLength)

	reader.moveTo("IFD#0.StripOffsets", int64(ifd0StripOffset))
	image0 = readJpegImage(reader, ifd0StripBytesCount)

	reader.moveTo("IFD#2.StripOffsets", int64(ifd2StripOffset))
	image2 = readRGBAImage(reader, ifd2ImageWidth, ifd2ImageHeight)

	reader.moveTo("IFD#3.StripOffsets", int64(ifd3StripOffset))
	rawDataBuffer := make([]byte, ifd3StripBytesCount)
	fmt.Printf("Reading %d bytes of RAW image...", ifd3StripBytesCount)
	reader.readInto(int64(ifd3StripBytesCount), &rawDataBuffer)
	fmt.Printf("done\n")
	rawReader := BufferReader{
		Reader:    bytes.NewReader(rawDataBuffer),
		ByteOrder: binary.BigEndian,
		Offset:    0,
	}
	image3 = readRawImage(&rawReader, ifd3ImageWidth, ifd3ImageHeight, ifd3CR2Slice)
}

func (cf *CR2File) addValueToExtract(entry *IFDEntry) {
	if cf.ValuesByOffset[entry.DataOrOffset] != nil {
		return
	}
	cf.ValuesToExtract = append(cf.ValuesToExtract, entry)
}

func (cf *CR2File) addDataAtOffset(offset uint32, val interface{}) {
	value := cf.ValuesByOffset[offset]
	if value == nil {
		cf.ValuesByOffset[offset] = val
	} else {
		panic(fmt.Sprintf("Already got value for offset %x", offset))
	}
}

type TiffHeader struct {
	ByteOrder  [2]byte
	TiffMagic  int16
	TiffOffset uint32
}

func (th *TiffHeader) readFrom(reader *BufferReader) {
	reader.readInto(8, th)
	if (th.ByteOrder[0] != 'I') || (th.ByteOrder[1] != 'I') {
		panic(fmt.Sprintf("Unsuported byte order\n"))
	}
	if th.TiffMagic != 0x002a {
		panic(fmt.Sprintf("Invalid TIFF magic\n"))
	}
}

type CR2Header struct {
	Cr2Magic     int16
	Cr2Major     byte
	Cr2Minor     byte
	RawIfdOffset uint32
}

func (h *CR2Header) readFrom(reader *BufferReader) {
	reader.readInto(8, h)
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

func (cf *CR2File) extractFields(reader *BufferReader, limit uint32) {

	valuesToExtract := cf.ValuesToExtract
	cf.ValuesToExtract = make([]*IFDEntry, 0)
	sort.Sort(ByOffset{valuesToExtract})

	for i, entry := range valuesToExtract {

		name := getExifTagName(entry.TagID)

		if limit != 0 && entry.DataOrOffset >= limit {
			//fmt.Printf("Requested offset for %s is past limit ! %d>=%d, stopping extraction there\n", name, entry.DataOrOffset, limit)
			cf.ValuesToExtract = valuesToExtract[i:]
			break
		}

		if reader.Offset > int64(entry.DataOrOffset) {
			//fmt.Printf("Field %s points to an offset BEFORE CURRENT, skipping field %d<%d\n", name, entry.DataOrOffset, reader.Offset)
			continue
		}

		reader.moveTo(name, int64(entry.DataOrOffset))
		//fmt.Printf("@%d %d ", entry.DataOrOffset, reader.Offset)

		switch entry.TagType {
		case TagTypeString:
			buffer := reader.readBuffer(int64(entry.NumberOfValues))
			val := string(buffer[:len(buffer)-1])
			cf.addDataAtOffset(entry.DataOrOffset, val)
			//fmt.Printf("%s=%s\n", name, val)
		case TagTypeUint16:
			val := make([]uint16, entry.NumberOfValues)
			reader.readInto(int64(2*entry.NumberOfValues), &val)
			cf.addDataAtOffset(entry.DataOrOffset, val)
			//fmt.Printf("%s=%v\n", name, val)
		case TagTypeUint32:
			val := make([]uint32, entry.NumberOfValues)
			reader.readInto(int64(4*entry.NumberOfValues), &val)
			cf.addDataAtOffset(entry.DataOrOffset, val)
			//fmt.Printf("%s=%v\n", name, val)
		case TagTypeUrational:
			var val Rational
			reader.readInto(8, &val)
			cf.addDataAtOffset(entry.DataOrOffset, val)
			//fmt.Printf("%s=%d/%d\n", name, val.Numerator, val.Denominator)
		case TagTypeByteSequence:
			val := reader.readBuffer(int64(entry.NumberOfValues))
			cf.addDataAtOffset(entry.DataOrOffset, val)
			//fmt.Printf("%s=%v\n", name, val)
		case TagTypeUbyte:
			val := make([]uint8, entry.NumberOfValues)
			reader.readInto(int64(entry.NumberOfValues), &val)
			cf.addDataAtOffset(entry.DataOrOffset, val)
			//fmt.Printf("%s=%v\n", name, val)
		case TagTypeRational:
			var val SRational
			reader.readInto(8, &val)
			cf.addDataAtOffset(entry.DataOrOffset, val)
			//fmt.Printf("%s=%d/%d\n", name, val.Numerator, val.Denominator)
		default:
			fmt.Printf("TagType cannot be extracted for %s: %d\n", name, entry.TagType)
		}
	}
}

type ImageFileDirectory struct {
	Name            string
	NumberOfEntries uint16
	Entries         []IFDEntry
	NextIFDOffset   uint32

	TagsById   map[uint16]*IFDEntry
	TagsByName map[string]*IFDEntry

	ParentFile *CR2File
	resolver   TagNameResolver
}

func (ifd *ImageFileDirectory) init(name string, file *CR2File, resolver TagNameResolver) {
	ifd.Name = name
	ifd.ParentFile = file
	ifd.TagsById = make(map[uint16]*IFDEntry)
	ifd.TagsByName = make(map[string]*IFDEntry)
	ifd.resolver = resolver
}

var KnownExifTags = map[uint16]string{
	0x0100: "Exif.Image.ImageWidth",
	0x0101: "Exif.Image.ImageHeight",
	0x0102: "Exif.Image.BitsPerSample",
	0x0103: "Exif.Image.Compression",
	0x0106: "Exif.Image.PhotometricInterpretation",
	0x010f: "Exif.Image.Make",
	0x0110: "Exif.Image.Model",
	0x0111: "Exif.Image.StripOffsets",
	0x0112: "Exif.Image.Orientation",
	0x0115: "Exif.Image.SamplesPerPixel",
	0x0116: "Exif.Image.RowsPerStrip",
	0x0117: "Exif.Image.StripByteCounts",
	0x011a: "Exif.Image.XResolution",
	0x011b: "Exif.Image.YResolution",
	0x011c: "Exif.Image.PlanarConfiguration",
	0x0128: "Exif.Image.ResolutionUnit",
	0x0132: "Exif.Image.DateTime",
	0x013b: "Exif.Image.Artist",
	0x02bc: "Exif.Image.XMLPacket",
	0x0201: "Exif.Image.ThumbnailOffset",
	0x0202: "Exif.Image.ThumbnailLength",
	0x8298: "Exif.Image.Copyright",
	0x829a: "Exif.Image.ExposureTime",
	0x8769: "Exif.Image.ExifTag",
	0x829d: "Exif.Image.FNumber",
	0x8822: "Exif.Image.ExposureProgram",
	0x8825: "Exif.Image.GPSTag",
	0x8827: "Exif.Image.ISOSpeedRatings",
	0x8830: "Exif.Image.SensitivityType",
	0x8832: "Exif.Image.RecommendedExposureIndex",
	0x9000: "Exif.Photo.ExifVersion",
	0x9003: "Exif.Photo.DateTimeOriginal",
	0x9004: "Exif.Photo.DateTimeDigitized",
	0x9101: "Exif.Photo.ComponentsConfiguration",
	0x9201: "Exif.Photo.ShutterSpeedValue",
	0x9202: "Exif.Photo.ApertureValue",
	0x9204: "Exif.Photo.ExposureBiasValue",
	0x9207: "Exif.Photo.MeteringMode",
	0x9209: "Exif.Photo.Flash",
	0x920a: "Exif.Photo.FocalLength",
	0x927c: "Exif.Photo.MakerNote",
	0x9286: "Exif.Photo.UserComment",
	0x9290: "Exif.Photo.SubSecTime",
	0x9291: "Exif.Photo.SubSecTimeOriginal",
	0x9292: "Exif.Photo.SubSecTimeDigitized",
	0xa000: "Exif.Photo.FlashpixVersion",
	0xa001: "Exif.Photo.ColorSpace",
	0xa002: "Exif.Photo.PixelXDimension",
	0xa003: "Exif.Photo.PixelYDimension",
	0xa005: "Exif.Photo.InteroperabilityTag",
	0xa20e: "Exif.Photo.FocalPlaneXResolution",
	0xa20f: "Exif.Photo.FocalPlaneYResolution",
	0xa210: "Exif.Photo.FocalPlaneResolutionUnit",
	0xa401: "Exif.Photo.CustomRendered",
	0xa402: "Exif.Photo.ExposureMode",
	0xa403: "Exif.Photo.WhiteBalance",
	0xa406: "Exif.Photo.SceneCaptureType",
	0xa430: "Exif.Photo.CameraOwnerName",
	0xa431: "Exif.Photo.BodySerialNumber",
	0xa432: "Exif.Photo.LensSpecification",
	0xa434: "Exif.Photo.LensModel",
	0xa435: "Exif.Photo.LensSerialNumber",
}

var KnownCanonTags = map[uint16]string{
	0x0001: "Exif.Canon.CameraSettings",
	0x0002: "Exif.Canon.FocalLength",
	0x0003: "Exif.Canon.FlashInfo",
	0x0004: "Exif.Canon.ShotInfo",
	0x0006: "Exif.Canon.ImageType",
	0x0007: "Exif.Canon.FirmwareVersion",
	0x0009: "Exif.Canon.OwnerName",
	0x000d: "Exif.Canon.CameraInfo",
	0x0010: "Exif.Canon.CanonModelID",
	0x0013: "Exif.Canon.ThumbnailImageValidArea",
	0x0026: "Exif.Canon.AFInfo2",
	0x0035: "Exif.Canon.TimeInfo",
	0x0038: "Exif.Canon.BatteryType",
	0x0093: "Exif.Canon.FileInfo",
	0x0095: "Exif.Canon.LensModel",
	0x0096: "Exif.Canon.InternalSerialNumber",
	0x0097: "Exif.Canon.DustRemovalData",
	0x0098: "Exif.Canon.CropInfo",
	0x0099: "Exif.Canon.CustomFunctions2",
	0x009a: "Exif.Canon.AspectInfo",
	0x00a0: "Exif.Canon.ProcessingInfo",
	0x00aa: "Exif.Canon.MeasuredColor",
	0x00b4: "Exif.Canon.ColorSpace",
	0x00d0: "Exif.Canon.VRDOffset",
	0x00e0: "Exif.Canon.SensorInfo",
	0x4001: "Exif.Canon.ColorData",
	0x4002: "Exif.Canon.CRWParam",
	0x4005: "Exif.Canon.Flavor",
	0x4008: "Exif.Canon.PictureStyleUserDef",
	0x4009: "Exif.Canon.PictureStylePC",
	0x4010: "Exif.Canon.CustomPictureStyleFileName",
	0x4013: "Exif.Canon.AFMicroAdj",
	0x4015: "Exif.Canon.VignettingCorr",
	0x4016: "Exif.Canon.VignettingCorr2",
	0x4018: "Exif.Canon.LightingOpt",
	0x4019: "Exif.Canon.LensInfo",
	0x4020: "Exif.Canon.AmbienceInfo",
	0x4021: "Exif.Canon.MultiExp",
	0x4024: "Exif.Canon.FilterInfo",
	0x4025: "Exif.Canon.HDRInfo",
}

type TagNameResolver func(uint16) string

func getExifTagName(tagId uint16) string {
	ret := KnownExifTags[tagId]
	if ret != "" {
		return ret
	}
	return fmt.Sprintf("Exif.Tag-0x%x", tagId)
}

func getCanonTagName(tagId uint16) string {
	ret := KnownCanonTags[tagId]
	if ret != "" {
		return ret
	}
	return fmt.Sprintf("Exif.Canon.Tag-0x%x", tagId)
}

type IFDEntry struct {
	TagID          uint16
	TagType        uint16
	NumberOfValues uint32
	DataOrOffset   uint32
}

type IFDEntries []*IFDEntry

func (s IFDEntries) Len() int      { return len(s) }
func (s IFDEntries) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

type ByOffset struct{ IFDEntries }

func (s ByOffset) Less(i, j int) bool {
	return s.IFDEntries[i].DataOrOffset < s.IFDEntries[j].DataOrOffset
}

func (e *IFDEntry) stringValue(file *CR2File) string {
	if e.TagType != TagTypeString {
		panic("Requesting string from an invalid entry type")
	}
	if e.DataOrOffset == 0 {
		return ""
	}
	value := file.ValuesByOffset[e.DataOrOffset]
	if value == nil {
		return fmt.Sprintf("<%d offset not found>", e.DataOrOffset)
	}
	return value.(string)
}

func (e *IFDEntry) uint16Value() uint16 {
	if e.TagType != TagTypeUint16 {
		panic("Requesting uint16 from an invalid entry type")
	}
	if e.NumberOfValues != 1 {
		panic("Requesting uint16 for an array entry type")
	}
	return uint16(e.DataOrOffset)
}

func (e *IFDEntry) uint32Value() uint32 {
	if e.TagType != TagTypeUint32 {
		panic("Requesting uint32 from an invalid entry type")
	}
	if e.NumberOfValues != 1 {
		panic("Requesting uint32 for an array entry type")
	}
	return e.DataOrOffset
}

func (e *IFDEntry) byteArrayValue(file *CR2File) []byte {
	if e.TagType != TagTypeByteSequence {
		panic(fmt.Sprintf("Requesting byte sequence from an invalid entry type: %d", e.TagType))
	}
	if e.NumberOfValues < 4 {
		panic("Requesting too small byte sequence")
	}
	value := file.ValuesByOffset[e.DataOrOffset]
	if value == nil {
		return nil
	}
	return value.([]byte)
}

func (e *IFDEntry) uint8ArrayValue(file *CR2File) []uint8 {
	if e.TagType != TagTypeUbyte {
		panic(fmt.Sprintf("Requesting uint8 array from an invalid entry type: %d", e.TagType))
	}
	if e.NumberOfValues <=4 {
		panic("Requesting too small uint8 sequence")
	}
	value := file.ValuesByOffset[e.DataOrOffset]
	if value == nil {
		return nil
	}
	return value.([]uint8)
}

func (e *IFDEntry) uint16ArrayValue(file *CR2File) []uint16 {
	if e.TagType != TagTypeUint16 {
		panic(fmt.Sprintf("Requesting uint16 array from an invalid entry type: %d", e.TagType))
	}
	if e.NumberOfValues <= 2 {
		panic("Requesting too small uint16 sequence")
	}
	value := file.ValuesByOffset[e.DataOrOffset]
	return value.([]uint16)
}

func (e *IFDEntry) uint32ArrayValue(file *CR2File) []uint32 {
	if e.TagType != TagTypeUint32 {
		panic(fmt.Sprintf("Requesting uint32 array from an invalid entry type: %d", e.TagType))
	}
	if e.NumberOfValues <= 1 {
		panic("Requesting too small uint32 sequence")
	}
	value := file.ValuesByOffset[e.DataOrOffset]
	return value.([]uint32)
}

func (e *IFDEntry) value(file *CR2File) interface{} {
	switch e.TagType {
	case TagTypeUbyte:
		if e.NumberOfValues <= 4 {
			return e.DataOrOffset
		}
		return e.uint8ArrayValue(file)
	case TagTypeString:
		return e.stringValue(file)
	case TagTypeUint16:
		if e.NumberOfValues <= 2 {
			return e.DataOrOffset
		}
		return e.uint16ArrayValue(file)
	case TagTypeUint32:
		if e.NumberOfValues == 1 {
			return e.DataOrOffset
		}
		return e.uint32ArrayValue(file)
	case TagTypeUrational:
		return file.ValuesByOffset[e.DataOrOffset]
	case TagTypeByteSequence:
		return e.byteArrayValue(file)
	case TagTypeRational:
		value := file.ValuesByOffset[e.DataOrOffset]
		if e.NumberOfValues == 1 {
			return value.(SRational)
		}
		panic("Not implemented")
	default:
		panic("Not implemented")
	}
}

func (ife *IFDEntry) readFrom(fr *BufferReader) {
	fr.readInto(12, ife)
}

const TagTypeUbyte = 0x01
const TagTypeString = 0x02
const TagTypeUint16 = 0x03
const TagTypeUint32 = 0x04
const TagTypeUrational = 0x05
const TagTypeByteSequence = 0x07
const TagTypeRational = 0x0a

func (ifd *ImageFileDirectory) readFrom(reader *BufferReader) {

	ifd.NumberOfEntries = reader.readUint16()

	fmt.Printf("%s: Number of entries: %d\n", ifd.Name, ifd.NumberOfEntries)
	ifd.Entries = make([]IFDEntry, ifd.NumberOfEntries)
	reader.readInto(int64(12*ifd.NumberOfEntries), &ifd.Entries)

	for i, entry := range ifd.Entries {
		pEntry := &(ifd.Entries[i])
		ifd.TagsById[entry.TagID] = pEntry
		TagName := ifd.resolver(entry.TagID)
		ifd.TagsByName[TagName] = pEntry
		//fmt.Printf("IFD[%d] %s type=%d count=%d offset/data=%x\n", i, TagName, entry.TagType, entry.NumberOfValues, entry.DataOrOffset)
		//fmt.Printf("Extracting %s@%d (n=%d, type=%d)\n", extractor.name, extractor.entry.DataOrOffset, extractor.entry.NumberOfValues, extractor.entry.TagType)
		switch entry.TagType {
		case TagTypeString:
			if entry.DataOrOffset != 0 {
				ifd.ParentFile.addValueToExtract(pEntry)
			}
		case TagTypeUint16:
			if entry.NumberOfValues != 1 {
				ifd.ParentFile.addValueToExtract(pEntry)
			}
		case TagTypeUint32:
			if entry.NumberOfValues != 1 {
				ifd.ParentFile.addValueToExtract(pEntry)
			}
		case TagTypeByteSequence:
			if entry.NumberOfValues > 4 {
				ifd.ParentFile.addValueToExtract(pEntry)
			}
		case TagTypeUbyte:
			if entry.NumberOfValues > 4 {
				ifd.ParentFile.addValueToExtract(pEntry)
			}
		case TagTypeUrational:
			ifd.ParentFile.addValueToExtract(pEntry)
		case TagTypeRational:
			ifd.ParentFile.addValueToExtract(pEntry)
		default:
			fmt.Printf("Unknown TagType in %s for entry %s: %d\n", ifd.Name, KnownExifTags[entry.TagID], entry.TagType)
			continue
		}
	}
	ifd.NextIFDOffset = reader.readUint32()
}

func (ifd *ImageFileDirectory) dumpTags(file *CR2File) {
	fmt.Printf("%s:\n", ifd.Name)
	for tagName, tagEntry := range ifd.TagsByName {
		tagValue := tagEntry.value(file)
		fmt.Printf("\t%s: :%v\n", tagName, tagValue)
	}
}

type BufferReader struct {
	Reader    io.Reader
	ByteOrder binary.ByteOrder
	Offset    int64
}

func (fr *BufferReader) readInto(limit int64, data interface{}) {
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

func (fr *BufferReader) readByte() byte {
	var ret byte
	err := binary.Read(fr.Reader, fr.ByteOrder, &ret)
	if err != nil {
		panic(err)
	}
	fr.Offset += 1
	return ret
}

func (fr *BufferReader) readUint16() uint16 {
	var ret uint16
	err := binary.Read(fr.Reader, fr.ByteOrder, &ret)
	if err != nil {
		panic(err)
	}
	fr.Offset += 2
	return ret
}

func (fr *BufferReader) readUint32() uint32 {
	var ret uint32
	err := binary.Read(fr.Reader, fr.ByteOrder, &ret)
	if err != nil {
		panic(err)
	}
	fr.Offset += 4
	return ret
}

func (fr *BufferReader) readBuffer(size int64) []byte {
	buffer := make([]byte, size)
	fr.readInto(size, &buffer)
	return buffer
}

func (fr *BufferReader) moveTo(name string, targetOffset int64) {
	if fr.Offset == targetOffset {
		return
	}
	if fr.Offset > targetOffset {
		panic(fmt.Sprintf("moveTo: target offset PASSED ALREADY for %s:%d<%d!\n", name, targetOffset, fr.Offset))
	}

	size := targetOffset - fr.Offset
	buffer := fr.readBuffer(size)
	for i, b := range buffer {
		if b != 0x0 {
			fmt.Printf("Discarded %d bytes while moving to %s:@%d %v\n", size, name, targetOffset, buffer[i:])
			break
		}
	}
}

func readRawImage(reader *BufferReader, imageWidth uint16, imageHeight uint16, cr2Slice CR2Slice) *image.RGBA64 {
	soi := reader.readUint16()
	if soi != 0xffd8 {
		panic(fmt.Sprintf("Image#3: Incorrect SOI magic: %x, expecting %x", soi, 0xffd8))
	}
	marker := reader.readUint16()
	if marker != 0xffc4 {
		panic(fmt.Sprintf("Image#3 unexpected marker: %x, expecting %x", marker, 0xffc4))
	}
	length := reader.readUint16()
	length -= 2

	huffMaps := make([]map[huffKey]uint8, 32)
	for c := byte(0); length > 0; c++ {
		classAndIndex := reader.readByte()
		length--
		tableClass := classAndIndex >> 4 & 0xf
		tableIndex := classAndIndex & 0xf
		if (tableClass) != 0 {
			panic(fmt.Sprintf("Unexpected Table Class: %x vs %x", tableClass, 0))
		}
		if tableIndex != c {
			panic(fmt.Sprintf("Unexpected Table Index: %d vs %d", c, tableIndex))
		}
		counts := [16]uint8{}
		reader.readInto(16, &counts)
		//fmt.Printf("%v\n", counts) // TODO remove
		length -= 16
		code := uint16(0)
		huffMap := make(map[huffKey]uint8)
		for i, count := range counts {
			bitCount := byte(i + 1)
			for j := uint8(0); j < count; j++ {
				var diffCodeLen uint8
				reader.readInto(1, &diffCodeLen)
				length--
				hk := huffKey{bitCount, code}
				huffMap[hk] = diffCodeLen
				//fmt.Printf("%d/%d %b %d\n", bitCount, count, code, diffCodeLen)
				code++
			}
			code <<= 1
		}
		huffMaps[c] = huffMap
	}

	marker = reader.readUint16()
	if marker != 0xffc3 {
		panic(fmt.Sprintf("Unexpected marker: %x vs %x", marker, 0xffc3))
	}
	length = reader.readUint16()
	length -= 2

	sof3Header := sof3{}
	reader.readInto(6, &sof3Header)
	length -= 6
	fmt.Printf("SOF3: Sample precision=%d, Number of lines=%d, Number of samples/line=%d, Number of components per frame:%d\n", sof3Header.SamplePrecision, sof3Header.NumberOfLines, sof3Header.SamplesPerLines, sof3Header.ComponentsPerFrame)
	if sof3Header.SamplePrecision != 14 {
		panic(fmt.Sprintf("Unsuported precision: %d bits!", sof3Header.SamplePrecision))
	}
	components := make([]componentInfo, sof3Header.ComponentsPerFrame)
	reader.readInto(int64(sof3Header.ComponentsPerFrame*3), &components)
	length -= uint16(sof3Header.ComponentsPerFrame) * 3
	for i, componentInfo := range components {
		horizontalSampling := componentInfo.Sampling >> 4 & 0xf
		verticalSampling := componentInfo.Sampling & 0xf
		fmt.Printf("Component %d: Horizontal sampling=%d, Vertical sampling=%d, Quantization table=%d\n", components[i].ComponentId, horizontalSampling, verticalSampling, components[i].Quantization)
		if componentInfo.ComponentId == 1 && (horizontalSampling != 1 || verticalSampling != 1) {
			panic(fmt.Sprintf("Unsuported  sampling for components %d: H:%d V:%d!", components[i].ComponentId, horizontalSampling, verticalSampling))
		}
		if componentInfo.ComponentId == 2 && (horizontalSampling != 1 || verticalSampling != 1) {
			panic(fmt.Sprintf("Unsuported  sampling for components %d: H:%d V:%d!", components[i].ComponentId, horizontalSampling, verticalSampling))
		}
		if componentInfo.Quantization != 0 {
			panic(fmt.Sprintf("Component %d: quantization is not supported! %v", components[i].ComponentId, components[i]))
		}
	}

	if length != 0 {
		panic(fmt.Sprintf("Incomplete read of SOF3 Header!"))
	}

	marker = reader.readUint16()
	if marker != 0xffda {
		panic(fmt.Sprintf("Unexpected marker: %x, expect %x", marker, 0xffda))
	}

	length = reader.readUint16()
	length -= 2

	var numberOfComponents uint8
	reader.readInto(1, &numberOfComponents)
	length--
	if numberOfComponents != sof3Header.ComponentsPerFrame {
		panic(fmt.Sprintf("Components number mismatch SOF3/SOS: %d/%d", sof3Header.ComponentsPerFrame, numberOfComponents))
	}

	tablesInfoHeader := make([]tablesInfo, numberOfComponents)
	reader.readInto(int64(sof3Header.ComponentsPerFrame*2), &tablesInfoHeader)
	length -= uint16(sof3Header.ComponentsPerFrame) * 2

	for _, tableInfo := range tablesInfoHeader {
		DCTable := tableInfo.Tables >> 4 & 0xf
		ACTable := tableInfo.Tables & 0xf
		fmt.Printf("Component %d: DC=%x AC=%x\n", tableInfo.ComponentId, DCTable, ACTable)
		if huffMaps[DCTable] == nil {
			panic(fmt.Sprintf("Unknown DC table for component %d: %d", tableInfo.ComponentId, DCTable))
		}
		if huffMaps[ACTable] == nil {
			panic(fmt.Sprintf("Unknown AC table for component %d: %d", tableInfo.ComponentId, ACTable))
		}
	}

	footer := sosFooter{}
	reader.readInto(3, &footer)
	length -= 3
	if footer.StartSpectraclPrediction != 1 {
		panic(fmt.Sprintf("Unsuported Start of spectral prediction selection: %d", footer.StartSpectraclPrediction))
	}
	if footer.EndSpectraclPrediction != 0 {
		panic(fmt.Sprintf("Unsuported End of spectral prediction selection: %d", footer.EndSpectraclPrediction))
	}
	if footer.ApproximationBitPosition != 0 {
		panic(fmt.Sprintf("Unsuported Successive approximation bit positions: %d", footer.ApproximationBitPosition))
	}

	if length != 0 {
		panic(fmt.Sprintf("Incomplete read of SOS Header!"))
	}

	image3 = image.NewRGBA64(image.Rect(0, 0, int(imageWidth), int(imageHeight)))
	readRawScanData(reader, cr2Slice, imageHeight, sof3Header, huffMaps, image3)

	eoi := reader.readUint16()
	if eoi != 0xffd9 {
		panic(fmt.Sprintf("Expected EOI, but read %x vs 0xffd9\n", eoi))
	}

	return image3
}

const ExifPhotoMakerNote = 0x927c
const ExifImageExifTag = 0x8769
const ExifImageCR2Slice = 0xC640
const ExifImageThumbnailOffset = 0x0201
const ExifImageThumbnailLength = 0x0202
const ExifImageStripOffset = 0x0111
const ExifImageStripBytesCount = 0x0117
const ExifImageWidth = 0x0100
const ExifImageHeight = 0x0101

func readJpegImage(fr *BufferReader, imageLength uint32) image.Image {
	image1Bytes := fr.readBuffer(int64(imageLength))
	imag1Reader := bytes.NewReader(image1Bytes)
	image1, err := jpeg.Decode(imag1Reader)
	if err != nil {
		panic(fmt.Sprintf("Error reading image1 JPEG %v", err))
	}
	fmt.Printf("Image1 bounds: %v, color model %v\n", image1.Bounds(), image1.ColorModel())
	return image1
}

func readRGBAImage(fr *BufferReader, width uint16, height uint16) *image.RGBA64 {
	image2 = image.NewRGBA64(image.Rect(0, 0, int(width), int(height)))
	for j := uint16(0); j < height; j++ {
		for i := uint16(0); i < width; i++ {
			pixelsRow := make([]uint16, 3)
			fr.readInto(6, &pixelsRow)
			image2.Set(int(i), int(j), color.RGBA64{R: 4 * pixelsRow[0], G: 4 * pixelsRow[1], B: 4 * pixelsRow[2], A: 0xffff})
		}
	}
	fmt.Printf("Image2 bounds: %v, color model %v\n", image2.Bounds(), image2.ColorModel())
	return image2
}

func main() {
	//fp, err := os.Open("/Users/lpautet/Pictures/2016/2016-04-08/IMG_0739.CR2")
	//fp, err := os.Open("/Users/lpautet/Desktop/IMG_2188.CR2")
	fp, err := os.Open("/Users/lpautet/tmp/IMG_8502.CR2")

	if err != nil {
		panic(err)
	}

	defer fp.Close()

	fr := BufferReader{Reader: fp, ByteOrder: binary.LittleEndian}
	cr := CR2File{}
	cr.init()

	cr.readFrom(&fr)

	fmt.Printf("offset=%d\n", fr.Offset)

	///Users/lpautet/Pictures/2016/2016-04-08/IMG_0739.CR2
	http.HandleFunc("/0/", handler0)
	http.HandleFunc("/1/", handler1)
	http.HandleFunc("/2/", handler2)
	http.HandleFunc("/3/", handler3)

	http.ListenAndServe(":8888", nil)
}

func readRawScanData(fr *BufferReader, cr2Slice CR2Slice, imageHeight uint16, sof3Header sof3, huffMaps []map[huffKey]byte, image3 *image.RGBA64) {

	fmt.Printf("Reading image#3 scan data...")

	c := uint8(0) // component
	x := 0
	y := 0
	s := 0 // slice

	var i uint16
	var j uint16
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("\nRecovered in readRawScanData, i=%d, j=%d, c=%d : %v", i, j, c, r)
		}
	}()
	defaultValue := uint16(1<<(sof3Header.SamplePrecision-1) - 1)
	//fmt.Printf("%d bits, %d %b\n", sof3Header.SamplePrecision, maxValue, maxValue)
	previousValues := make([]uint16, sof3Header.ComponentsPerFrame)
	previousRowFirstSamples := make([]uint16, sof3Header.ComponentsPerFrame)
	sliceWidth := cr2Slice.SliceSize

	for j = uint16(0); j < sof3Header.NumberOfLines; j++ {
		for k := byte(0); k < sof3Header.ComponentsPerFrame; k++ {
			if j != 0 {
				previousValues[k] = previousRowFirstSamples[k]
			} else {
				previousValues[k] = defaultValue
			}
		}

		//fmt.Printf("\nR%d[%d:%d]>", j, previousValues[0], previousValues[1])
		for i = uint16(0); i < sof3Header.SamplesPerLines; i++ {
			for c := uint8(0); c < sof3Header.ComponentsPerFrame; c++ {
				diffValue := getDiffValue(fr, huffMaps[c%2])
				newVal := int(previousValues[c]) + diffValue

				if newVal < 0 {
					panic("new value is <0!")
				}
				if newVal > (1 << 14) {
					panic(fmt.Sprintf("newval is > 14bits %d+%d=%d", previousValues[c], diffValue, newVal))
				}
				val := uint16(newVal)
				if i == 0 {
					previousRowFirstSamples[c] = val
					//fmt.Printf("C%d:%d\n", c, val)
				}
				previousValues[c] = val

				if x == int(sliceWidth)*(s+1) {
					//fmt.Printf("\nEnd of line s=%d x=%d, y=%d=>", s, x, y)
					x = int(sliceWidth) * s
					y++
					//fmt.Printf("s=%d x=%d, y=%d, j=%d, i=%d, c=%d (v=%d)", s, x, y, j, i, c, val)
				}
				if y == int(imageHeight) {
					//fmt.Printf("\nEnd of stripe s=%d x=%d, y=%d=>", s, x, y)
					y = 0
					s++
					x = int(sliceWidth) * s
					//fmt.Printf("s=%d x=%d, y=%d (v=%d)", s, x, y, val)
				}

				if y%2 == 0 {
					if c%2 == 0 {
						image3.Set(x, y, color.RGBA64{R: 4 * val, G: uint16(0), B: uint16(0), A: 0xffff})
					} else {
						image3.Set(x, y, color.RGBA64{R: uint16(0), G: 4 * val, B: uint16(0), A: 0xffff})
					}
				} else {
					if c%2 == 0 {
						image3.Set(x, y, color.RGBA64{R: uint16(0), G: 4 * val, B: uint16(0), A: 0xffff})
					} else {
						image3.Set(x, y, color.RGBA64{R: uint16(0), G: uint16(0), B: 6 * val, A: 0xffff})
					}
				}
				x++
			}
		}
	}

	fmt.Printf("done\n")
}

var queueLen = uint8(0)
var queueVal = uint32(0)

func readBits(fr *BufferReader, len uint8) uint16 {

	var readByte uint8
	var output uint16

	for len > queueLen {
		// Read a byte in, shift it up to join the queue
		fr.readInto(1, &readByte)
		//fmt.Printf(" [read new byte: %x] ", readByte)
		if readByte == 0xff {
			var padding byte
			fr.readInto(1, &padding)
			if padding != 0x00 {
				if padding == 0xd9 {
					panic("Unexpected end of image!")
				}
				panic(fmt.Sprintf("Non-null padding in stream!"))
			}
		}

		queueVal = queueVal | uint32(readByte)<<(24-queueLen)
		queueLen += 8
	}

	// Shift the requested number of bytes down to the other end
	output = uint16((queueVal >> (32 - len)) & ((1 << len) - 1));
	queueLen -= len;
	queueVal <<= len;
	return output
}

func getDiffCodeLength(fr *BufferReader, huffMap map[huffKey]uint8) uint8 {
	code := uint16(0)
	length := uint8(0)

	for {
		code <<= 1
		bit := readBits(fr, 1)
		code = code | bit
		length++
		hkey := huffKey{length, code}
		//fmt.Printf("Try: %d 0x%x %b %v\n", length, code, code, huffMap)
		val, ok := huffMap[hkey]
		if ok {
			return val
		}
		if length == 16 {
			panic(fmt.Sprintf("Invalid HUFFMAN ENCODING !\n"))
		}
	}
}

func getDiffValue(fr *BufferReader, huffMap map[huffKey]uint8) int {

	diffCodeLen := getDiffCodeLength(fr, huffMap)
	//fmt.Printf("diffCodeLen=%d, ", diffCodeLen)
	var diffValue int
	if diffCodeLen == 0 {
		diffValue = 0
	} else {
		diffCode := readBits(fr, diffCodeLen)
		//fmt.Printf("diffCode=%d, ", diffCode)
		bitMask := uint16(1) << (diffCodeLen - 1)
		if bitMask&diffCode != 0 {
			// positive diff
			//fmt.Printf("%d %x %b %d\n", diffCodeLen, diffCode, diffCode, diffCode)
			diffValue = int(diffCode)
		} else {
			// negative diff
			//fmt.Printf("(%d-%d)", ((1<<diffCodeLen) - 1), diffCode)
			//fmt.Printf("%d %d %b %d\n", diffCodeLen, diffCode, diffCode, - (  (1 << diffCodeLen) - 1 )+int(diffCode))
			diffValue = - ((1 << diffCodeLen) - 1) + int(diffCode)
		}
	}
	//fmt.Printf("diffValue=%d, ", diffValue)
	return diffValue
}

type sof3 struct {
	SamplePrecision    byte
	NumberOfLines      uint16
	SamplesPerLines    uint16
	ComponentsPerFrame byte
}

type tablesInfo struct {
	ComponentId byte
	Tables      byte
}

type sosFooter struct {
	StartSpectraclPrediction byte
	EndSpectraclPrediction   byte
	ApproximationBitPosition byte
}

type componentInfo struct {
	ComponentId  byte
	Sampling     byte
	Quantization byte
}

type component struct {
	HorizontalSampling byte
	VerticalSampling   byte
}

type huffKey struct {
	bitCount uint8
	code     uint16
}

type CR2Slice struct {
	SliceCount    uint16
	SliceSize     uint16
	LastSliceSize uint16
}

var image2, image3 *image.RGBA64
var image0, image1 image.Image

func handler1(w http.ResponseWriter, _ *http.Request) {
	writeJpegImage(w, image1)
}

func handler0(w http.ResponseWriter, _ *http.Request) {
	writeJpegImage(w, image0)
}

func handler2(w http.ResponseWriter, _ *http.Request) {
	writeImage(w, image2)
}

func handler3(w http.ResponseWriter, _ *http.Request) {
	writeImage(w, image3)
}

func writeJpegImage(w http.ResponseWriter, img image.Image) {

	buffer := new(bytes.Buffer)
	if err := jpeg.Encode(buffer, img, nil); err != nil {
		fmt.Println("unable to encode image.")
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(buffer.Bytes())))
	if _, err := w.Write(buffer.Bytes()); err != nil {
		fmt.Println("unable to write image.")
	}
}

func writeImage(w http.ResponseWriter, img *image.RGBA64) {

	buffer := new(bytes.Buffer)
	if err := png.Encode(buffer, img); err != nil {
		fmt.Println("unable to encode image.")
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(buffer.Bytes())))
	if _, err := w.Write(buffer.Bytes()); err != nil {
		fmt.Println("unable to write image.")
	}
}
