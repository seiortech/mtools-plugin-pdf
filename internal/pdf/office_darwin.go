//go:build darwin

package pdf

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Compile-time constant AppleScripts; file paths are passed strictly via argv
// (`on run argv`) and never interpolated into script source.
const (
	pagesExportScript = `on run argv
	set inFile to POSIX file (item 1 of argv)
	set outFile to POSIX file (item 2 of argv)
	tell application "Pages"
		set wasRunning to running
		set doc to open inFile
		try
			export doc to outFile as PDF
		on error errMsg number errNum
			try
				close doc saving no
			end try
			if not wasRunning then quit
			error errMsg number errNum
		end try
		close doc saving no
		if not wasRunning then quit
	end tell
end run`

	numbersExportScript = `on run argv
	set inFile to POSIX file (item 1 of argv)
	set outFile to POSIX file (item 2 of argv)
	tell application "Numbers"
		set wasRunning to running
		set doc to open inFile
		try
			export doc to outFile as PDF
		on error errMsg number errNum
			try
				close doc saving no
			end try
			if not wasRunning then quit
			error errMsg number errNum
		end try
		close doc saving no
		if not wasRunning then quit
	end tell
end run`

	keynoteExportScript = `on run argv
	set inFile to POSIX file (item 1 of argv)
	set outFile to POSIX file (item 2 of argv)
	tell application "Keynote"
		set wasRunning to running
		set doc to open inFile
		try
			export doc to outFile as PDF
		on error errMsg number errNum
			try
				close doc saving no
			end try
			if not wasRunning then quit
			error errMsg number errNum
		end try
		close doc saving no
		if not wasRunning then quit
	end tell
end run`
)

// runOsascript is injectable in tests to verify argv safety and error mapping.
var runOsascript = func(ctx context.Context, argv ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", argv...)
	prepareCommand(cmd)
	return cmd.CombinedOutput()
}

func detectIWorkApps() []string {
	apps := []string{"Pages", "Numbers", "Keynote"}
	var found []string
	home := os.Getenv("HOME")
	for _, app := range apps {
		bundle := app + ".app"
		candidates := []string{filepath.Join("/Applications", bundle)}
		if home != "" {
			candidates = append(candidates, filepath.Join(home, "Applications", bundle))
		}
		for _, c := range candidates {
			if info, err := os.Stat(c); err == nil && info.IsDir() {
				found = append(found, app)
				break
			}
		}
	}
	return found
}

func iWorkScriptForApp(app string) (string, bool) {
	switch app {
	case "Pages":
		return pagesExportScript, true
	case "Numbers":
		return numbersExportScript, true
	case "Keynote":
		return keynoteExportScript, true
	default:
		return "", false
	}
}

func convertWithAppleIWork(ctx context.Context, inAbs, outPDF string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(inAbs))
	app := iWorkAppForExt(ext)
	script, ok := iWorkScriptForApp(app)
	if !ok {
		return fmt.Errorf("%w: Apple iWork cannot convert %s", ErrUnsupportedFormat, ext)
	}

	out, err := runOsascript(ctx, "-e", script, "--", inAbs, outPDF)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg := strings.TrimSpace(string(out))
		lower := strings.ToLower(msg)
		if strings.Contains(msg, "-1743") || strings.Contains(lower, "not authorized to send apple events") {
			return fmt.Errorf("%w (%s): %s", ErrAutomationDenied, app, msg)
		}
		return fmt.Errorf("%w: Apple %s export failed: %s (%v)", ErrConversionFailed, app, msg, err)
	}
	return nil
}
