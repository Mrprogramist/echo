package media

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/png"
	"os"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/Mrprogramist/echo/internal/config"
)

var (
	ErrImageTooLarge = errors.New("image pixel count exceeds safe budget")
	ErrInvalidImage  = errors.New("unsupported or corrupt image format")
)

// ProcessSquareImage декодирует картинку, кадрирует в центр квадрата, масштабирует под targetSize и сохраняет как PNG
func ProcessSquareImage(data []byte, targetSize int) ([]byte, error) {
	// 1. Проверяем конфиг картинки без полного декодирования в память (защита от бомб)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalidImage
	}

	// Защита от Pixel Flood (атака разжатия памяти)
	if int64(cfg.Width)*int64(cfg.Height) > config.MaxImagePixels {
		return nil, ErrImageTooLarge
	}

	// 2. Полное декодирование
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// 3. Кадрирование по центру до квадрата (Crop to center square)
	squareSize := w
	if h < squareSize {
		squareSize = h
	}

	x0 := bounds.Min.X + (w-squareSize)/2
	y0 := bounds.Min.Y + (h-squareSize)/2
	cropRect := image.Rect(x0, y0, x0+squareSize, y0+squareSize)

	cropped := image.NewRGBA(image.Rect(0, 0, squareSize, squareSize))
	draw.Draw(cropped, cropped.Bounds(), src, cropRect.Min, draw.Src)

	// 4. Качественный ресайз (Catmull-Rom фильтр) под targetSize
	dst := image.NewRGBA(image.Rect(0, 0, targetSize, targetSize))
	draw.CatmullRom.Scale(dst, dst.Bounds(), cropped, cropped.Bounds(), draw.Over, nil)

	// 5. Кодируем результат в PNG
	var out bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&out, dst); err != nil {
		return nil, fmt.Errorf("failed to encode png: %w", err)
	}

	return out.Bytes(), nil
}

// RemoveFileIfExists безопасно удаляет старый файл с диска
func RemoveFileIfExists(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}
