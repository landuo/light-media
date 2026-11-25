package iyuuautoseed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	sdk "light-media/builtinplugins/internal/sdk"
	"light-media/builtinplugins/plugins/autosignin/pyvalue"
)

func (d sourceDownloaderReads) AddPaused(ctx context.Context, s Service, spec AddSpec) (AddReceipt, error) {
	if !spec.Paused {
		return AddReceipt{}, errors.New("IYUU requires paused addition")
	}
	if s.Kind == "qbittorrent" && s.actualKind() == "transmission" {
		return AddReceipt{}, sourceFault("TypeError", "Transmission.add_torrent tag")
	}
	save, err := optionalDownloaderText(spec.SavePath, "download directory")
	if err != nil {
		return AddReceipt{}, err
	}
	request := sdk.DownloaderSourceAddRequestV1{DownloaderAddRequestV1: sdk.DownloaderAddRequestV1{
		DownloaderID: s.Ref, Torrent: spec.Content, Magnet: spec.Magnet,
		Paused: true, SavePath: save, Tags: spec.Tags,
	}}
	if s.Kind == "qbittorrent" {
		category, err := optionalDownloaderText(spec.Category, "category")
		if err != nil {
			return AddReceipt{}, err
		}
		request.Category = category
		request.IgnoreCategoryCheck = &spec.IgnoreCategoryCheck
		request.SkipCheckingWire, err = downloaderSkipCheckingWire(spec.SkipCheckingRaw, spec.SkipChecking)
		if err != nil {
			// The qB wrapper catches the vendor's multipart encoding failure
			// and returns (False, []), which is still true in __download.
			return AddReceipt{SourceTruthy: true, Outcome: "rejected"}, nil
		}
	} else if s.actualKind() == "qbittorrent" {
		// TR metadata supplies labels to the actual qB wrapper as **kwargs.
		request.Tags, request.QBLabels = nil, spec.Tags
	}
	result, err := d.client.SourceAdd(ctx, request)
	if err != nil {
		return AddReceipt{}, err
	}
	receipt := AddReceipt{
		Accepted: result.Accepted, Outcome: result.Outcome,
		SourceTruthy: s.actualKind() == "qbittorrent" || result.Accepted && !result.NativeError,
	}
	if s.actualKind() == "transmission" && receipt.SourceTruthy {
		receipt.Hash = result.Hash
		if !sdk.DownloaderTaskHashValid(receipt.Hash) {
			return receipt, errors.New("IYUU Transmission add returned an invalid task hash")
		}
	}
	return receipt, downloaderMutationFence(result)
}

func downloaderSkipCheckingWire(raw json.RawMessage, fallback any) (*string, error) {
	if len(raw) == 0 {
		var err error
		raw, err = json.Marshal(fallback)
		if err != nil {
			return nil, err
		}
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	value, err := pyvalue.JSONString(raw)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func optionalDownloaderText(value any, name string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, sourceFault("TypeError", name)
	}
	return &text, nil
}

func (d sourceDownloaderReads) FindByTag(ctx context.Context, s Service, tag string) (string, error) {
	result, err := d.client.SourceFindByTag(ctx, sdk.DownloaderFindByTagRequestV1{DownloaderID: s.Ref, Tags: []string{tag}})
	if err != nil || result.Hash == nil {
		return "", err
	}
	return *result.Hash, nil
}

func (d sourceDownloaderReads) SetCategory(ctx context.Context, s Service, hash string, value any) (EffectReceipt, error) {
	category, err := optionalDownloaderText(value, "category")
	if err != nil {
		return EffectReceipt{}, err
	}
	return d.control(ctx, s, []string{hash}, "set_category", category)
}

func (d sourceDownloaderReads) Recheck(ctx context.Context, s Service, hashes []string) (EffectReceipt, error) {
	return d.control(ctx, s, hashes, "recheck", nil)
}

func (d sourceDownloaderReads) Start(ctx context.Context, s Service, hashes []string) (EffectReceipt, error) {
	return d.control(ctx, s, hashes, "start", nil)
}

func (d sourceDownloaderReads) control(ctx context.Context, s Service, hashes []string, action string, category *string) (EffectReceipt, error) {
	result, err := d.client.SourceControl(ctx, sdk.DownloaderControlRequestV1{
		DownloaderID: s.Ref, Hashes: hashes, Action: action, Category: category,
	})
	if err != nil {
		return EffectReceipt{}, err
	}
	return EffectReceipt{Accepted: result.Accepted, Outcome: result.Outcome}, downloaderMutationFence(result)
}

func downloaderMutationFence(result sdk.DownloaderSourceMutationResultV1) error {
	if result.FailureCode != "" {
		return errors.New(result.FailureCode)
	}
	return nil
}
