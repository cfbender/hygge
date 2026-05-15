package notify

import (
	"log/slog"

	"github.com/gen2brain/beeep"
)

// notifyFn is the function used to send notifications.
// Replaced in tests to prevent real OS notification dialogs.
var notifyFn = func(title, msg, icon string) error {
	return beeep.Notify(title, msg, icon)
}

// NativeBackend sends desktop notifications via the OS notification system.
// It uses the beeep library which supports macOS, Linux (libnotify), and Windows.
type NativeBackend struct{}

func init() {
	beeep.AppName = "Hygge"
}

// Send sends a native desktop notification.  Errors are returned to the
// caller for best-effort logging; a failure does not crash the caller.
func (NativeBackend) Send(n Notification) error {
	if err := notifyFn(n.Title, n.Message, ""); err != nil {
		slog.Debug("notify: native send failed", "err", err)
		return err
	}
	return nil
}
