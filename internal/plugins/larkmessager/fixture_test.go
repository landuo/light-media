package larkmessager

import "path/filepath"

func originalFixture(name string) string {
	return filepath.Join("..", "..", "..", "testdata", "plugins", "lark-messager-0.9.1", "input", "_lark_messager_port", name)
}
