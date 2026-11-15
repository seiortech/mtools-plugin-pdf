//go:build !darwin

package pdf

func defaultPlatformRenderer() PageRenderer {
	return NewPDFiumRenderer()
}
