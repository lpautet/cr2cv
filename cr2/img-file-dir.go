package cr2

import (
	"fmt"
	"github.com/lpautet/cr2cv/bufreader"
)

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

func (ifd *ImageFileDirectory) Init(name string, file *CR2File, resolver TagNameResolver) {
	ifd.Name = name
	ifd.ParentFile = file
	ifd.TagsById = make(map[uint16]*IFDEntry)
	ifd.TagsByName = make(map[string]*IFDEntry)
	ifd.resolver = resolver
}

type TagNameResolver func(uint16) string

func GetExifTagName(tagId uint16) string {
	ret := KnownExifTags[tagId]
	if ret != "" {
		return ret
	}
	return fmt.Sprintf("Exif.Tag-0x%x", tagId)
}

func GetCanonTagName(tagId uint16) string {
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

func (e *IFDEntry) StringValue(file *CR2File) string {
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

func (e *IFDEntry) Uint16Value() uint16 {
	if e.TagType != TagTypeUint16 {
		panic("Requesting uint16 from an invalid entry type")
	}
	if e.NumberOfValues != 1 {
		panic("Requesting uint16 for an array entry type")
	}
	return uint16(e.DataOrOffset)
}

func (e *IFDEntry) Uint32Value() uint32 {
	if e.TagType != TagTypeUint32 {
		panic("Requesting uint32 from an invalid entry type")
	}
	if e.NumberOfValues != 1 {
		panic("Requesting uint32 for an array entry type")
	}
	return e.DataOrOffset
}

func (e *IFDEntry) ByteArrayValue(file *CR2File) []byte {
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

func (e *IFDEntry) Uint8ArrayValue(file *CR2File) []uint8 {
	if e.TagType != TagTypeUbyte {
		panic(fmt.Sprintf("Requesting uint8 array from an invalid entry type: %d", e.TagType))
	}
	if e.NumberOfValues <= 4 {
		panic("Requesting too small uint8 sequence")
	}
	value := file.ValuesByOffset[e.DataOrOffset]
	if value == nil {
		return nil
	}
	return value.([]uint8)
}

func (e *IFDEntry) Uint16ArrayValue(file *CR2File) []uint16 {
	if e.TagType != TagTypeUint16 {
		panic(fmt.Sprintf("Requesting uint16 array from an invalid entry type: %d", e.TagType))
	}
	if e.NumberOfValues <= 2 {
		panic("Requesting too small uint16 sequence")
	}
	value := file.ValuesByOffset[e.DataOrOffset]
	return value.([]uint16)
}

func (e *IFDEntry) Uint32ArrayValue(file *CR2File) []uint32 {
	if e.TagType != TagTypeUint32 {
		panic(fmt.Sprintf("Requesting uint32 array from an invalid entry type: %d", e.TagType))
	}
	if e.NumberOfValues <= 1 {
		panic("Requesting too small uint32 sequence")
	}
	value := file.ValuesByOffset[e.DataOrOffset]
	return value.([]uint32)
}

func (e *IFDEntry) Value(file *CR2File) interface{} {
	switch e.TagType {
	case TagTypeUbyte:
		if e.NumberOfValues <= 4 {
			return e.DataOrOffset
		}
		return e.Uint8ArrayValue(file)
	case TagTypeString:
		return e.StringValue(file)
	case TagTypeUint16:
		if e.NumberOfValues <= 2 {
			return e.DataOrOffset
		}
		return e.Uint16ArrayValue(file)
	case TagTypeUint32:
		if e.NumberOfValues == 1 {
			return e.DataOrOffset
		}
		return e.Uint32ArrayValue(file)
	case TagTypeUrational:
		return file.ValuesByOffset[e.DataOrOffset]
	case TagTypeByteSequence:
		return e.ByteArrayValue(file)
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

func (ife *IFDEntry) ReadFrom(fr *bufreader.BufferReader) {
	fr.ReadInto(12, ife)
}

const TagTypeUbyte = 0x01
const TagTypeString = 0x02
const TagTypeUint16 = 0x03
const TagTypeUint32 = 0x04
const TagTypeUrational = 0x05
const TagTypeByteSequence = 0x07
const TagTypeRational = 0x0a

func (ifd *ImageFileDirectory) readFrom(reader *bufreader.BufferReader) {

	ifd.NumberOfEntries = reader.ReadUint16()

	fmt.Printf("%s: Number of entries: %d\n", ifd.Name, ifd.NumberOfEntries)
	ifd.Entries = make([]IFDEntry, ifd.NumberOfEntries)
	reader.ReadInto(int64(12*ifd.NumberOfEntries), &ifd.Entries)

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
				ifd.ParentFile.AddValueToExtract(pEntry)
			}
		case TagTypeUint16:
			if entry.NumberOfValues != 1 {
				ifd.ParentFile.AddValueToExtract(pEntry)
			}
		case TagTypeUint32:
			if entry.NumberOfValues != 1 {
				ifd.ParentFile.AddValueToExtract(pEntry)
			}
		case TagTypeByteSequence:
			if entry.NumberOfValues > 4 {
				ifd.ParentFile.AddValueToExtract(pEntry)
			}
		case TagTypeUbyte:
			if entry.NumberOfValues > 4 {
				ifd.ParentFile.AddValueToExtract(pEntry)
			}
		case TagTypeUrational:
			ifd.ParentFile.AddValueToExtract(pEntry)
		case TagTypeRational:
			ifd.ParentFile.AddValueToExtract(pEntry)
		default:
			fmt.Printf("Unknown TagType in %s for entry %s: %d\n", ifd.Name, KnownExifTags[entry.TagID], entry.TagType)
			continue
		}
	}
	ifd.NextIFDOffset = reader.ReadUint32()
}

func (ifd *ImageFileDirectory) dumpTags(file *CR2File) {
	fmt.Printf("%s:\n", ifd.Name)
	for tagName, tagEntry := range ifd.TagsByName {
		tagValue := tagEntry.Value(file)
		fmt.Printf("\t%s: :%v\n", tagName, tagValue)
	}
}
