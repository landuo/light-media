package app

import (
	"context"
	"encoding/json"

	lark "light-media/internal/plugins/larkmessager"
)

// This is the private, immutable host-side projection of an actual MessageChain
// producer. It is not an API/SDK input type: workers receive only call_id.
// Recipients, old message IDs, attachments and response metadata are frozen by
// the producer and authenticated again before any native grant is constructed.
type pluginLarkModuleInput struct {
	Name                                            string
	Notification                                    lark.Notification
	Medias                                          []lark.MediaView
	Torrents                                        []lark.TorrentView
	Channel, Source, MessageID, ChatID, Title, Text string
	User                                            any
	Buttons                                         [][]lark.Button
	Metadata                                        lark.Object
	Status                                          lark.Object
	Ref, Emoji, ReactionID                          string
}

// Every original get_module entry has a real source implementation here. An
// unavailable producer/authority must reject before this function; this switch
// never turns an unknown module or a dependency failure into a successful send.
func invokeLarkModule(ctx context.Context, p *lark.Plugin, in pluginLarkModuleInput) (json.RawMessage, error) {
	if p == nil {
		return nil, lark.ErrDependency
	}
	var value any
	var e error
	switch in.Name {
	case "message_parser":
		value, e = p.ParseMessage(ctx, in.Source, in.Metadata)
	case "post_message":
		e = p.Post(ctx, in.Notification)
	case "post_medias_message":
		e = p.PostMedias(ctx, in.Notification, in.Medias)
	case "post_torrents_message":
		e = p.PostTorrents(ctx, in.Notification, in.Torrents)
	case "edit_message":
		value, e = p.Edit(ctx, in.Channel, in.Source, in.MessageID, in.ChatID, in.Title, in.Text, in.Buttons, in.Metadata)
	case "send_direct_message":
		value, e = p.SendDirect(ctx, in.Notification)
	case "finalize_message":
		value, e = p.Finalize(ctx, in.Channel, in.Metadata)
	case "mark_message_processing_started":
		value, e = p.ProcessingStarted(ctx, in.Channel, in.Source, in.User, in.MessageID, in.ChatID, in.Text)
	case "mark_message_processing_finished":
		value, e = p.ProcessingFinished(ctx, in.Channel, in.Source, in.MessageID, in.Status)
	case "download_feishu_image_to_data_url":
		value, e = p.DownloadImage(ctx, in.Ref, in.Source)
	case "download_feishu_file_bytes":
		value, e = p.DownloadFile(ctx, in.Ref, in.Source)
	case "add_feishu_message_reaction":
		value, e = p.AddReaction(ctx, in.MessageID, in.Emoji, in.Source)
	case "delete_feishu_message_reaction":
		value, e = p.DeleteReaction(ctx, in.MessageID, in.ReactionID, in.Source)
	default:
		return nil, lark.ErrDenied
	}
	if e != nil {
		return nil, e
	}
	return json.Marshal(value)
}

// The source Notification has an optional enum. Its Go zero string represents
// source None, not an invalid enum value "". Preserve that distinction in the
// private durable DTO without loosening the source model's own validation.
func (in pluginLarkModuleInput) MarshalJSON() ([]byte, error) {
	type plain pluginLarkModuleInput
	notification, e := json.Marshal(in.Notification)
	if e != nil {
		return nil, e
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(notification, &fields); e != nil {
		return nil, e
	}
	if in.Notification.MType == "" {
		fields["mtype"] = json.RawMessage(`null`)
	}
	notification, e = json.Marshal(fields)
	if e != nil {
		return nil, e
	}
	return json.Marshal(struct {
		*plain
		Notification json.RawMessage
	}{(*plain)(&in), notification})
}
