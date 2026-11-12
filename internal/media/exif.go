package media

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// EXIFOrientation reads the EXIF orientation tag (0x0112) from a JPEG
// file. Returns 0 when no EXIF orientation is present.
func EXIFOrientation(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return 0, err
	}
	return exifOrientationFromJPEG(data)
}

func exifOrientationFromJPEG(data []byte) (int, error) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 0, fmt.Errorf("not a JPEG")
	}
	off := 2
	for off+4 <= len(data) {
		if data[off] != 0xFF {
			return 0, fmt.Errorf("bad JPEG marker at %d", off)
		}
		marker := data[off+1]
		if marker == 0xDA || marker == 0xD9 { // SOS / EOI
			break
		}
		segLen := int(binary.BigEndian.Uint16(data[off+2 : off+4]))
		if marker == 0xE1 && segLen >= 8 && bytes.Equal(data[off+4:off+10], []byte("Exif\x00\x00")) {
			return parseTIFFOrientation(data[off+10 : off+2+segLen])
		}
		off += 2 + segLen
	}
	return 0, nil
}

func parseTIFFOrientation(tiff []byte) (int, error) {
	if len(tiff) < 8 {
		return 0, fmt.Errorf("TIFF header too short")
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0, fmt.Errorf("bad TIFF byte order")
	}
	ifdOff := int(bo.Uint32(tiff[4:8]))
	if ifdOff+2 > len(tiff) {
		return 0, fmt.Errorf("IFD out of range")
	}
	count := int(bo.Uint16(tiff[ifdOff : ifdOff+2]))
	for i := 0; i < count; i++ {
		e := ifdOff + 2 + i*12
		if e+12 > len(tiff) {
			break
		}
		tag := bo.Uint16(tiff[e : e+2])
		if tag == 0x0112 {
			return int(bo.Uint16(tiff[e+8 : e+10])), nil
		}
	}
	return 0, nil
}

// SetEXIFOrientation returns a copy of jpegData with an EXIF APP1 segment
// carrying the given orientation (1-8) inserted after the SOI marker.
// Used to manufacture orientation test fixtures.
func SetEXIFOrientation(jpegData []byte, orientation int) ([]byte, error) {
	if orientation < 1 || orientation > 8 {
		return nil, fmt.Errorf("orientation must be 1-8")
	}
	if len(jpegData) < 2 || jpegData[0] != 0xFF || jpegData[1] != 0xD8 {
		return nil, fmt.Errorf("not a JPEG")
	}
	// Minimal TIFF: big-endian, IFD0 with a single orientation entry.
	var tiff bytes.Buffer
	tiff.WriteString("MM")
	binary.Write(&tiff, binary.BigEndian, uint16(42))
	binary.Write(&tiff, binary.BigEndian, uint32(8))  // IFD0 offset
	binary.Write(&tiff, binary.BigEndian, uint16(1))  // entry count
	binary.Write(&tiff, binary.BigEndian, uint16(0x0112))
	binary.Write(&tiff, binary.BigEndian, uint16(3))  // SHORT
	binary.Write(&tiff, binary.BigEndian, uint32(1))  // count
	binary.Write(&tiff, binary.BigEndian, uint16(orientation))
	binary.Write(&tiff, binary.BigEndian, uint16(0))  // padding
	binary.Write(&tiff, binary.BigEndian, uint32(0))  // next IFD

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	segLen := len(payload) + 2
	var out bytes.Buffer
	out.Write(jpegData[:2])
	out.WriteByte(0xFF)
	out.WriteByte(0xE1)
	binary.Write(&out, binary.BigEndian, uint16(segLen))
	out.Write(payload)
	out.Write(jpegData[2:])
	return out.Bytes(), nil
}
