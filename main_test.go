package main

import (
	"os"
	"strings"
	"testing"

	"github.com/seiortech/mtools-sdk/pluginapi"
)

func TestPDFSurfacesValidation(t *testing.T) {
	sRaw, err := os.ReadFile("surfaces.json")
	if err != nil {
		t.Fatalf("read surfaces.json: %v", err)
	}
	m := pluginapi.Manifest{
		Schema:       1,
		ID:           "com.mtools.pdf",
		Name:         "PDF Utility",
		Version:      "1.0.0",
		Platform:     "darwin-arm64",
		Publisher:    pluginapi.Publisher{Name: "mtools Team", PublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="},
		Capabilities: []pluginapi.Capability{pluginapi.Process, pluginapi.FilesRead, pluginapi.FilesWrite, pluginapi.UILive},
		Entry:        pluginapi.Entry{Kind: "sidecar", Path: "bin/tool", Protocol: pluginapi.Protocol},
		Tools:        []pluginapi.Tool{{ID: "pdf", Name: "PDF Utility", Description: "PDF tools", Category: "Document"}},
		Files:        map[string]pluginapi.FileInfo{"bin/tool": {SHA256: strings.Repeat("a", 64), Size: 10, Executable: true}},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest validate: %v", err)
	}
	doc, err := pluginapi.DecodeSurfaces(sRaw, m)
	if err != nil {
		t.Fatalf("DecodeSurfaces: %v", err)
	}
	if len(doc.Tools) != 1 || len(doc.Tools[0].Modes) != 5 {
		t.Fatalf("expected 1 tool with 5 modes, got %+v", doc.Tools)
	}
	if doc.Tools[0].Live == nil || !doc.Tools[0].Live.FullBleed {
		t.Fatalf("expected full_bleed=true on pdf Live UI surface, got %+v", doc.Tools[0].Live)
	}
}
