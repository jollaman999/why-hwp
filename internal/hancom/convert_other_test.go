//go:build !windows

package hancom

import (
	"context"
	"errors"
	"testing"
)

func TestConvertNeedsWindows(t *testing.T) {
	_, err := Convert(context.Background(), Options{In: "a.hwpx", Out: "a.hwp", To: HWP})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
	if err.Error() != "convert needs Windows with Hancom Office" {
		t.Errorf("message = %q", err)
	}
}
