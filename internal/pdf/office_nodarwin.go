//go:build !darwin

package pdf

import "context"

func detectIWorkApps() []string {
	return nil
}

func convertWithAppleIWork(ctx context.Context, inAbs, outPDF string) error {
	return ErrUnsupportedEngine
}
