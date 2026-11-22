//go:build !windows

package pdf

import "context"

func detectMSOfficeApps() []string {
	return nil
}

func convertWithMSOffice(ctx context.Context, inAbs, outPDF string) error {
	return ErrUnsupportedEngine
}
