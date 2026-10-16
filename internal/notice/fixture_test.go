package notice

import "path/filepath"

func originalFixture(name string) string {
	return filepath.Join("../../testdata/notice-message-core/input/_notice_message_port", filepath.FromSlash(name))
}
