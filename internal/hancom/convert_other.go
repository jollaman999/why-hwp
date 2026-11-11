//go:build !windows

package hancom

import "context"

// Convert always fails outside Windows: Hancom Office automation is a
// Windows COM object.
func Convert(ctx context.Context, o Options) (Result, error) {
	return Result{}, ErrUnsupported
}
