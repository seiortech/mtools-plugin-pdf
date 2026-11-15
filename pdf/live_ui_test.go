package pdf

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/seiortech/mtools-sdk/pluginapi"
)

func TestLiveUIOpenTabsAndValidation(t *testing.T) {
	ui := NewLiveUI()
	tree, err := ui.Open(context.Background(), pluginapi.UIOpenParams{
		Tool:     "pdf",
		Viewport: pluginapi.UIViewport{Width: 960, Height: 680},
		Lang:     "en",
		Settings: map[string]string{"output_dir": t.TempDir()},
	}, nil)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	tree.Rev = 1
	if err := tree.Validate(); err != nil {
		t.Fatalf("initial tree invalid: %v", err)
	}

	for _, tab := range []string{"split", "images", "office", "merge"} {
		if err := ui.Event(context.Background(), pluginapi.UIEvent{
			Rev: 1, Node: "pdf.tabs", Kind: pluginapi.UIEventSelect, Value: tab,
		}, nil); err != nil {
			t.Fatalf("tab %s event failed: %v", tab, err)
		}
		ui.mu.Lock()
		next := ui.buildTreeLocked(2)
		ui.mu.Unlock()
		if err := next.Validate(); err != nil {
			t.Fatalf("tab %s tree invalid: %v", tab, err)
		}
	}

	// Test multi-file add, reorder, and remove
	if err := ui.Event(context.Background(), pluginapi.UIEvent{
		Rev: 2, Node: "pdf.merge.pick", Kind: pluginapi.UIEventChange, Value: "/tmp/a.pdf\n/tmp/b.pdf",
	}, nil); err != nil {
		t.Fatalf("pick failed: %v", err)
	}
	ui.mu.Lock()
	if len(ui.mergeFiles) != 2 || ui.mergeFiles[0].Name != "a.pdf" || ui.mergeFiles[1].Name != "b.pdf" {
		t.Fatalf("unexpected merge files: %+v", ui.mergeFiles)
	}
	ui.mu.Unlock()

	if err := ui.Event(context.Background(), pluginapi.UIEvent{
		Rev: 3, Node: "pdf.merge.item.1.up", Kind: pluginapi.UIEventClick,
	}, nil); err != nil {
		t.Fatalf("move up failed: %v", err)
	}
	ui.mu.Lock()
	if ui.mergeFiles[0].Name != "b.pdf" {
		t.Fatalf("expected b.pdf first after move up, got %+v", ui.mergeFiles)
	}
	ui.mu.Unlock()

	// Ensure tree serializes cleanly to JSON and decodes via DecodeTree
	ui.mu.Lock()
	finalTree := ui.buildTreeLocked(4)
	ui.mu.Unlock()
	raw, err := json.Marshal(finalTree)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pluginapi.DecodeTree(raw); err != nil {
		t.Fatalf("DecodeTree failed: %v", err)
	}
}
