package app

import "encoding/json"

// FileManager.transfer resolves these flags before calling the native handler.
// A selected directory may explicitly carry null; an explicit target path
// defaults notification and an unspecified scrape flag to false.
func originalTransferApplyNativeFlags(info map[string]any, kwargs map[string]any, success bool) error {
	_, hasFileItem := info["fileitem"]
	_, hasTransferType := info["transfer_type"]
	if hasFileItem && hasTransferType && info["fileitem"] == nil && info["transfer_type"] == nil {
		return nil
	} // freshly constructed exception TransferInfo
	var flags struct {
		Scrape    *bool `json:"scrape"`
		Directory *struct {
			Notify   *bool `json:"notify"`
			Scraping *bool `json:"scraping"`
		} `json:"target_directory"`
	}
	if err := json.Unmarshal(mustCleanSeedJSON(kwargs), &flags); err != nil {
		return err
	}
	value := func(flag *bool) any {
		if flag == nil {
			return nil
		}
		return *flag
	}
	info["need_notify"] = false
	if flags.Directory != nil {
		info["need_notify"] = value(flags.Directory.Notify)
	}
	if success {
		info["need_scrape"] = false
		if flags.Scrape != nil {
			info["need_scrape"] = *flags.Scrape
		} else if flags.Directory != nil {
			info["need_scrape"] = value(flags.Directory.Scraping)
		}
	}
	return nil
}
