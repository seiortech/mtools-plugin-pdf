package pdf

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/seiortech/mtools-sdk/job"
)

// OpType represents the PDF processing mode.
type OpType string

const (
	OpMerge   OpType = "merge"
	OpSplit   OpType = "split"
	OpExtract OpType = "extract"
	OpImages  OpType = "images"
	OpOffice  OpType = "office"
)

// PDFJob configures and executes a PDF processing operation.
type PDFJob struct {
	Op           OpType
	InputFiles   []string
	OutputFile   string
	OutputDir    string
	PageSpan     int
	PageRanges   []string
	SofficePath  string
	OfficeEngine OfficeEngine
}

// Run executes the PDF operation as a background job.
func (j PDFJob) Run(ctx context.Context, progress func(job.Progress)) (job.Result, error) {
	if progress == nil {
		progress = func(job.Progress) {}
	}

	progress(job.Progress{Done: 10, Total: 100, Unit: "%", Message: "Starting " + string(j.Op) + "..."})

	select {
	case <-ctx.Done():
		return job.Result{}, job.ErrCanceled
	default:
	}

	switch j.Op {
	case OpMerge:
		if len(j.InputFiles) == 0 {
			return job.Result{}, ErrEmptyInput
		}
		progress(job.Progress{Done: 30, Total: 100, Unit: "%", Message: fmt.Sprintf("Merging %d files...", len(j.InputFiles))})
		if err := MergePDFs(ctx, j.InputFiles, j.OutputFile); err != nil {
			return job.Result{}, err
		}
		progress(job.Progress{Done: 100, Total: 100, Unit: "%", Message: "Merge completed"})
		return job.Result{
			Outputs: []string{j.OutputFile},
			Summary: fmt.Sprintf("Successfully merged %d files into %s", len(j.InputFiles), filepath.Base(j.OutputFile)),
		}, nil

	case OpSplit:
		if len(j.InputFiles) == 0 || j.InputFiles[0] == "" {
			return job.Result{}, ErrEmptyInput
		}
		progress(job.Progress{Done: 30, Total: 100, Unit: "%", Message: "Splitting PDF into pages..."})
		outFiles, err := SplitPDF(ctx, j.InputFiles[0], j.OutputDir, j.PageSpan)
		if err != nil {
			return job.Result{}, err
		}
		progress(job.Progress{Done: 100, Total: 100, Unit: "%", Message: "Split completed"})
		return job.Result{
			Outputs: outFiles,
			Summary: fmt.Sprintf("Split %s into %d files", filepath.Base(j.InputFiles[0]), len(outFiles)),
		}, nil

	case OpExtract:
		if len(j.InputFiles) == 0 || j.InputFiles[0] == "" {
			return job.Result{}, ErrEmptyInput
		}
		progress(job.Progress{Done: 30, Total: 100, Unit: "%", Message: "Extracting pages..."})
		if err := ExtractPages(ctx, j.InputFiles[0], j.OutputFile, j.PageRanges); err != nil {
			return job.Result{}, err
		}
		progress(job.Progress{Done: 100, Total: 100, Unit: "%", Message: "Extract completed"})
		return job.Result{
			Outputs: []string{j.OutputFile},
			Summary: fmt.Sprintf("Extracted pages (%s) from %s", strings.Join(j.PageRanges, ", "), filepath.Base(j.InputFiles[0])),
		}, nil

	case OpImages:
		if len(j.InputFiles) == 0 {
			return job.Result{}, ErrEmptyInput
		}
		progress(job.Progress{Done: 30, Total: 100, Unit: "%", Message: fmt.Sprintf("Converting %d images to PDF...", len(j.InputFiles))})
		if err := ImagesToPDF(ctx, j.InputFiles, j.OutputFile); err != nil {
			return job.Result{}, err
		}
		progress(job.Progress{Done: 100, Total: 100, Unit: "%", Message: "Conversion completed"})
		return job.Result{
			Outputs: []string{j.OutputFile},
			Summary: fmt.Sprintf("Converted %d images to %s", len(j.InputFiles), filepath.Base(j.OutputFile)),
		}, nil

	case OpOffice:
		if len(j.InputFiles) == 0 {
			return job.Result{}, ErrEmptyInput
		}
		var outputs []string
		total := len(j.InputFiles)
		for i, f := range j.InputFiles {
			select {
			case <-ctx.Done():
				return job.Result{}, job.ErrCanceled
			default:
			}

			pct := 20.0 + (float64(i)/float64(total))*70.0
			progress(job.Progress{
				Done:    pct,
				Total:   100,
				Unit:    "%",
				Message: fmt.Sprintf("Converting [%d/%d] %s...", i+1, total, filepath.Base(f)),
			})

			outPDF, err := ConvertOfficeToPDFWithSpec(ctx, OfficeConvertSpec{
				Engine:      j.OfficeEngine,
				SofficePath: j.SofficePath,
				InputFile:   f,
				OutputDir:   j.OutputDir,
			})
			if err != nil {
				return job.Result{Outputs: outputs}, err
			}
			outputs = append(outputs, outPDF)
		}

		progress(job.Progress{Done: 100, Total: 100, Unit: "%", Message: "Office conversion completed"})
		return job.Result{
			Outputs: outputs,
			Summary: fmt.Sprintf("Converted %d document(s) to PDF", len(outputs)),
		}, nil

	default:
		return job.Result{}, fmt.Errorf("unknown operation type: %s", j.Op)
	}
}
