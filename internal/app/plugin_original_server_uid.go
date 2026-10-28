package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
)

// Fixed SystemUtils.generate_user_unique_id: SHA256 of root device-inode;
// only on failure, SHA256 of a non-multicast node rendered as12 hex digits.
// The source comment mentions hostname, but its actual loop has no such step.
func originalPluginUIDFromHost(root func() (string, error), node func() (uint64, error)) string {
	identity, err := root()
	if err != nil {
		mac, nodeErr := node()
		if nodeErr != nil || (mac>>40)%2 != 0 {
			return ""
		}
		identity = fmt.Sprintf("%012x", mac)
	}
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:])
}

func originalPluginRootIdentity() (string, error) {
	info, err := os.Stat("/")
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("root filesystem identity unavailable")
	}
	return fmt.Sprintf("%d-%d", stat.Dev, stat.Ino), nil
}

// Native Linux node provider follows uuid.getnode's preference for universal
// six-byte addresses before local ones. A random/multicast node is rejected by
// the source algorithm; do not replace an unavailable host identity with random.
func originalPluginMACNode() (uint64, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return 0, err
	}
	var local uint64
	hasLocal := false
	for _, iface := range interfaces {
		mac := iface.HardwareAddr
		if len(mac) != 6 || mac[0]&1 != 0 {
			continue
		}
		var value uint64
		for _, part := range mac {
			value = value<<8 | uint64(part)
		}
		if mac[0]&2 == 0 {
			return value, nil
		}
		if !hasLocal {
			local, hasLocal = value, true
		}
	}
	if hasLocal {
		return local, nil
	}
	return 0, errors.New("MAC node unavailable")
}

func (a *App) originalPluginServerUID(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if cached := a.pluginOriginalUserUID.Load(); cached != nil {
		return *cached, nil
	}
	uid := originalPluginUIDFromHost(originalPluginRootIdentity, originalPluginMACNode)
	if uid == "" {
		return "", nil
	} // source omits header and retries next time
	if a.pluginOriginalUserUID.CompareAndSwap(nil, &uid) {
		return uid, nil
	}
	return *a.pluginOriginalUserUID.Load(), nil
}
