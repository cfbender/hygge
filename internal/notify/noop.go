package notify

// NoopBackend is a Backend that silently drops all notifications.
// Used when notifications are disabled or in tests where real OS
// notifications must not fire.
type NoopBackend struct{}

// Send discards the notification and returns nil.
func (NoopBackend) Send(_ Notification) error { return nil }
