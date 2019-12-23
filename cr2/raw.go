package cr2

import (
	"bytes"
	"fmt"
	"github.com/lpautet/cr2cv/bufreader"
	"image"
	"image/color"
	"image/jpeg"
)

func readJpegImage(fr *bufreader.BufferReader, imageLength uint32) image.Image {
	image1Bytes := fr.ReadBuffer(int64(imageLength))
	imag1Reader := bytes.NewReader(image1Bytes)
	image1, err := jpeg.Decode(imag1Reader)
	if err != nil {
		panic(fmt.Sprintf("Error reading image1 JPEG %v", err))
	}
	fmt.Printf("Image1 bounds: %v, color model %v\n", image1.Bounds(), image1.ColorModel())
	return image1
}

func readRGBAImage(fr *bufreader.BufferReader, width uint16, height uint16) *image.RGBA64 {
	image2 := image.NewRGBA64(image.Rect(0, 0, int(width), int(height)))
	for j := uint16(0); j < height; j++ {
		for i := uint16(0); i < width; i++ {
			pixelsRow := make([]uint16, 3)
			fr.ReadInto(6, &pixelsRow)
			image2.Set(int(i), int(j), color.RGBA64{R: 4 * pixelsRow[0], G: 4 * pixelsRow[1], B: 4 * pixelsRow[2], A: 0xffff})
		}
	}
	fmt.Printf("Image2 bounds: %v, color model %v\n", image2.Bounds(), image2.ColorModel())
	return image2
}

var queueLen = uint8(0)
var queueVal = uint32(0)

func readBits(fr *bufreader.BufferReader, len uint8) uint16 {

	var readByte uint8
	var output uint16

	for len > queueLen {
		// Read a byte in, shift it up to join the queue
		fr.ReadInto(1, &readByte)
		//fmt.Printf(" [read new byte: %x] ", readByte)
		if readByte == 0xff {
			var padding byte
			fr.ReadInto(1, &padding)
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
	output = uint16((queueVal >> (32 - len)) & ((1 << len) - 1))
	queueLen -= len
	queueVal <<= len
	return output
}

func getDiffCodeLength(fr *bufreader.BufferReader, huffMap map[huffKey]uint8) uint8 {
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

func getDiffValue(fr *bufreader.BufferReader, huffMap map[huffKey]uint8) int {

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
			diffValue = -((1 << diffCodeLen) - 1) + int(diffCode)
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

func readRawImage(reader *bufreader.BufferReader, imageWidth uint16, imageHeight uint16, cr2Slice Slice) *image.RGBA64 {
	soi := reader.ReadUint16()
	if soi != 0xffd8 {
		panic(fmt.Sprintf("Image#3: Incorrect SOI magic: %x, expecting %x", soi, 0xffd8))
	}
	marker := reader.ReadUint16()
	if marker != 0xffc4 {
		panic(fmt.Sprintf("Image#3 unexpected marker: %x, expecting %x", marker, 0xffc4))
	}
	length := reader.ReadUint16()
	length -= 2

	huffMaps := make([]map[huffKey]uint8, 32)
	for c := byte(0); length > 0; c++ {
		classAndIndex := reader.ReadByte()
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
		reader.ReadInto(16, &counts)
		//fmt.Printf("%v\n", counts) // TODO remove
		length -= 16
		code := uint16(0)
		huffMap := make(map[huffKey]uint8)
		for i, count := range counts {
			bitCount := byte(i + 1)
			for j := uint8(0); j < count; j++ {
				var diffCodeLen uint8
				reader.ReadInto(1, &diffCodeLen)
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

	marker = reader.ReadUint16()
	if marker != 0xffc3 {
		panic(fmt.Sprintf("Unexpected marker: %x vs %x", marker, 0xffc3))
	}
	length = reader.ReadUint16()
	length -= 2

	sof3Header := sof3{}
	reader.ReadInto(6, &sof3Header)
	length -= 6
	fmt.Printf("SOF3: Sample precision=%d, Number of lines=%d, Number of samples/line=%d, Number of components per frame:%d\n", sof3Header.SamplePrecision, sof3Header.NumberOfLines, sof3Header.SamplesPerLines, sof3Header.ComponentsPerFrame)
	if sof3Header.SamplePrecision != 14 {
		panic(fmt.Sprintf("Unsuported precision: %d bits!", sof3Header.SamplePrecision))
	}
	components := make([]componentInfo, sof3Header.ComponentsPerFrame)
	reader.ReadInto(int64(sof3Header.ComponentsPerFrame*3), &components)
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

	marker = reader.ReadUint16()
	if marker != 0xffda {
		panic(fmt.Sprintf("Unexpected marker: %x, expect %x", marker, 0xffda))
	}

	length = reader.ReadUint16()
	length -= 2

	var numberOfComponents uint8
	reader.ReadInto(1, &numberOfComponents)
	length--
	if numberOfComponents != sof3Header.ComponentsPerFrame {
		panic(fmt.Sprintf("Components number mismatch SOF3/SOS: %d/%d", sof3Header.ComponentsPerFrame, numberOfComponents))
	}

	tablesInfoHeader := make([]tablesInfo, numberOfComponents)
	reader.ReadInto(int64(sof3Header.ComponentsPerFrame*2), &tablesInfoHeader)
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
	reader.ReadInto(3, &footer)
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

	image3 := image.NewRGBA64(image.Rect(0, 0, int(imageWidth), int(imageHeight)))
	readRawScanData(reader, cr2Slice, imageHeight, sof3Header, huffMaps, image3)

	eoi := reader.ReadUint16()
	if eoi != 0xffd9 {
		panic(fmt.Sprintf("Expected EOI, but read %x vs 0xffd9\n", eoi))
	}

	return image3
}

func readRawScanData(fr *bufreader.BufferReader, cr2Slice Slice, imageHeight uint16, sof3Header sof3, huffMaps []map[huffKey]byte, image3 *image.RGBA64) {

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
