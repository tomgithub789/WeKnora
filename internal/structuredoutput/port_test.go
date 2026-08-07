package structuredoutput

import (
	"context"
	"errors"
	"testing"
	"time"
)

type testAcceptor struct {
	mode    Mode
	result  Result
	err     error
	timeout time.Duration
}

func (a testAcceptor) Mode() Mode { return a.mode }
func (a testAcceptor) Accept(context.Context, Request) (Result, error) {
	return a.result, a.err
}
func (a testAcceptor) ValidateResponse(context.Context, Response) error { return a.err }
func (a testAcceptor) CallTimeout() time.Duration                       { return a.timeout }

func TestDefaultPortIsOffAndPassesThrough(t *testing.T) {
	if Enabled() {
		t.Fatal("default structured-output port must be disabled")
	}
	result, err := Accept(context.Background(), Request{Raw: "original"})
	if err != nil || result.JSON != "original" {
		t.Fatalf("default Accept() = (%q, %v), want original, nil", result.JSON, err)
	}
}

func TestShadowCannotRewriteOrReject(t *testing.T) {
	restore := Register(testAcceptor{
		mode:   ModeShadow,
		result: Result{JSON: "rewritten"},
		err:    errors.New("shadow validation error"),
	})
	defer restore()

	result, err := Accept(context.Background(), Request{Raw: "original"})
	if err != nil || result.JSON != "original" {
		t.Fatalf("shadow Accept() = (%q, %v), want original, nil", result.JSON, err)
	}
	if err := ValidateResponse(context.Background(), Response{}); err != nil {
		t.Fatalf("shadow ValidateResponse() returned %v", err)
	}
}

func TestEnforceAppliesTimeout(t *testing.T) {
	restore := Register(testAcceptor{mode: ModeEnforce, timeout: 20 * time.Millisecond})
	defer restore()

	ctx, cancel := WithCallTimeout(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("enforce mode did not add a call deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 20*time.Millisecond {
		t.Fatalf("unexpected call timeout remaining: %s", remaining)
	}
}

func TestOffPreservesOriginalContext(t *testing.T) {
	restore := Register(testAcceptor{mode: ModeOff, timeout: time.Second})
	defer restore()

	original := context.Background()
	got, cancel := WithCallTimeout(original)
	defer cancel()
	if got != original {
		t.Fatal("off mode replaced the original context")
	}
}
