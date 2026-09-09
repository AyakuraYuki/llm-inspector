// Package imagemeta 通过魔数嗅探图片格式并解析头部尺寸，不做完整解码。
// 之所以不用 image/png 等标准解码器的 DecodeConfig，是因为 webp 需要引入
// 额外依赖，且这里只需要格式与宽高两个字段，手工解析头部足够。
package imagemeta

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// Meta 是嗅探结果：格式（png/jpeg/webp）与像素宽高。
type Meta struct {
	Format string
	Width  int
	Height int
}

var (
	pngSig = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}

	ErrUnknownFormat = errors.New("无法识别的图片格式")
	errTruncated     = errors.New("图片头部数据不完整")
)

// Sniff 识别 data 的图片格式并解析尺寸。
func Sniff(data []byte) (Meta, error) {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], pngSig):
		return sniffPNG(data)
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return sniffJPEG(data)
	case len(data) >= 16 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return sniffWebP(data)
	default:
		return Meta{}, ErrUnknownFormat
	}
}

// sniffPNG 读取首个 chunk（必须是 IHDR）中的宽高。
func sniffPNG(data []byte) (Meta, error) {
	if len(data) < 24 || string(data[12:16]) != "IHDR" {
		return Meta{}, errTruncated
	}
	return Meta{
		Format: "png",
		Width:  int(binary.BigEndian.Uint32(data[16:20])),
		Height: int(binary.BigEndian.Uint32(data[20:24])),
	}, nil
}

// sniffJPEG 顺序扫描段直到 SOFn，从中读取宽高。
func sniffJPEG(data []byte) (Meta, error) {
	isSOF := func(marker byte) bool {
		// SOF0-SOF15 位于 0xC0-0xCF，其中 0xC4(DHT)、0xC8(JPG)、0xCC(DAC) 不是帧头
		return marker >= 0xC0 && marker <= 0xCF && marker != 0xC4 && marker != 0xC8 && marker != 0xCC
	}
	i := 2
	for i+2 <= len(data) {
		if data[i] != 0xFF {
			return Meta{}, errTruncated
		}
		marker := data[i+1]
		if marker == 0xFF { // 填充字节
			i++
			continue
		}
		// 无长度字段的独立标记（TEM/RSTn）
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			continue
		}
		if marker == 0xD9 || marker == 0xDA { // EOI / SOS：再往后没有 SOF 了
			break
		}
		if i+4 > len(data) {
			return Meta{}, errTruncated
		}
		segLen := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if segLen < 2 {
			return Meta{}, errTruncated
		}
		if isSOF(marker) {
			// 段内布局：长度(2) 精度(1) 高(2) 宽(2)
			if i+9 > len(data) {
				return Meta{}, errTruncated
			}
			return Meta{
				Format: "jpeg",
				Height: int(binary.BigEndian.Uint16(data[i+5 : i+7])),
				Width:  int(binary.BigEndian.Uint16(data[i+7 : i+9])),
			}, nil
		}
		i += 2 + segLen
	}
	return Meta{}, errTruncated
}

// sniffWebP 解析 RIFF 容器中第一个 chunk（VP8 / VP8L / VP8X）的尺寸。
func sniffWebP(data []byte) (Meta, error) {
	payload := data[20:] // RIFF 头(12) + chunk fourCC(4) + chunk size(4)
	switch string(data[12:16]) {
	case "VP8 ": // 有损：帧标签(3) + 同步码 9D 01 2A + 宽(2) + 高(2)，宽高各取低 14 位
		if len(payload) < 10 || payload[3] != 0x9D || payload[4] != 0x01 || payload[5] != 0x2A {
			return Meta{}, errTruncated
		}
		return Meta{
			Format: "webp",
			Width:  int(binary.LittleEndian.Uint16(payload[6:8]) & 0x3FFF),
			Height: int(binary.LittleEndian.Uint16(payload[8:10]) & 0x3FFF),
		}, nil
	case "VP8L": // 无损：签名 0x2F 之后是 LSB-first 的 14 位宽减一、14 位高减一
		if len(payload) < 5 || payload[0] != 0x2F {
			return Meta{}, errTruncated
		}
		b1, b2, b3, b4 := payload[1], payload[2], payload[3], payload[4]
		return Meta{
			Format: "webp",
			Width:  1 + (int(b2&0x3F)<<8 | int(b1)),
			Height: 1 + (int(b4&0x0F)<<10 | int(b3)<<2 | int(b2>>6)),
		}, nil
	case "VP8X": // 扩展：标志(1) + 保留(3) + 画布宽减一(3, LE) + 画布高减一(3, LE)
		if len(payload) < 10 {
			return Meta{}, errTruncated
		}
		return Meta{
			Format: "webp",
			Width:  1 + int(uint32(payload[4])|uint32(payload[5])<<8|uint32(payload[6])<<16),
			Height: 1 + int(uint32(payload[7])|uint32(payload[8])<<8|uint32(payload[9])<<16),
		}, nil
	default:
		return Meta{}, ErrUnknownFormat
	}
}
