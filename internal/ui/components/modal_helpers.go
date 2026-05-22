package components

// ModalContentWidth returns the inner content width for modal boxes, clamped to
// a readable range shared by modal components.
func ModalContentWidth(containerWidth int) int {
	width := containerWidth - 12
	if containerWidth <= 0 {
		width = 80
	}
	width = min(width, containerWidth, 96)
	width = max(width, 36)
	return width
}
