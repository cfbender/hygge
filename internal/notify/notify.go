// Package notify provides desktop notification support.
//
// # Backends
//
// Two backends are available:
//   - NoopBackend: does nothing (used when notifications are disabled or in tests).
//   - NativeBackend: sends OS-native desktop notifications via the beeep library.
//
// The UI layer selects a backend at startup based on config.Notifications.Enabled
// and swaps it at any time via the Backend interface.
package notify

// Notification is a single desktop notification payload.
type Notification struct {
	// Title is the notification title line.
	Title string
	// Message is the notification body text.
	Message string
}

// Backend abstracts the OS notification mechanism.
// Implementations must be safe for concurrent use.
type Backend interface {
	Send(n Notification) error
}
