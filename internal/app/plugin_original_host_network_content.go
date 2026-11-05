package app

import (
	"errors"
	"light-media/internal/networkproxy"
	"light-media/internal/plugins"
)

const originalHostNetworkContentLimit = (plugins.MaxPayloadBytes - 1024) * 3 / 4

// Keep the host API error projection separate from source requests decoding.
func originalHostNetworkDecode(data []byte, encoding string) ([]byte, error) {
	value, err := networkproxy.DecodeRequestsContent(data, encoding, originalHostNetworkContentLimit)
	if errors.Is(err, networkproxy.ErrRequestsContentTooLarge) {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "远程文本超出共享传输上限")
	}
	return value, err
}
