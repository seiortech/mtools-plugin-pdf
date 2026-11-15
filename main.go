package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/seiortech/mtools-plugin-pdf/internal/pdf"
	pdfui "github.com/seiortech/mtools-plugin-pdf/pdf"
	"github.com/seiortech/mtools-sdk/pluginapi"
)

type pdfServer struct{}

func resolveSofficePath(settings map[string]string) (string, error) {
	path := settings["soffice_path"]
	if path == "" {
		path = settings["helper_soffice"]
	}
	return pdf.FindSofficePath(path)
}

func (s *pdfServer) Inspect(ctx context.Context, p pluginapi.InspectParams) (pluginapi.InspectResult, error) {
	res := pluginapi.InspectResult{
		Fields:       make(map[string]pluginapi.InspectFieldUpdate),
		Metadata:     make(map[string]string),
		HelperStatus: make(map[string]pluginapi.HelperStatus),
	}

	// Inspect page counts for files
	for _, fieldID := range []string{"split_file", "extract_file"} {
		if files, ok := p.Inputs[fieldID]; ok && len(files) > 0 && files[0] != "" {
			if count, err := pdf.GetPageCount(ctx, files[0]); err == nil {
				res.Fields[fieldID] = pluginapi.InspectFieldUpdate{
					Info: fmt.Sprintf("%d pages", count),
				}
			}
		}
	}

	// Helper status for LibreOffice
	sofficePath, err := resolveSofficePath(p.Settings)
	res.HelperStatus["soffice"] = pluginapi.HelperStatus{
		Found:   err == nil,
		Path:    sofficePath,
		Message: "LibreOffice is required for office document conversion",
	}

	return res, nil
}

func (s *pdfServer) Run(ctx context.Context, p pluginapi.RunParams, emit func(pluginapi.Progress) error) (pluginapi.RunResult, error) {
	if p.Tool != "pdf" {
		return pluginapi.RunResult{}, errors.New("unsupported tool")
	}

	switch p.Action {
	case "merge":
		inFiles := p.Inputs["files"]
		if len(inFiles) == 0 {
			return pluginapi.RunResult{}, pdf.ErrEmptyInput
		}
		outFiles := p.Inputs["output_file"]
		if len(outFiles) == 0 || outFiles[0] == "" {
			return pluginapi.RunResult{}, errors.New("missing output file")
		}
		outFile := outFiles[0]
		_ = emit(pluginapi.Progress{
			JobID:   p.JobID,
			Done:    10,
			Total:   100,
			Unit:    "%",
			Message: "Merging PDF files...",
			LogLine: fmt.Sprintf("Merging %d files into %s", len(inFiles), filepath.Base(outFile)),
		})
		if err := pdf.MergePDFs(ctx, inFiles, outFile); err != nil {
			return pluginapi.RunResult{}, err
		}
		_ = emit(pluginapi.Progress{
			JobID:   p.JobID,
			Done:    100,
			Total:   100,
			Unit:    "%",
			Message: "Merge complete",
			LogLine: "Successfully wrote merged PDF",
		})
		return pluginapi.RunResult{
			JobID:   p.JobID,
			Outputs: []string{outFile},
			Summary: fmt.Sprintf("Merged %d PDF files successfully.", len(inFiles)),
		}, nil

	case "split":
		inFiles := p.Inputs["split_file"]
		if len(inFiles) == 0 || inFiles[0] == "" {
			return pluginapi.RunResult{}, pdf.ErrEmptyInput
		}
		inFile := inFiles[0]
		outDir := p.Settings["output_dir"]
		if len(p.Inputs["output_dir"]) > 0 && p.Inputs["output_dir"][0] != "" {
			outDir = p.Inputs["output_dir"][0]
		}
		if outDir == "" {
			return pluginapi.RunResult{}, errors.New("missing output directory")
		}
		span := 1
		if spans, ok := p.Inputs["split_span"]; ok && len(spans) > 0 && spans[0] != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(spans[0])); err == nil && n > 0 {
				span = n
			}
		}
		_ = emit(pluginapi.Progress{
			JobID:   p.JobID,
			Done:    20,
			Total:   100,
			Unit:    "%",
			Message: "Splitting PDF...",
			LogLine: fmt.Sprintf("Splitting %s (span=%d)", filepath.Base(inFile), span),
		})
		outputs, err := pdf.SplitPDF(ctx, inFile, outDir, span)
		if err != nil {
			return pluginapi.RunResult{}, err
		}
		_ = emit(pluginapi.Progress{
			JobID:   p.JobID,
			Done:    100,
			Total:   100,
			Unit:    "%",
			Message: "Split complete",
			LogLine: fmt.Sprintf("Generated %d files", len(outputs)),
		})
		return pluginapi.RunResult{
			JobID:   p.JobID,
			Outputs: outputs,
			Summary: fmt.Sprintf("Split into %d files.", len(outputs)),
		}, nil

	case "extract":
		inFiles := p.Inputs["extract_file"]
		if len(inFiles) == 0 || inFiles[0] == "" {
			return pluginapi.RunResult{}, pdf.ErrEmptyInput
		}
		inFile := inFiles[0]
		outFiles := p.Inputs["output_file"]
		if len(outFiles) == 0 || outFiles[0] == "" {
			return pluginapi.RunResult{}, errors.New("missing output file")
		}
		outFile := outFiles[0]
		pagesRaw := p.Inputs["pages"]
		if len(pagesRaw) == 0 || pagesRaw[0] == "" {
			return pluginapi.RunResult{}, pdf.ErrInvalidRange
		}
		pages := strings.Split(pagesRaw[0], ",")
		for i := range pages {
			pages[i] = strings.TrimSpace(pages[i])
		}
		_ = emit(pluginapi.Progress{
			JobID:   p.JobID,
			Done:    20,
			Total:   100,
			Unit:    "%",
			Message: "Extracting pages...",
			LogLine: fmt.Sprintf("Extracting pages %v from %s", pages, filepath.Base(inFile)),
		})
		if err := pdf.ExtractPages(ctx, inFile, outFile, pages); err != nil {
			return pluginapi.RunResult{}, err
		}
		_ = emit(pluginapi.Progress{
			JobID:   p.JobID,
			Done:    100,
			Total:   100,
			Unit:    "%",
			Message: "Extract complete",
			LogLine: "Successfully extracted pages",
		})
		return pluginapi.RunResult{
			JobID:   p.JobID,
			Outputs: []string{outFile},
			Summary: fmt.Sprintf("Extracted pages to %s.", filepath.Base(outFile)),
		}, nil

	case "images":
		imgFiles := p.Inputs["image_files"]
		if len(imgFiles) == 0 {
			return pluginapi.RunResult{}, pdf.ErrEmptyInput
		}
		outFiles := p.Inputs["output_file"]
		if len(outFiles) == 0 || outFiles[0] == "" {
			return pluginapi.RunResult{}, errors.New("missing output file")
		}
		outFile := outFiles[0]
		_ = emit(pluginapi.Progress{
			JobID:   p.JobID,
			Done:    20,
			Total:   100,
			Unit:    "%",
			Message: "Converting images to PDF...",
			LogLine: fmt.Sprintf("Converting %d images to %s", len(imgFiles), filepath.Base(outFile)),
		})
		if err := pdf.ImagesToPDF(ctx, imgFiles, outFile); err != nil {
			return pluginapi.RunResult{}, err
		}
		_ = emit(pluginapi.Progress{
			JobID:   p.JobID,
			Done:    100,
			Total:   100,
			Unit:    "%",
			Message: "Conversion complete",
			LogLine: "Successfully converted images to PDF",
		})
		return pluginapi.RunResult{
			JobID:   p.JobID,
			Outputs: []string{outFile},
			Summary: fmt.Sprintf("Converted %d images to PDF.", len(imgFiles)),
		}, nil

	case "office":
		officeFiles := p.Inputs["office_file"]
		if len(officeFiles) == 0 {
			return pluginapi.RunResult{}, pdf.ErrEmptyInput
		}
		outDir := p.Settings["output_dir"]
		if len(p.Inputs["output_dir"]) > 0 && p.Inputs["output_dir"][0] != "" {
			outDir = p.Inputs["output_dir"][0]
		}
		if outDir == "" {
			return pluginapi.RunResult{}, errors.New("missing output directory")
		}
		sofficePath, _ := resolveSofficePath(p.Settings)
		var outputs []string
		total := len(officeFiles)
		for i, officeFile := range officeFiles {
			if officeFile == "" {
				continue
			}
			pct := 20.0 + (float64(i)/float64(total))*70.0
			_ = emit(pluginapi.Progress{
				JobID:   p.JobID,
				Done:    pct,
				Total:   100,
				Unit:    "%",
				Message: fmt.Sprintf("Converting [%d/%d] %s...", i+1, total, filepath.Base(officeFile)),
				LogLine: fmt.Sprintf("Converting %s via LibreOffice", filepath.Base(officeFile)),
			})
			outPDF, err := pdf.ConvertOfficeToPDF(ctx, sofficePath, officeFile, outDir)
			if err != nil {
				return pluginapi.RunResult{Outputs: outputs}, err
			}
			outputs = append(outputs, outPDF)
		}
		_ = emit(pluginapi.Progress{
			JobID:   p.JobID,
			Done:    100,
			Total:   100,
			Unit:    "%",
			Message: "Office conversion complete",
			LogLine: fmt.Sprintf("Converted %d document(s) to PDF", len(outputs)),
		})
		return pluginapi.RunResult{
			JobID:   p.JobID,
			Outputs: outputs,
			Summary: fmt.Sprintf("Converted %d document(s) to PDF", len(outputs)),
		}, nil

	default:
		return pluginapi.RunResult{}, errors.New("unknown action")
	}
}

func main() {
	identity := pluginapi.InitializeResult{
		Protocol: pluginapi.Protocol,
		PluginID: "com.mtools.pdf",
		Version:  "1.0.0",
	}
	server := &pdfServer{}
	live := pdfui.NewLiveUI()
	if err := pluginapi.ServeLive(context.Background(), os.Stdin, os.Stdout, identity, server, live); err != nil {
		os.Exit(1)
	}
}
