//go:build windows

package pdf

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows/registry"
)

const (
	// msoAutomationSecurityForceDisable disables all macros when opening
	// untrusted Office documents via COM automation.
	msoAutomationSecurityForceDisable = 3

	wdExportFormatPDF = 17
	xlTypePDF         = 0
	ppSaveAsPDF       = 32
	msoFalse          = 0
	msoTrue           = -1
)

func putProp(disp *ole.IDispatch, name string, params ...any) error {
	v, err := oleutil.PutProperty(disp, name, params...)
	if v != nil {
		_ = v.Clear()
	}
	return err
}

func callMethodDiscard(disp *ole.IDispatch, name string, params ...any) error {
	v, err := oleutil.CallMethod(disp, name, params...)
	if v != nil {
		_ = v.Clear()
	}
	return err
}

func detectMSOfficeApps() []string {
	type probe struct {
		name   string
		progID string
	}
	probes := []probe{
		{name: "Word", progID: `Word.Application\CLSID`},
		{name: "Excel", progID: `Excel.Application\CLSID`},
		{name: "PowerPoint", progID: `PowerPoint.Application\CLSID`},
	}
	var found []string
	for _, p := range probes {
		k, err := registry.OpenKey(registry.CLASSES_ROOT, p.progID, registry.QUERY_VALUE)
		if err == nil {
			_ = k.Close()
			found = append(found, p.name)
		}
	}
	return found
}

func convertWithMSOffice(ctx context.Context, inAbs, outPDF string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(inAbs))
	appName := msOfficeAppForExt(ext)
	if appName == "" {
		return fmt.Errorf("%w: Microsoft Office cannot convert %s", ErrUnsupportedFormat, ext)
	}

	errCh := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
			errCh <- fmt.Errorf("%w: COM init failed: %v", ErrConversionFailed, err)
			return
		}
		defer ole.CoUninitialize()

		var err error
		switch appName {
		case "Word":
			err = convertWithWordCOM(ctx, inAbs, outPDF)
		case "Excel":
			err = convertWithExcelCOM(ctx, inAbs, outPDF)
		case "PowerPoint":
			err = convertWithPowerPointCOM(ctx, inAbs, outPDF)
		default:
			err = fmt.Errorf("%w: %s", ErrUnsupportedFormat, ext)
		}
		errCh <- err
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func convertWithWordCOM(ctx context.Context, inAbs, outPDF string) error {
	unknown, err := oleutil.CreateObject("Word.Application")
	if err != nil {
		return fmt.Errorf("%w: create Word.Application: %v", ErrConversionFailed, err)
	}
	defer unknown.Release()

	word, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return fmt.Errorf("%w: query Word IDispatch: %v", ErrConversionFailed, err)
	}
	defer word.Release()
	defer func() {
		_ = callMethodDiscard(word, "Quit", false)
	}()

	_ = putProp(word, "Visible", false)
	_ = putProp(word, "DisplayAlerts", 0)
	_ = putProp(word, "AutomationSecurity", msoAutomationSecurityForceDisable)

	if err := ctx.Err(); err != nil {
		return err
	}

	docsVar, err := oleutil.GetProperty(word, "Documents")
	if err != nil {
		return fmt.Errorf("%w: Word Documents collection: %v", ErrConversionFailed, err)
	}
	docs := docsVar.ToIDispatch()
	defer docs.Release()

	// Documents.Open(FileName, ConfirmConversions=false, ReadOnly=true)
	docVar, err := oleutil.CallMethod(docs, "Open", inAbs, false, true)
	if err != nil {
		return fmt.Errorf("%w: Word failed to open document: %v", ErrConversionFailed, err)
	}
	doc := docVar.ToIDispatch()
	defer doc.Release()
	defer func() {
		_ = callMethodDiscard(doc, "Close", false)
	}()

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := callMethodDiscard(doc, "ExportAsFixedFormat", outPDF, wdExportFormatPDF); err != nil {
		return fmt.Errorf("%w: Word PDF export failed: %v", ErrConversionFailed, err)
	}
	return nil
}

func convertWithExcelCOM(ctx context.Context, inAbs, outPDF string) error {
	unknown, err := oleutil.CreateObject("Excel.Application")
	if err != nil {
		return fmt.Errorf("%w: create Excel.Application: %v", ErrConversionFailed, err)
	}
	defer unknown.Release()

	excel, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return fmt.Errorf("%w: query Excel IDispatch: %v", ErrConversionFailed, err)
	}
	defer excel.Release()
	defer func() {
		_ = callMethodDiscard(excel, "Quit")
	}()

	_ = putProp(excel, "Visible", false)
	_ = putProp(excel, "DisplayAlerts", false)
	_ = putProp(excel, "AutomationSecurity", msoAutomationSecurityForceDisable)

	if err := ctx.Err(); err != nil {
		return err
	}

	workbooksVar, err := oleutil.GetProperty(excel, "Workbooks")
	if err != nil {
		return fmt.Errorf("%w: Excel Workbooks collection: %v", ErrConversionFailed, err)
	}
	workbooks := workbooksVar.ToIDispatch()
	defer workbooks.Release()

	// Workbooks.Open(Filename, UpdateLinks=0, ReadOnly=true)
	wbVar, err := oleutil.CallMethod(workbooks, "Open", inAbs, 0, true)
	if err != nil {
		return fmt.Errorf("%w: Excel failed to open workbook: %v", ErrConversionFailed, err)
	}
	wb := wbVar.ToIDispatch()
	defer wb.Release()
	defer func() {
		_ = callMethodDiscard(wb, "Close", false)
	}()

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := callMethodDiscard(wb, "ExportAsFixedFormat", xlTypePDF, outPDF); err != nil {
		return fmt.Errorf("%w: Excel PDF export failed: %v", ErrConversionFailed, err)
	}
	return nil
}

func convertWithPowerPointCOM(ctx context.Context, inAbs, outPDF string) error {
	unknown, err := oleutil.CreateObject("PowerPoint.Application")
	if err != nil {
		return fmt.Errorf("%w: create PowerPoint.Application: %v", ErrConversionFailed, err)
	}
	defer unknown.Release()

	ppt, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return fmt.Errorf("%w: query PowerPoint IDispatch: %v", ErrConversionFailed, err)
	}
	defer ppt.Release()
	defer func() {
		_ = callMethodDiscard(ppt, "Quit")
	}()

	_ = putProp(ppt, "AutomationSecurity", msoAutomationSecurityForceDisable)

	if err := ctx.Err(); err != nil {
		return err
	}

	presVar, err := oleutil.GetProperty(ppt, "Presentations")
	if err != nil {
		return fmt.Errorf("%w: PowerPoint Presentations collection: %v", ErrConversionFailed, err)
	}
	presentations := presVar.ToIDispatch()
	defer presentations.Release()

	// Presentations.Open(FileName, ReadOnly=msoTrue, Untitled=msoFalse, WithWindow=msoFalse)
	docVar, err := oleutil.CallMethod(presentations, "Open", inAbs, msoTrue, msoFalse, msoFalse)
	if err != nil {
		return fmt.Errorf("%w: PowerPoint failed to open presentation: %v", ErrConversionFailed, err)
	}
	doc := docVar.ToIDispatch()
	defer doc.Release()
	defer func() {
		_ = callMethodDiscard(doc, "Close")
	}()

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := callMethodDiscard(doc, "SaveAs", outPDF, ppSaveAsPDF); err != nil {
		return fmt.Errorf("%w: PowerPoint PDF export failed: %v", ErrConversionFailed, err)
	}
	return nil
}
