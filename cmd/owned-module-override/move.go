package main

import "os"

// Acceptance-only real move. Both owned roots were checked by module_transfer;
// the destructive effect occurs in this separate SDK process, not in the host.
func (f *fixture) transferOwnedMove(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if f.config.TransferMode == "move_corrupt" {
		for i := range data {
			data[i] = 'x'
		}
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	if _, err = output.Write(data); err == nil {
		err = output.Sync()
	}
	closeErr := output.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Remove(source)
}
