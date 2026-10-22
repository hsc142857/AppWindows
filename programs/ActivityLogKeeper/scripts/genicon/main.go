// genicon은 ActivityLogKeeper 아이콘(확장 PNG, 트레이·exe용 ICO)을 그린다.
// 실행: go run ./scripts/genicon  (결과물은 저장소에 커밋한다)
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
)

var (
	teal   = color.NRGBA{0x0f, 0x7b, 0x6c, 0xff}
	white  = color.NRGBA{0xff, 0xff, 0xff, 0xff}
	line   = color.NRGBA{0xa8, 0xd8, 0xcf, 0xff}
	head   = color.NRGBA{0x2f, 0x9e, 0x8c, 0xff}
	orange = color.NRGBA{0xff, 0xa6, 0x2b, 0xff}
)

// draw는 size 크기 아이콘을 4배 슈퍼샘플링으로 그린다: 청록 둥근 사각형 + 흰 표(머리글·행) + 주황 기록 점.
func draw(size int) *image.NRGBA {
	const ss = 4
	n := size * ss
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					u := (float64(x*ss+sx) + 0.5) / float64(n)
					v := (float64(y*ss+sy) + 0.5) / float64(n)
					c, ok := sample(u, v)
					if !ok {
						continue
					}
					af := float64(c.A) / 255
					r += float64(c.R) * af
					g += float64(c.G) * af
					b += float64(c.B) * af
					a += af
				}
			}
			cnt := float64(ss * ss)
			if a == 0 {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(a / cnt * 255)})
		}
	}
	return img
}

func sample(u, v float64) (color.NRGBA, bool) {
	if !roundRect(u, v, 0.04, 0.04, 0.96, 0.96, 0.2) {
		return color.NRGBA{}, false
	}
	// 기록 표시 점 (오른쪽 아래)
	if math.Hypot(u-0.74, v-0.74) < 0.15 {
		if math.Hypot(u-0.74, v-0.74) < 0.11 {
			return orange, true
		}
		return teal, true
	}
	// 표: 머리글 띠 + 행 구분선
	if roundRect(u, v, 0.2, 0.22, 0.8, 0.78, 0.05) {
		if v < 0.34 {
			return head, true
		}
		for _, ly := range []float64{0.45, 0.56, 0.67} {
			if v > ly-0.018 && v < ly+0.018 && u > 0.26 && u < 0.74 {
				return line, true
			}
		}
		return white, true
	}
	return teal, true
}

func roundRect(u, v, x0, y0, x1, y1, r float64) bool {
	if u < x0 || u > x1 || v < y0 || v > y1 {
		return false
	}
	cx := math.Max(x0+r, math.Min(u, x1-r))
	cy := math.Max(y0+r, math.Min(v, y1-r))
	return math.Hypot(u-cx, v-cy) <= r
}

func pngBytes(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		log.Fatal(err)
	}
	return buf.Bytes()
}

// writeICO는 PNG를 담은 ICO(Windows Vista 이상 지원)를 쓴다.
func writeICO(path string, sizes []int) {
	var imgs [][]byte
	for _, s := range sizes {
		imgs = append(imgs, pngBytes(draw(s)))
	}
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		w := uint8(s)
		if s >= 256 {
			w = 0
		}
		binary.Write(&buf, binary.LittleEndian, struct {
			W, H, Colors, Reserved uint8
			Planes, BPP            uint16
			Size, Offset           uint32
		}{w, w, 0, 0, 1, 32, uint32(len(imgs[i])), uint32(offset)})
		offset += len(imgs[i])
	}
	for _, b := range imgs {
		buf.Write(b)
	}
	must(os.WriteFile(path, buf.Bytes(), 0o644))
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	extDir := filepath.Join("extension", "icons")
	must(os.MkdirAll(extDir, 0o755))
	for _, s := range []int{16, 32, 48, 128} {
		must(os.WriteFile(filepath.Join(extDir, "icon"+strconv.Itoa(s)+".png"), pngBytes(draw(s)), 0o644))
	}
	must(os.MkdirAll("app/assets", 0o755))
	must(os.WriteFile(filepath.Join("app", "assets", "icon.png"), pngBytes(draw(256)), 0o644))
	writeICO(filepath.Join("app", "assets", "icon.ico"), []int{16, 24, 32, 48, 64, 256})
	log.Println("아이콘 생성 완료")
}
