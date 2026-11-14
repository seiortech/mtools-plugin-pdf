package pdf

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

var (
	ErrEmptyInput       = errors.New("no input files provided")
	ErrInvalidRange     = errors.New("invalid page selection range")
	ErrMissingSoffice   = errors.New("libreoffice (soffice) executable not found")
	ErrConversionFailed = errors.New("office conversion failed")
)

// For test overriding
var lookPath = exec.LookPath

// MergePDFs merges input PDFs. pdfcpu stages the output before replacing it.
func MergePDFs(ctx context.Context, inFiles []string, outFile string) error {
	if len(inFiles) == 0 {
		return ErrEmptyInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outFile), 0755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	return api.MergeCreateFile(ctx, inFiles, outFile, false, model.NewStatelessConfiguration())
}

// SplitPDF splits a PDF into chunks of span pages (one page when span <= 0).
func SplitPDF(ctx context.Context, inFile string, outDir string, span int) ([]string, error) {
	if inFile == "" {
		return nil, ErrEmptyInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("create destination directory: %w", err)
	}
	if span <= 0 {
		span = 1
	}
	if err := api.SplitFile(ctx, inFile, outDir, span, model.NewStatelessConfiguration()); err != nil {
		return nil, err
	}
	baseName := strings.TrimSuffix(filepath.Base(inFile), filepath.Ext(inFile))
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return nil, err
	}
	var outputs []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), baseName+"_") && strings.HasSuffix(entry.Name(), ".pdf") {
			outputs = append(outputs, filepath.Join(outDir, entry.Name()))
		}
	}
	return outputs, nil
}

// ExtractPages collects a page selection into a PDF, preserving the old output on failure.
func ExtractPages(ctx context.Context, inFile string, outFile string, selectedPages []string) error {
	if inFile == "" {
		return ErrEmptyInput
	}
	if len(selectedPages) == 0 {
		return ErrInvalidRange
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outFile), 0755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	return api.CollectFile(ctx, inFile, outFile, selectedPages, model.NewStatelessConfiguration())
}

// ImagesToPDF replaces rather than appends to an existing output. Stage in the
// destination filesystem so errors/cancellation cannot destroy the previous PDF.
func ImagesToPDF(ctx context.Context, imgFiles []string, outFile string) error {
	if len(imgFiles) == 0 {
		return ErrEmptyInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Dir(outFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	staging, err := os.MkdirTemp(dir, ".mtools-pdf-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	tempFile := filepath.Join(staging, "output.pdf")
	if err := api.ImportImagesFile(ctx, imgFiles, tempFile, nil, model.NewStatelessConfiguration()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(tempFile, outFile)
}

// GetPageCount uses stateless configuration instead of pdfcpu's user config directory.
func GetPageCount(ctx context.Context, inFile string) (int, error) {
	if inFile == "" {
		return 0, ErrEmptyInput
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	f, err := os.Open(inFile)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return api.PageCount(ctx, f, model.NewStatelessConfiguration())
}

// FindSofficePath attempts to locate the LibreOffice soffice binary.
func FindSofficePath(customPath string) (string, error) {
	if customPath != "" {
		info, err := os.Stat(customPath)
		if err == nil && !info.IsDir() {
			return customPath, nil
		}
	}

	// Known OS locations
	switch runtime.GOOS {
	case "darwin":
		candidates := []string{
			"/Applications/LibreOffice.app/Contents/MacOS/soffice",
			filepath.Join(os.Getenv("HOME"), "Applications/LibreOffice.app/Contents/MacOS/soffice"),
		}
		for _, c := range candidates {
			if info, err := os.Stat(c); err == nil && !info.IsDir() {
				return c, nil
			}
		}
	case "windows":
		candidates := []string{
			`C:\Program Files\LibreOffice\program\soffice.exe`,
			`C:\Program Files (x86)\LibreOffice\program\soffice.exe`,
		}
		for _, c := range candidates {
			if info, err := os.Stat(c); err == nil && !info.IsDir() {
				return c, nil
			}
		}
	}

	// Search PATH
	if p, err := lookPath("soffice"); err == nil {
		return p, nil
	}
	if p, err := lookPath("libreoffice"); err == nil {
		return p, nil
	}

	return "", ErrMissingSoffice
}

// ConvertOfficeToPDF converts a document (.docx, .xlsx, .pptx, etc.) to PDF using headless LibreOffice.
func ConvertOfficeToPDF(ctx context.Context, sofficePath string, inFile string, outDir string) (string, error) {
	if inFile == "" {
		return "", ErrEmptyInput
	}
	if sofficePath == "" {
		var err error
		sofficePath, err = FindSofficePath("")
		if err != nil {
			return "", err
		}
	}

	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create destination directory: %w", err)
	}

	cmd := exec.CommandContext(ctx, sofficePath, "--headless", "--convert-to", "pdf", "--outdir", outDir, inFile)
	prepareCommand(cmd)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s (%v)", ErrConversionFailed, strings.TrimSpace(string(out)), err)
	}

	baseName := strings.TrimSuffix(filepath.Base(inFile), filepath.Ext(inFile))
	outPDF := filepath.Join(outDir, baseName+".pdf")
	if _, err := os.Stat(outPDF); err != nil {
		return "", fmt.Errorf("%w: output PDF not generated: %v", ErrConversionFailed, err)
	}

	return outPDF, nil
}
