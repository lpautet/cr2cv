package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/lpautet/cr2cv/bufreader"
	"github.com/lpautet/cr2cv/cr2"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"strconv"
)

var cr = cr2.CR2File{}

func main() {
	//fp, err := os.Open("/Users/lpautet/Pictures/2016/2016-04-08/IMG_0739.CR2")
	//fp, err := os.Open("/Users/lpautet/Desktop/IMG_2188.CR2")
	fp, err := os.Open("/Users/lpautet/tmp/IMG_8502.CR2")

	if err != nil {
		panic(err)
	}

	defer fp.Close()

	fr := bufreader.BufferReader{Reader: fp, ByteOrder: binary.LittleEndian}

	cr.Init()

	cr.ReadFrom(&fr)

	fmt.Printf("offset=%d\n", fr.Offset)

	///Users/lpautet/Pictures/2016/2016-04-08/IMG_0739.CR2
	http.HandleFunc("/0/", handler0)
	http.HandleFunc("/1/", handler1)
	http.HandleFunc("/2/", handler2)
	http.HandleFunc("/3/", handler3)

	http.ListenAndServe(":8888", nil)
}

func handler1(w http.ResponseWriter, _ *http.Request) {
	writeJpegImage(w, cr.Image1)
}

func handler0(w http.ResponseWriter, _ *http.Request) {
	writeJpegImage(w, cr.Image0)
}

func handler2(w http.ResponseWriter, _ *http.Request) {
	writeImage(w, cr.Image2)
}

func handler3(w http.ResponseWriter, _ *http.Request) {
	writeImage(w, cr.Image3)
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
