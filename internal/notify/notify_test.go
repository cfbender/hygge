package notify

import (
	"errors"
	"testing"
)

func TestNoopBackend_Send(t *testing.T) {
	t.Parallel()
	b := NoopBackend{}
	if err := b.Send(Notification{Title: "t", Message: "m"}); err != nil {
		t.Fatalf("NoopBackend.Send: unexpected error: %v", err)
	}
}

func TestNativeBackend_Send_CallsNotifyFn(t *testing.T) {
	// Not parallel: mutates the package-level notifyFn.
	var captured Notification
	orig := notifyFn
	notifyFn = func(title, msg, _ string) error {
		captured = Notification{Title: title, Message: msg}
		return nil
	}
	t.Cleanup(func() { notifyFn = orig })

	b := NativeBackend{}
	n := Notification{Title: "Hygge is waiting…", Message: "Permission required to execute \"bash\""}
	if err := b.Send(n); err != nil {
		t.Fatalf("NativeBackend.Send: unexpected error: %v", err)
	}
	if captured.Title != n.Title {
		t.Errorf("title = %q, want %q", captured.Title, n.Title)
	}
	if captured.Message != n.Message {
		t.Errorf("message = %q, want %q", captured.Message, n.Message)
	}
}

func TestNativeBackend_Send_PropagatesError(t *testing.T) {
	// Not parallel: mutates the package-level notifyFn.
	wantErr := errors.New("notify: OS rejected notification")
	orig := notifyFn
	notifyFn = func(_, _, _ string) error { return wantErr }
	t.Cleanup(func() { notifyFn = orig })

	b := NativeBackend{}
	err := b.Send(Notification{Title: "x", Message: "y"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("want %v, got %v", wantErr, err)
	}
}
