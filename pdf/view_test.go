package pdf

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/seiortech/mtools-sdk/job"
	plugin "github.com/seiortech/mtools-sdk/settings"
)

func TestPDFWorkspaceTabsAndEmptyStates(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mtools-pdf-view-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	settingsFile := filepath.Join(tempDir, "settings.json")
	store, _ := plugin.NewJSONSettingsStore(settingsFile, PluginSettingsSchema())
	q := job.NewQueue(1, nil)
	p := NewPlugin(tempDir, store, q)

	tt := ui.NewTester(p.View, 1180, 800)
	tt.Frame()

	// Initial Merge tab
	if !tt.HasText("PDF Workbench") {
		t.Errorf("expected 'PDF Workbench' title")
	}
	if !tt.HasText("Merge PDF Files") {
		t.Errorf("expected 'Merge PDF Files' initial header")
	}
	if !tt.HasText("+ Add PDF Files...") {
		t.Errorf("expected '+ Add PDF Files...' button")
	}

	// Switch to Split tab
	if err := tt.Click("SPLIT"); err != nil {
		t.Fatalf("Click 'SPLIT' failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Split PDF File") {
		t.Errorf("expected 'Split PDF File' header")
	}
	if !tt.HasText("Choose PDF File...") {
		t.Errorf("expected 'Choose PDF File...' button")
	}

	// Switch to Images tab
	if err := tt.Click("IMAGE TO PDF"); err != nil {
		t.Fatalf("Click 'IMAGE TO PDF' failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Convert Images to PDF") {
		t.Errorf("expected 'Convert Images to PDF' header")
	}
	if !tt.HasText("+ Add Images...") {
		t.Errorf("expected '+ Add Images...' button")
	}

	// Switch to Office tab
	if err := tt.Click("OFFICE TO PDF"); err != nil {
		t.Fatalf("Click 'OFFICE TO PDF' failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Convert Office Documents to PDF") {
		t.Errorf("expected 'Convert Office Documents to PDF' header")
	}

	// Switch back to Merge tab
	if err := tt.Click("MERGE"); err != nil {
		t.Fatalf("Click 'MERGE' failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Merge PDF Files") {
		t.Errorf("expected return to 'Merge PDF Files'")
	}
}

func TestPDFWorkspaceJobCard(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mtools-pdf-view-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	settingsFile := filepath.Join(tempDir, "settings.json")
	store, _ := plugin.NewJSONSettingsStore(settingsFile, PluginSettingsSchema())
	q := job.NewQueue(1, nil)
	p := NewPlugin(tempDir, store, q)

	doneCh := make(chan struct{})
	jobID := q.Enqueue("Merge PDFs", job.Func(func(ctx context.Context, progress func(job.Progress)) (job.Result, error) {
		progress(job.Progress{Done: 50, Message: "Merging pages 1-10..."})
		close(doneCh)
		return job.Result{
			Outputs: []string{filepath.Join(tempDir, "test.pdf")},
			Summary: "Merged 2 files into test.pdf",
		}, nil
	}))
	p.state.ActiveJobID = jobID

	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("job timed out")
	}

	tt := ui.NewTester(p.View, 1180, 800)
	tt.Frame()

	if !tt.HasText("Task Activity") {
		t.Errorf("expected 'Task Activity' card")
	}
	if !tt.HasText("Reveal in Folder") {
		t.Errorf("expected 'Reveal in Folder' button on success")
	}
}

func TestPDFWorkspaceResponsiveLayout(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mtools-pdf-view-resp-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	settingsFile := filepath.Join(tempDir, "settings.json")
	store, _ := plugin.NewJSONSettingsStore(settingsFile, PluginSettingsSchema())
	q := job.NewQueue(1, nil)
	p := NewPlugin(tempDir, store, q)

	// Render on narrow 720x520 minimum window size
	tt := ui.NewTester(p.View, 720, 520)
	tt.Frame()

	if !tt.HasText("PDF Workbench") {
		t.Errorf("expected 'PDF Workbench' title on narrow window")
	}
	if !tt.HasText("+ Add PDF Files...") {
		t.Errorf("expected '+ Add PDF Files...' button")
	}

	// Switch to Image to PDF tab
	if err := tt.Click("IMAGE TO PDF"); err != nil {
		t.Fatalf("Click 'IMAGE TO PDF' failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("+ Add Images...") {
		t.Errorf("expected '+ Add Images...' button in narrow view")
	}

	// Switch to Office to PDF tab
	if err := tt.Click("OFFICE TO PDF"); err != nil {
		t.Fatalf("Click 'OFFICE TO PDF' failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Convert Office Documents to PDF") {
		t.Errorf("expected 'Convert Office Documents to PDF' header in narrow view")
	}
}

func TestPDFWorkspaceHistoryMinimizing(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mtools-pdf-view-hist-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	settingsFile := filepath.Join(tempDir, "settings.json")
	store, _ := plugin.NewJSONSettingsStore(settingsFile, PluginSettingsSchema())
	q := job.NewQueue(1, nil)
	p := NewPlugin(tempDir, store, q)

	// Wide viewport: history open by default
	tt := ui.NewTester(p.View, 1200, 800)
	tt.Frame()

	if !tt.HasText("PDF History") {
		t.Errorf("expected 'PDF History' visible on wide viewport")
	}
	if !tt.HasText("Hide »") {
		t.Errorf("expected 'Hide »' button in history panel header")
	}

	// Minimize history via "Hide »"
	if err := tt.Click("Hide »"); err != nil {
		t.Fatalf("Click 'Hide »' failed: %v", err)
	}
	tt.Frame()

	if tt.HasText("PDF History") {
		t.Errorf("expected 'PDF History' to be hidden after clicking 'Hide »'")
	}
	if !tt.HasText("History (0)") {
		t.Errorf("expected 'History (0)' expand button in workspace header")
	}

	// Re-expand history via "History (0)" button
	if err := tt.Click("History (0)"); err != nil {
		t.Fatalf("Click 'History (0)' failed: %v", err)
	}
	tt.Frame()

	if !tt.HasText("PDF History") {
		t.Errorf("expected 'PDF History' restored after expand")
	}

	// Minimize history via "Hide History" button in workspace header
	if err := tt.Click("Hide History"); err != nil {
		t.Fatalf("Click 'Hide History' failed: %v", err)
	}
	tt.Frame()

	if tt.HasText("PDF History") {
		t.Errorf("expected 'PDF History' hidden after clicking 'Hide History'")
	}
}

func TestPDFWorkspaceHistoryResizable(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mtools-pdf-view-resize-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	settingsFile := filepath.Join(tempDir, "settings.json")
	store, _ := plugin.NewJSONSettingsStore(settingsFile, PluginSettingsSchema())
	q := job.NewQueue(1, nil)
	p := NewPlugin(tempDir, store, q)

	tt := ui.NewTester(p.View, 1200, 800)
	tt.Frame()

	rect, found := tt.Find("Resize History Panel")
	if !found {
		t.Fatalf("expected 'Resize History Panel' handle on wide viewport")
	}

	// Drag the divider left by 40 DIP: the inspector widens to 360.
	tt.Press(rect.X+rect.W/2, rect.Y+rect.H/2)
	tt.Move(rect.X+rect.W/2-40, rect.Y+rect.H/2)
	tt.Frame()
	tt.Release(rect.X+rect.W/2-40, rect.Y+rect.H/2)
	tt.Frame()

	if p.state.HistoryWidth != 360 {
		t.Fatalf("expected HistoryWidth 360 after drag, got %d", p.state.HistoryWidth)
	}

	// Double click the divider to reset to the default 320.
	rect, _ = tt.Find("Resize History Panel")
	tt.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tt.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tt.Frame()

	if p.state.HistoryWidth != 320 {
		t.Fatalf("expected HistoryWidth reset to 320, got %d", p.state.HistoryWidth)
	}
}
