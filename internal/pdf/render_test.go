package pdf

import (
	"context"
	"image"
	"image/color"
	"path/filepath"
	"testing"
)

func TestQuantizeRenderScale(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0, 1.0},
		{0.1, 0.25},
		{0.62, 0.5},
		{0.63, 0.75},
		{1.0, 1.0},
		{2.38, 2.5},
		{4.5, 3.0},
	}
	for _, tc := range cases {
		if got := QuantizeRenderScale(tc.in); got != tc.want {
			t.Fatalf("QuantizeRenderScale(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestPageBitmapCache(t *testing.T) {
	c := NewPageBitmapCache(2)
	img1 := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img2 := image.NewRGBA(image.Rect(0, 0, 20, 20))
	img3 := image.NewRGBA(image.Rect(0, 0, 30, 30))

	c.Put("/a.pdf", 1, 1.0, img1)
	c.Put("/a.pdf", 2, 1.0, img2)
	if _, ok := c.Get("/a.pdf", 1, 1.0); !ok {
		t.Fatal("expected page 1 hit")
	}
	c.Put("/b.pdf", 1, 1.0, img3)
	if _, ok := c.Get("/a.pdf", 2, 1.0); ok {
		t.Fatal("expected page 2 to be evicted as LRU")
	}
	c.ClearForPath("/a.pdf")
	if _, ok := c.Get("/a.pdf", 1, 1.0); ok {
		t.Fatal("expected /a.pdf cleared")
	}
	if _, ok := c.Get("/b.pdf", 1, 1.0); !ok {
		t.Fatal("expected /b.pdf retained")
	}
}

func TestFallbackAndDefaultRenderer(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "sample.png")
	createTestPNG(t, imgPath, color.RGBA{R: 74, G: 93, B: 69, A: 255})

	pdfPath := filepath.Join(dir, "sample.pdf")
	if err := ImagesToPDF(context.Background(), []string{imgPath}, pdfPath); err != nil {
		t.Fatalf("ImagesToPDF: %v", err)
	}

	fb := &FallbackRenderer{}
	cnt, err := fb.PageCount(context.Background(), pdfPath)
	if err != nil || cnt != 1 {
		t.Fatalf("FallbackRenderer.PageCount = (%d, %v), want (1, nil)", cnt, err)
	}
	w, h, err := fb.PageDimensions(context.Background(), pdfPath, 1)
	if err != nil || w <= 0 || h <= 0 {
		t.Fatalf("FallbackRenderer.PageDimensions = (%v, %v, %v)", w, h, err)
	}
	if _, err := fb.RenderPage(context.Background(), pdfPath, 1, 1.0); err != nil {
		t.Fatalf("FallbackRenderer.RenderPage: %v", err)
	}

	dr := DefaultRenderer()
	configureTestRenderer(t, dr)
	img, err := dr.RenderPage(context.Background(), pdfPath, 1, 1.0)
	if err != nil || img.Bounds().Dx() <= 0 || img.Bounds().Dy() <= 0 {
		t.Fatalf("DefaultRenderer.RenderPage = (%v, %v)", img, err)
	}
}
