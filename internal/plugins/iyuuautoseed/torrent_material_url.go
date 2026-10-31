package iyuuautoseed

import "strings"

// TorrentMaterialURLJoin reuses the pinned source urllib.parse implementation;
// it does not perform requests, validate authority, or grant a new origin.
func TorrentMaterialURLJoin(base, relative string) (string, error) {
	return sourceURLJoin(base, relative)
}

// NormalizeTorrentMaterialURL is DownloadChain._normalize_indirect_download_url
// for its string inputs. The caller preserves non-string source exceptions.
func NormalizeTorrentMaterialURL(address, base string) (string, error) {
	if address == "" || base == "" {
		return address, nil
	}
	trusted, err := sourceParseURL(base, "")
	if err != nil {
		return "", err
	}
	if trusted.scheme == "" || trusted.netloc == "" {
		return address, nil
	}
	parsed, err := sourceParseURL(address, "")
	if err != nil {
		return "", err
	}
	if parsed.netloc == "" {
		return sourceURLJoin(strings.TrimRight(base, "/")+"/", address)
	}
	parsed.scheme, parsed.netloc = trusted.scheme, trusted.netloc
	return sourceURLUnparse(parsed), nil
}

// TorrentMaterialBaseHasOrigin preserves the normalizer's base-first parsing
// before a non-string URL reaches the source's urlparse/startswith failure.
func TorrentMaterialBaseHasOrigin(base string) (bool, error) {
	parsed, err := sourceParseURL(base, "")
	return parsed.scheme != "" && parsed.netloc != "", err
}
