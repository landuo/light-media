package mergemessagesmtp

import "path/filepath"

func originalFixture(name string) string {
	return filepath.Join("..", "..", "..", "..", "testdata", "plugins", "merge-message-notify-1.20.1", "input", "_merge_message_smtp_port", name)
}
