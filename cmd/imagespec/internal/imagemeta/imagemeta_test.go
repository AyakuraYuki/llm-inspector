package imagemeta

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSniffPNG(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 320, 176))))

	meta, err := Sniff(buf.Bytes())
	require.NoError(t, err)
	assert.Equal(t, Meta{Format: "png", Width: 320, Height: 176}, meta)
}

func TestSniffJPEG(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 640, 480)), nil))

	meta, err := Sniff(buf.Bytes())
	require.NoError(t, err)
	assert.Equal(t, Meta{Format: "jpeg", Width: 640, Height: 480}, meta)
}

// riffHeader 拼一个最小的 RIFF/WEBP 容器头（chunk size 字段不参与解析，填 0 即可）。
func riffHeader(fourcc string, payload []byte) []byte {
	buf := []byte("RIFF\x00\x00\x00\x00WEBP")
	buf = append(buf, fourcc...)
	buf = append(buf, 0, 0, 0, 0) // chunk size
	return append(buf, payload...)
}

func TestSniffWebPLossy(t *testing.T) {
	// VP8 有损：帧标签(3) + 同步码 9D 01 2A + 宽(2, LE) + 高(2, LE)
	payload := []byte{0x00, 0x00, 0x00, 0x9D, 0x01, 0x2A}
	payload = binary.LittleEndian.AppendUint16(payload, 1024)
	payload = binary.LittleEndian.AppendUint16(payload, 768)

	meta, err := Sniff(riffHeader("VP8 ", payload))
	require.NoError(t, err)
	assert.Equal(t, Meta{Format: "webp", Width: 1024, Height: 768}, meta)
}

func TestSniffWebPLossless(t *testing.T) {
	// VP8L：签名 0x2F + LSB-first 的 14 位宽减一、14 位高减一
	w, h := 1536, 864
	bits := uint32(w-1) | uint32(h-1)<<14
	payload := []byte{0x2F, byte(bits), byte(bits >> 8), byte(bits >> 16), byte(bits >> 24)}

	meta, err := Sniff(riffHeader("VP8L", payload))
	require.NoError(t, err)
	assert.Equal(t, Meta{Format: "webp", Width: w, Height: h}, meta)
}

func TestSniffWebPExtended(t *testing.T) {
	// VP8X：标志(1) + 保留(3) + 画布宽减一(3, LE) + 画布高减一(3, LE)
	w, h := 3840, 2160
	payload := []byte{
		0x00, 0x00, 0x00, 0x00,
		byte(w - 1), byte((w - 1) >> 8), byte((w - 1) >> 16),
		byte(h - 1), byte((h - 1) >> 8), byte((h - 1) >> 16),
	}

	meta, err := Sniff(riffHeader("VP8X", payload))
	require.NoError(t, err)
	assert.Equal(t, Meta{Format: "webp", Width: w, Height: h}, meta)
}

func TestSniffUnknown(t *testing.T) {
	_, err := Sniff([]byte("GIF89a not supported"))
	assert.ErrorIs(t, err, ErrUnknownFormat)

	_, err = Sniff(nil)
	assert.ErrorIs(t, err, ErrUnknownFormat)
}

func TestSniffTruncated(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8))))

	_, err := Sniff(buf.Bytes()[:16])
	assert.Error(t, err)
}
