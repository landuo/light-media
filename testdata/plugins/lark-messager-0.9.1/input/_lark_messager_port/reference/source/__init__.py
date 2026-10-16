import json
import re
import threading
import time
from pathlib import Path
from typing import Any, Dict, List, Tuple, Optional, Union

import requests
from fastapi import Request, Response
from fastapi.responses import JSONResponse

from app.core.config import settings
from app.core.context import Context, MediaInfo
from app.core.event import eventmanager
from app.plugins import _PluginBase
from app.log import logger
from app.schemas import CommingMessage, MessageResponse, Notification
from app.schemas.event import Event
from app.schemas.types import EventType, MessageChannel, NotificationType, SystemConfigKey
from app.utils.http import RequestUtils

from .client import LarkClient, API_BASE
from .crypto import LarkCrypto
from .schemas import LarkWebhookEvent


class LarkMessager(_PluginBase):
    """
    Lark 开放平台应用通知与消息交互插件
    通过 get_module() 注册到系统消息链，与内置飞书模块共享同一 MessageChannel.Feishu 渠道。
    入站消息通过 _forward_to_message_chain() → POST /api/v1/message → MessageChain 处理，
    出站通知通过 post_message(Notification) → 建卡发送。

    对标内置飞书模块 app/modules/feishu/__init__.py + feishu.py 的全部功能。
    """

    # —— 插件元数据 —— #
    plugin_name = "Lark 应用消息通知"
    plugin_desc = "基于国际版飞书 Lark 开放平台应用的通知与消息交互插件，支持文本、卡片消息发送及消息回调交互。"
    plugin_icon = "https://raw.githubusercontent.com/jxxghp/MoviePilot-Plugins/main/icons/FeiShu_A.png"
    plugin_version = "0.9.1"
    plugin_author = "ui-beam-9"
    author_url = "https://github.com/ui-beam-9"
    plugin_config_prefix = "larkmessager_"
    plugin_order = 50
    auth_level = 1

    # —— 消息路由标识（对应 /api/v1/message?source=Lark） —— #
    _msg_source: str = "Lark"

    # —— 运行时状态 —— #
    _enabled: bool = False
    _app_id: str = ""
    _app_secret: str = ""
    _chat_id: str = ""
    _user_id: str = ""
    _verification_token: str = ""
    _encrypt_key: str = ""
    _admin_users: List[str] = []
    _admin_users_resolved: dict = {}
    _config_missing: List[str] = []
    _switchs: List[str] = []
    _client: Optional[LarkClient] = None
    _crypto: Optional[LarkCrypto] = None
    _stop_event = False

    # ================================================================== #
    #  get_module — 注册插件方法到系统消息链（对标内置模块全部方法）
    # ================================================================== #
    def get_module(self) -> Dict[str, Any]:
        """
        返回插件模块方法字典，供 MessageChain 调用。
        对标内置 FeishuModule 的全部消息链方法。
        """
        if not self._enabled or not self._client:
            logger.debug(
                "LarkMessager get_module 返回空字典：enabled=%s, client=%s",
                self._enabled,
                bool(self._client),
            )
            return {}
        return {
            "message_parser": self.message_parser,
            "post_message": self.post_message,
            "post_medias_message": self.post_medias_message,
            "post_torrents_message": self.post_torrents_message,
            "edit_message": self.edit_message,
            "send_direct_message": self.send_direct_message,
            "finalize_message": self.finalize_message,
            "mark_message_processing_started": self.mark_message_processing_started,
            "mark_message_processing_finished": self.mark_message_processing_finished,
            "download_feishu_image_to_data_url": self.download_feishu_image_to_data_url,
            "download_feishu_file_bytes": self.download_feishu_file_bytes,
            "add_feishu_message_reaction": self.add_feishu_message_reaction,
            "delete_feishu_message_reaction": self.delete_feishu_message_reaction,
        }

    # ================================================================== #
    #  MessageAction 事件处理 — 接收 [PLUGIN] 前缀的按钮回调
    #  对标系统 MessageChain._handle_callback 的 [PLUGIN] 分支
    # ================================================================== #
    @eventmanager.register(EventType.MessageAction)
    def on_message_action(self, event: Event):
        """
        监听 EventType.MessageAction 事件，处理 [PLUGIN]LarkMessager|xxx 格式的按钮回调。
        当系统 MessageChain._handle_callback 收到 [PLUGIN] 前缀的 callback_data 时，
        会广播此事件，plugin_id 为去掉前缀的插件类名，text 为 | 后的内容。
        """
        event_data = event.event_data or {}
        plugin_id = event_data.get("plugin_id")
        if plugin_id != self.__class__.__name__:
            return

        text = event_data.get("text") or ""
        userid = event_data.get("userid") or ""
        channel = event_data.get("channel")
        source = event_data.get("source")
        original_message_id = event_data.get("original_message_id") or ""
        original_chat_id = event_data.get("original_chat_id") or ""

        logger.info(
            "LarkMessager 收到 MessageAction 回调：text=%s, userid=%s",
            text,
            userid,
        )

        # 已知的插件交互动作
        if text == "test_ok":
            # 测试确认按钮 — 通过 MessageChain 路由回来的情况
            if self._client:
                try:
                    self._client.reply_message(
                        original_message_id,
                        "✅ 测试确认成功！LarkMessager 插件卡片交互正常工作。",
                        msg_type="text",
                    )
                except Exception as e:
                    logger.error("回复测试确认消息失败：%s", e)
            return

        # 其它已知动作可在此扩展
        logger.info("LarkMessager MessageAction：未处理的动作 text=%s", text)

    # ================================================================== #
    #  message_parser — 解析来自 /api/v1/message 的消息 payload
    #  对标 Feishu.parse_message
    # ================================================================== #
    def message_parser(
        self, source: str, body: Any, form: Any, args: Any
    ) -> Optional[CommingMessage]:
        if source != self._msg_source:
            return None

        try:
            message = (
                json.loads(body) if isinstance(body, (str, bytes, bytearray)) else body
            )
        except Exception:
            return None
        if not isinstance(message, dict):
            return None

        sender = message.get("sender") or {}
        open_id = sender.get("open_id") or ""
        user_id = sender.get("user_id") or ""
        username = self._resolve_username(
            open_id, user_id, sender.get("name") or open_id or user_id
        )
        userid = open_id or user_id or ""
        if not userid:
            return None

        # 卡片按钮回调
        if message.get("type") == "cardAction":
            callback_data = message.get("callback_data") or ""
            if not callback_data:
                return None
            if str(callback_data).strip().startswith(
                "/"
            ) and self._should_reject_admin_command(open_id, user_id):
                if self._client:
                    self._client.send_text(
                        "只有管理员才有权限执行此命令",
                        userid=str(userid),
                        chat_id=message.get("chat_id"),
                        receive_id_type="open_id" if open_id else "user_id",
                        default_open_id=self._user_id or None,
                        default_chat_id=self._chat_id or None,
                    )
                return None
            return CommingMessage(
                channel=MessageChannel.Feishu,
                source=self._msg_source,
                userid=userid,
                username=username,
                text=f"CALLBACK:{callback_data}",
                is_callback=True,
                callback_data=callback_data,
                message_id=message.get("message_id") or "",
                chat_id=message.get("chat_id") or "",
            )

        # 普通消息：解析文本、图片、音频、文件
        text = (message.get("text") or "").strip()
        images = CommingMessage.MessageImage.normalize_list(message.get("images"))
        audio_refs = None
        if isinstance(message.get("audio_refs"), list):
            audio_refs = [
                str(item).strip()
                for item in message.get("audio_refs")
                if str(item).strip()
            ] or None
        files = None
        if isinstance(message.get("files"), list):
            normalized_files = []
            for item in message.get("files"):
                if isinstance(item, dict) and item.get("ref"):
                    normalized_files.append(CommingMessage.MessageAttachment(**item))
            files = normalized_files or None

        if not text and not images and not audio_refs and not files:
            return None

        if text.startswith("/") and self._should_reject_admin_command(open_id, user_id):
            if self._client:
                self._client.send_text(
                    "只有管理员才有权限执行此命令",
                    userid=str(userid),
                    chat_id=message.get("chat_id"),
                    receive_id_type="open_id" if open_id else "user_id",
                    default_open_id=self._user_id or None,
                    default_chat_id=self._chat_id or None,
                )
            return None

        return CommingMessage(
            channel=MessageChannel.Feishu,
            source=self._msg_source,
            userid=userid,
            username=username,
            text=text,
            message_id=message.get("message_id") or "",
            chat_id=message.get("chat_id") or "",
            images=images,
            audio_refs=audio_refs,
            files=files,
        )

    # ================================================================== #
    #  post_message — 将系统通知发送到 Lark（对标 FeishuModule.post_message）
    # ================================================================== #
    def post_message(self, message: Notification, **kwargs) -> None:
        if not self._enabled or not self._client:
            logger.debug(
                "LarkMessager post_message 跳过：enabled=%s, client=%s, mtype=%s",
                self._enabled,
                bool(self._client),
                message.mtype.value if message.mtype else None,
            )
            return

        logger.info(
            "LarkMessager post_message 收到通知：mtype=%s, title=%s, userid=%s",
            message.mtype.value if message.mtype else None,
            message.title,
            message.userid,
        )

        # 场景类型过滤
        if self._switchs and message.mtype:
            if message.mtype.value not in self._switchs:
                logger.info(
                    "LarkMessager 通知被 switchs 过滤：mtype=%s, switchs=%s",
                    message.mtype.value,
                    self._switchs,
                )
                return

        userid, chat_id, receive_id_type = self._resolve_message_target(message)
        original_message_id = (
            str(message.original_message_id) if message.original_message_id else None
        )

        # 广播通知无明确目标时，回退到插件配置的默认用户/群聊
        if not userid and not chat_id:
            userid = self._user_id or None
            chat_id = self._chat_id or None
            receive_id_type = "open_id" if userid else None
            logger.debug(
                "LarkMessager 广播通知回退默认目标：userid=%s, chat_id=%s",
                userid,
                chat_id,
            )

        if not userid and not chat_id:
            logger.warning(
                "LarkMessager post_message 无发送目标：userid 和 chat_id 均为空，请配置默认通知用户或群聊"
            )
            return

        logger.info(
            "LarkMessager post_message 发送通知：userid=%s, chat_id=%s, receive_id_type=%s",
            userid,
            chat_id,
            receive_id_type,
        )

        if message.image and message.file_path:
            # 图文+文件：先发图文卡片，再发文件
            self._client.send_notification(
                message=message.model_copy(
                    update={"file_path": None, "file_name": None}
                ),
                userid=userid,
                chat_id=chat_id,
                receive_id_type=receive_id_type,
                original_message_id=original_message_id,
                default_open_id=self._user_id or None,
                default_chat_id=self._chat_id or None,
            )
            self._client.send_file(
                file_path=message.file_path,
                userid=userid,
                chat_id=chat_id,
                file_name=message.file_name,
                receive_id_type=receive_id_type,
                original_message_id=original_message_id,
                default_open_id=self._user_id or None,
                default_chat_id=self._chat_id or None,
            )
        elif message.file_path:
            self._client.send_file(
                file_path=message.file_path,
                userid=userid,
                chat_id=chat_id,
                title=message.title,
                text=message.text,
                file_name=message.file_name,
                receive_id_type=receive_id_type,
                original_message_id=original_message_id,
                default_open_id=self._user_id or None,
                default_chat_id=self._chat_id or None,
            )
        elif message.voice_path:
            self._client.send_voice(
                voice_path=message.voice_path,
                userid=userid,
                chat_id=chat_id,
                caption=message.voice_caption,
                receive_id_type=receive_id_type,
                original_message_id=original_message_id,
                default_open_id=self._user_id or None,
                default_chat_id=self._chat_id or None,
            )
        else:
            self._client.send_notification(
                message=message,
                userid=userid,
                chat_id=chat_id,
                receive_id_type=receive_id_type,
                original_message_id=original_message_id,
                default_open_id=self._user_id or None,
                default_chat_id=self._chat_id or None,
            )

    # ================================================================== #
    #  post_medias_message — 发送媒体列表（对标 FeishuModule.post_medias_message）
    # ================================================================== #
    def post_medias_message(
        self, message: Notification, medias: List[MediaInfo]
    ) -> None:
        if not self._enabled or not self._client:
            return
        if self._switchs and message.mtype:
            if message.mtype.value not in self._switchs:
                return

        userid, chat_id, receive_id_type = self._resolve_message_target(message)
        # 广播通知回退默认目标
        if not userid and not chat_id:
            userid = self._user_id or None
            chat_id = self._chat_id or None
            receive_id_type = "open_id" if userid else None
        if not userid and not chat_id:
            logger.warning("LarkMessager post_medias_message 无发送目标")
            return
        # 构建图文列表：每个媒体带海报图，避免仅发文字
        image_items = []
        for index, media in enumerate(medias[:10], start=1):
            title = (
                getattr(media, "title_year", None)
                or getattr(media, "title", None)
                or "未知媒体"
            )
            numbered = f"{index}. {title}"
            poster = self._extract_poster(media)
            if poster:
                image_items.append({"title": numbered, "url": poster})
            else:
                image_items.append({"title": numbered})
        proxy_message = Notification(
            title=message.title,
            text=None,
            link=message.link,
            buttons=message.buttons,
            userid=message.userid,
            targets=message.targets,
        )
        self._client.send_notification(
            message=proxy_message,
            userid=userid or message.userid,
            chat_id=chat_id,
            receive_id_type=receive_id_type,
            default_open_id=self._user_id or None,
            default_chat_id=self._chat_id or None,
            image_items=image_items or None,
        )

    # ================================================================== #
    #  post_torrents_message — 发送种子列表（对标 FeishuModule.post_torrents_message）
    # ================================================================== #
    def post_torrents_message(
        self, message: Notification, torrents: List[Context]
    ) -> None:
        if not self._enabled or not self._client:
            return
        if self._switchs and message.mtype:
            if message.mtype.value not in self._switchs:
                return

        userid, chat_id, receive_id_type = self._resolve_message_target(message)
        # 广播通知回退默认目标
        if not userid and not chat_id:
            userid = self._user_id or None
            chat_id = self._chat_id or None
            receive_id_type = "open_id" if userid else None
        if not userid and not chat_id:
            logger.warning("LarkMessager post_torrents_message 无发送目标")
            return
        # 构建图文列表：每个种子带媒体海报图，避免仅发文字
        image_items = []
        for index, torrent in enumerate(torrents[:10], start=1):
            torrent_info = getattr(torrent, "torrent_info", None)
            title = (
                getattr(torrent_info, "title", None)
                or getattr(torrent_info, "site_name", None)
                or "未知种子"
            )
            numbered = f"{index}. {title}"
            poster = self._extract_poster(getattr(torrent, "media_info", None))
            if poster:
                image_items.append({"title": numbered, "url": poster})
            else:
                image_items.append({"title": numbered})
        proxy_message = Notification(
            title=message.title,
            text=None,
            link=message.link,
            buttons=message.buttons,
            userid=message.userid,
            targets=message.targets,
        )
        self._client.send_notification(
            message=proxy_message,
            userid=userid or message.userid,
            chat_id=chat_id,
            receive_id_type=receive_id_type,
            default_open_id=self._user_id or None,
            default_chat_id=self._chat_id or None,
            image_items=image_items or None,
        )

    # ================================================================== #
    #  edit_message — 编辑消息/流式更新（对标 FeishuModule.edit_message）
    # ================================================================== #
    def edit_message(
        self,
        channel: MessageChannel,
        source: str,
        message_id: Union[str, int],
        chat_id: Union[str, int],
        text: str,
        title: Optional[str] = None,
        buttons: Optional[List[List[dict]]] = None,
        metadata: Optional[dict] = None,
    ) -> Optional[bool]:
        if channel != MessageChannel.Feishu or source != self._msg_source:
            return None
        if not self._client:
            return False
        return self._client.edit_message(
            message_id=str(message_id),
            title=title,
            text=text,
            buttons=buttons,
            metadata=metadata,
            chat_id=str(chat_id) if chat_id else None,
        )

    # ================================================================== #
    #  send_direct_message — 定向消息带返回值（对标 FeishuModule.send_direct_message）
    # ================================================================== #
    def send_direct_message(self, message: Notification) -> Optional[MessageResponse]:
        if not self._enabled or not self._client:
            return None
        if self._switchs and message.mtype:
            if message.mtype.value not in self._switchs:
                return None

        userid, chat_id, receive_id_type = self._resolve_message_target(message)
        original_message_id = (
            str(message.original_message_id) if message.original_message_id else None
        )
        # 广播通知回退默认目标
        if not userid and not chat_id:
            userid = self._user_id or None
            chat_id = self._chat_id or None
            receive_id_type = "open_id" if userid else None

        if message.image and message.file_path:
            result = self._client.send_notification(
                message=message.model_copy(
                    update={"file_path": None, "file_name": None}
                ),
                userid=userid,
                chat_id=chat_id,
                receive_id_type=receive_id_type,
                original_message_id=original_message_id,
                default_open_id=self._user_id or None,
                default_chat_id=self._chat_id or None,
            )
            if result and result.get("success"):
                self._client.send_file(
                    file_path=message.file_path,
                    userid=userid,
                    chat_id=chat_id,
                    file_name=message.file_name,
                    receive_id_type=receive_id_type,
                    original_message_id=original_message_id,
                    default_open_id=self._user_id or None,
                    default_chat_id=self._chat_id or None,
                )
        elif message.file_path:
            result = self._client.send_file(
                file_path=message.file_path,
                userid=userid,
                chat_id=chat_id,
                title=message.title,
                text=message.text,
                file_name=message.file_name,
                receive_id_type=receive_id_type,
                original_message_id=original_message_id,
                default_open_id=self._user_id or None,
                default_chat_id=self._chat_id or None,
            )
        elif message.voice_path:
            result = self._client.send_voice(
                voice_path=message.voice_path,
                userid=userid,
                chat_id=chat_id,
                caption=message.voice_caption,
                receive_id_type=receive_id_type,
                original_message_id=original_message_id,
                default_open_id=self._user_id or None,
                default_chat_id=self._chat_id or None,
            )
        else:
            result = self._client.send_notification(
                message=message,
                userid=userid,
                chat_id=chat_id,
                receive_id_type=receive_id_type,
                original_message_id=original_message_id,
                default_open_id=self._user_id or None,
                default_chat_id=self._chat_id or None,
            )

        if result and result.get("success"):
            return MessageResponse(
                message_id=result.get("message_id"),
                chat_id=result.get("chat_id"),
                channel=MessageChannel.Feishu,
                source=self._msg_source,
                metadata=result.get("metadata"),
                success=True,
            )
        return None

    # ================================================================== #
    #  finalize_message — 关闭流式卡片（对标 FeishuModule.finalize_message）
    # ================================================================== #
    def finalize_message(self, response: MessageResponse) -> bool:
        if response.channel != MessageChannel.Feishu or not isinstance(
            response.metadata, dict
        ):
            return False
        if not self._client:
            return False
        stream_meta = response.metadata.get("feishu_streaming") or {}
        card_id = str(stream_meta.get("card_id") or "").strip()
        if not card_id:
            return False
        sequence = int(stream_meta.get("sequence") or 0) + 1
        return self._client.close_streaming_card(card_id=card_id, sequence=sequence)

    # ================================================================== #
    #  mark_message_processing_started — 标记消息处理中（对标 FeishuModule）
    # ================================================================== #
    def mark_message_processing_started(
        self,
        channel: MessageChannel,
        source: str,
        userid: Optional[Union[str, int]] = None,
        message_id: Optional[Union[str, int]] = None,
        chat_id: Optional[Union[str, int]] = None,
        text: Optional[str] = None,
    ) -> Optional[dict]:
        if channel != MessageChannel.Feishu or source != self._msg_source:
            return None
        if not message_id or not text or str(text).startswith("CALLBACK:"):
            return None
        reaction_id = self.add_feishu_message_reaction(
            message_id=str(message_id),
            emoji_type=LarkClient.PROCESSING_REACTION_EMOJI,
            source=source,
        )
        if not reaction_id:
            return None
        return {
            "channel": channel.value,
            "source": source,
            "userid": userid,
            "message_id": str(message_id),
            "chat_id": str(chat_id) if chat_id else None,
            "metadata": {
                "kind": "reaction",
                "reaction_id": str(reaction_id),
                "emoji_type": LarkClient.PROCESSING_REACTION_EMOJI,
            },
        }

    # ================================================================== #
    #  mark_message_processing_finished — 删除处理中标记（对标 FeishuModule）
    # ================================================================== #
    def mark_message_processing_finished(
        self,
        channel: MessageChannel,
        source: str,
        userid: Optional[Union[str, int]] = None,
        message_id: Optional[Union[str, int]] = None,
        chat_id: Optional[Union[str, int]] = None,
        status: Optional[dict] = None,
    ) -> Optional[bool]:
        if channel != MessageChannel.Feishu or source != self._msg_source:
            return None
        metadata = (status or {}).get("metadata") or {}
        target_message_id = (status or {}).get("message_id") or message_id
        reaction_id = metadata.get("reaction_id")
        if not target_message_id or not reaction_id:
            return False
        return self.delete_feishu_message_reaction(
            message_id=str(target_message_id),
            reaction_id=str(reaction_id),
            source=source,
        )

    # ================================================================== #
    #  download_feishu_image_to_data_url — 图片转 data URL（对标 FeishuModule）
    # ================================================================== #
    def download_feishu_image_to_data_url(
        self, image_ref: str, source: str
    ) -> Optional[str]:
        if not image_ref or not image_ref.startswith("feishu://image/"):
            return None
        if not self._client:
            return None
        resource_path = image_ref.replace("feishu://image/", "", 1)
        message_id = None
        image_key = resource_path
        if "/" in resource_path:
            message_id, image_key = resource_path.split("/", 1)
            message_id = message_id.strip() or None
            image_key = image_key.strip()

        downloaded = None
        if message_id:
            downloaded = self._client.download_message_resource_bytes(
                message_id=message_id,
                file_key=image_key,
                resource_type="image",
            )
        if not downloaded:
            downloaded = self._client.download_image_bytes(image_key)
        if not downloaded:
            return None
        content, _, content_type = downloaded
        mime_type = content_type or "image/jpeg"
        import base64

        return f"data:{mime_type};base64,{base64.b64encode(content).decode()}"

    # ================================================================== #
    #  download_feishu_file_bytes — 下载文件 bytes（对标 FeishuModule）
    # ================================================================== #
    def download_feishu_file_bytes(self, file_ref: str, source: str) -> Optional[bytes]:
        if not file_ref or not file_ref.startswith("feishu://file/"):
            return None
        if not self._client:
            return None
        parts = [
            part.strip()
            for part in file_ref.replace("feishu://file/", "", 1).split("/")
            if part.strip()
        ]
        file_key = ""
        downloaded = None
        if len(parts) >= 2 and parts[0].startswith("om_"):
            message_id, file_key = parts[0], parts[1]
            downloaded = self._client.download_message_resource_bytes(
                message_id=message_id,
                file_key=file_key,
                resource_type="audio",
            )
            if not downloaded:
                downloaded = self._client.download_message_resource_bytes(
                    message_id=message_id,
                    file_key=file_key,
                    resource_type="file",
                )
        else:
            file_key = parts[0] if parts else ""
        if not file_key:
            return None
        if not downloaded:
            downloaded = self._client.download_file_bytes(file_key)
        if not downloaded:
            return None
        content, _, _ = downloaded
        return content

    # ================================================================== #
    #  add_feishu_message_reaction — 添加表情回应（对标 FeishuModule）
    # ================================================================== #
    def add_feishu_message_reaction(
        self, message_id: str, emoji_type: str, source: str
    ) -> Optional[str]:
        if not self._client or source != self._msg_source:
            return None
        return self._client.add_message_reaction(
            message_id=message_id, emoji_type=emoji_type
        )

    # ================================================================== #
    #  delete_feishu_message_reaction — 删除表情回应（对标 FeishuModule）
    # ================================================================== #
    def delete_feishu_message_reaction(
        self, message_id: str, reaction_id: str, source: str
    ) -> Optional[bool]:
        if not self._client or source != self._msg_source:
            return False
        return self._client.delete_message_reaction(
            message_id=message_id, reaction_id=reaction_id
        )

    # ================================================================== #
    #  _forward_to_message_chain — 将 payload 转发到 /api/v1/message
    # ================================================================== #
    def _forward_to_message_chain(self, payload: dict) -> None:
        def _run() -> None:
            try:
                RequestUtils(timeout=15).post_res(
                    f"http://127.0.0.1:{settings.PORT}/api/v1/message"
                    f"?token={settings.API_TOKEN}&source={self._msg_source}",
                    json=payload,
                )
            except Exception as err:
                logger.error("LarkMessager 转发消息到 MessageChain 失败：%s", err)

        threading.Thread(target=_run, daemon=True).start()

    # ================================================================== #
    #  消息目标解析（对标 FeishuModule._resolve_message_target）
    # ================================================================== #
    @staticmethod
    def _resolve_message_target(
        message: Notification,
    ) -> Tuple[Optional[str], Optional[str], Optional[str]]:
        """优先使用 open_id，其次回退 user_id 或 chat_id。"""
        userid = str(message.userid).strip() if message.userid else None
        chat_id = None
        receive_id_type = "open_id" if userid else None

        targets = message.targets or {}
        if not userid and targets:
            open_id = str(targets.get("feishu_openid") or "").strip() or None
            user_id = str(targets.get("feishu_userid") or "").strip() or None
            chat_id = str(targets.get("feishu_chat_id") or "").strip() or None
            if open_id:
                userid = open_id
                receive_id_type = "open_id"
            elif user_id:
                userid = user_id
                receive_id_type = "user_id"

        return userid, chat_id, receive_id_type

    # ================================================================== #
    #  海报图片提取（用于媒体/种子列表场景）
    # ================================================================== #
    @staticmethod
    def _extract_poster(obj) -> Optional[str]:
        """从 MediaInfo / Context 等对象安全提取海报图片 URL。"""
        if obj is None:
            return None
        get_img = getattr(obj, "get_message_image", None)
        if callable(get_img):
            try:
                val = get_img()
                if val:
                    return str(val)
            except Exception:
                pass
        for attr in ("message_image", "poster_path", "poster"):
            val = getattr(obj, attr, None)
            if val:
                return str(val)
        return None

    # ================================================================== #
    #  用户名解析（对标 Feishu._resolve_username）
    # ================================================================== #
    @staticmethod
    def _resolve_username(
        open_id: Optional[str], user_id: Optional[str], fallback: Optional[str]
    ) -> Optional[str]:
        """根据 Lark 绑定 ID 映射 MoviePilot 用户名。"""
        try:
            from app.db.user_oper import UserOper

            binding_ids = {}
            if open_id:
                binding_ids["feishu_openid"] = open_id
            if user_id:
                binding_ids["feishu_userid"] = user_id
            if binding_ids:
                mapped_username = UserOper().get_name(**binding_ids)
                if mapped_username:
                    return mapped_username
        except Exception as err:
            logger.debug(f"解析 Lark 用户绑定失败：{err}")
        return fallback

    # ================================================================== #
    #  管理员权限校验（对标 Feishu._should_reject_admin_command）
    # ================================================================== #
    def _should_reject_admin_command(
        self, *user_ids: Optional[Union[str, int]]
    ) -> bool:
        """判断命令是否应因非管理员身份被拒绝。"""
        if not self._admin_users:
            return False
        candidates = [
            str(uid).strip() for uid in user_ids if uid is not None and str(uid).strip()
        ]
        # 先做直接匹配（open_id 在 admin 列表中）
        if any(c in self._admin_users for c in candidates):
            return False
        # 再做邮箱/手机号解析匹配
        for c in candidates:
            if self._is_admin(c):
                return False
        return True

    # ------------------------------------------------------------------ #
    #  生命周期
    # ------------------------------------------------------------------ #
    def init_plugin(self, config: dict = None):
        config = config or {}
        self._enabled = bool(config.get("enabled", False))
        self._app_id = (config.get("app_id") or "").strip()
        self._app_secret = (config.get("app_secret") or "").strip()
        self._chat_id = (config.get("chat_id") or "").strip()
        self._user_id_raw = (config.get("FEISHU_OPEN_ID") or config.get("user_id") or "").strip()
        self._user_id = self._user_id_raw
        self._verification_token = (config.get("verification_token") or "").strip()
        self._encrypt_key = (config.get("encrypt_key") or "").strip()
        # 管理员用户：单一输入框，混填 邮箱 / 手机号 / Open ID（ou_xxx），
        # 多个用 , 分隔。插件按格式自动识别类型并解析成 Open ID，
        # 再同步给 AI 智能助手。兼容旧字段 admin_emails / admin_mobiles / admin_users。
        _admin_raw = [
            u.strip()
            for u in (
                config.get("FEISHU_ADMINS")
                or config.get("admin_users")
                or ""
            ).split(",")
            if u.strip()
        ]
        # 兼容历史独立字段（旧配置迁移用）
        for _legacy in (config.get("admin_emails") or "").split(",") + \
                (config.get("admin_mobiles") or "").split(","):
            _legacy = _legacy.strip()
            if _legacy and _legacy not in _admin_raw:
                _admin_raw.append(_legacy)
        self._admin_openids_direct = [u for u in _admin_raw if u.startswith("ou_")]
        self._admin_emails = [u for u in _admin_raw if "@" in u]
        # 非邮箱、非 Open ID 的统一当作手机号（允许 +86 之类带前缀）
        self._admin_mobiles = [
            u for u in _admin_raw if "@" not in u and not u.startswith("ou_")
        ]
        # 供插件自身斜杠命令校验使用（原始混合列表）
        self._admin_users = list(dict.fromkeys(_admin_raw))
        self._admin_users_resolved = {}
        self._switchs = config.get("switchs") or []

        missing: list[str] = []
        if not self._app_id:
            missing.append("App ID")
        if not self._app_secret:
            missing.append("App Secret")
        self._config_missing = missing
        if self._enabled and missing:
            logger.warning(
                "LarkMessager 必填项缺失，已自动禁用：%s。请在插件配置中补齐后重新启用。",
                "、".join(missing),
            )
            self._enabled = False

        if self._enabled and self._app_id and self._app_secret:
            self._client = LarkClient(self._app_id, self._app_secret)
            if self._encrypt_key:
                self._crypto = LarkCrypto(self._encrypt_key, self._app_secret)
                logger.info("LarkMessager: crypto 已初始化（encrypt_key 已配置）")
            else:
                self._crypto = None
                logger.warning(
                    "LarkMessager: encrypt_key 未配置，将无法校验签名和解密加密请求。"
                )
            logger.info("LarkMessager 初始化成功，App ID：%s", self._app_id)
        else:
            self._client = None
            self._crypto = None

        # 异步将管理员标识（邮箱/手机号/Open ID）解析为 Open ID，
        # 并同步到 SYSTEMCONFIG.Notifications（名为 Lark 的配置），
        # 使 AI 智能助手的渠道管理员判定能够识别。
        # 注意：插件自身配置（plugin.LarkMessager）不会被智能助手读取，
        # 必须写回到 Notifications 才会生效。
        self._start_admin_sync()

    def get_state(self) -> bool:
        return self._enabled

    def stop_service(self):
        self._stop_event = True
        self._client = None
        self._crypto = None
        logger.info("LarkMessager 已停止")

    # ------------------------------------------------------------------ #
    #  配置页（Vuetify JSON）
    # ------------------------------------------------------------------ #
    def get_form(self) -> Tuple[List[dict], Dict[str, Any]]:
        return [
            {
                "component": "VForm",
                "content": [
                    {
                        "component": "VRow",
                        "content": [
                            {
                                "component": "VCol",
                                "props": {"cols": 12, "md": 4},
                                "content": [
                                    {
                                        "component": "VSwitch",
                                        "props": {
                                            "model": "enabled",
                                            "label": "启用 Lark 通知",
                                            "hint": "开启后将监听通知事件并转发到 Lark",
                                            "persistentHint": True,
                                            "color": "primary",
                                        },
                                    }
                                ],
                            },
                            {
                                "component": "VCol",
                                "props": {"cols": 12, "md": 8},
                                "content": [
                                    {
                                        "component": "VTextField",
                                        "props": {
                                            "model": "app_id",
                                            "label": "App ID *",
                                            "placeholder": "cli_xxxxxxxxxxxxxxxx",
                                            "variant": "outlined",
                                            "hint": "Lark 开放平台应用的 App ID（必填）",
                                            "persistentHint": True,
                                            "clearable": True,
                                            "density": "comfortable",
                                            "rules": [
                                                {
                                                    "required": True,
                                                    "message": "App ID 不能为空",
                                                }
                                            ],
                                        },
                                    }
                                ],
                            },
                        ],
                    },
                    {
                        "component": "VRow",
                        "content": [
                            {
                                "component": "VCol",
                                "props": {"cols": 12},
                                "content": [
                                    {
                                        "component": "VTextField",
                                        "props": {
                                            "model": "app_secret",
                                            "label": "App Secret *",
                                            "placeholder": "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
                                            "variant": "outlined",
                                            "hint": "Lark 开放平台应用的 App Secret（必填）",
                                            "persistentHint": True,
                                            "type": "{{ app_secret_visible ? 'text' : 'password' }}",
                                            "append-inner-icon": "{{ app_secret_visible ? 'mdi-eye-off' : 'mdi-eye' }}",
                                            "onClick:append-inner": "function(e){ model.app_secret_visible = !model.app_secret_visible }",
                                            "clearable": True,
                                            "density": "comfortable",
                                            "rules": [
                                                {
                                                    "required": True,
                                                    "message": "App Secret 不能为空",
                                                }
                                            ],
                                        },
                                    }
                                ],
                            },
                        ],
                    },
                    {
                        "component": "VRow",
                        "content": [
                            {
                                "component": "VCol",
                                "props": {"cols": 12, "md": 6},
                                "content": [
                                    {
                                        "component": "VTextField",
                                        "props": {
                                            "model": "FEISHU_OPEN_ID",
                                            "label": "默认通知用户",
                                            "placeholder": "邮箱/手机号/ou_xxx",
                                            "variant": "outlined",
                                            "hint": "填邮箱、手机号或 Open ID（ou_xxx），留空则不发送私信",
                                            "persistentHint": True,
                                            "clearable": True,
                                            "density": "comfortable",
                                        },
                                    }
                                ],
                            },
                            {
                                "component": "VCol",
                                "props": {"cols": 12, "md": 6},
                                "content": [
                                    {
                                        "component": "VTextField",
                                        "props": {
                                            "model": "chat_id",
                                            "label": "默认通知群聊",
                                            "placeholder": "oc_xxx",
                                            "variant": "outlined",
                                            "hint": "填群聊 Chat ID（oc_xxx），留空则不发送群通知",
                                            "persistentHint": True,
                                            "clearable": True,
                                            "density": "comfortable",
                                        },
                                    }
                                ],
                            },
                        ],
                    },
                    {
                        "component": "VRow",
                        "content": [
                            {
                                "component": "VCol",
                                "props": {"cols": 12, "md": 6},
                                "content": [
                                    {
                                        "component": "VTextField",
                                        "props": {
                                            "model": "verification_token",
                                            "label": "Verification Token",
                                            "placeholder": "事件订阅 Token",
                                            "variant": "outlined",
                                            "hint": "Lark 事件订阅的 Verification Token",
                                            "persistentHint": True,
                                            "clearable": True,
                                            "density": "comfortable",
                                        },
                                    }
                                ],
                            },
                            {
                                "component": "VCol",
                                "props": {"cols": 12, "md": 6},
                                "content": [
                                    {
                                        "component": "VTextField",
                                        "props": {
                                            "model": "encrypt_key",
                                            "label": "Encrypt Key",
                                            "placeholder": "留空则不加密",
                                            "variant": "outlined",
                                            "hint": "Lark 事件订阅的 Encrypt Key，启用消息加密时填写",
                                            "persistentHint": True,
                                            "clearable": True,
                                            "density": "comfortable",
                                        },
                                    }
                                ],
                            },
                        ],
                    },
                    {
                        "component": "VRow",
                        "content": [
                            {
                                "component": "VCol",
                                "props": {"cols": 12},
                                "content": [
                                    {
                                        "component": "VTextField",
                                        "props": {
                                            "model": "FEISHU_ADMINS",
                                            "label": "管理员用户",
                                            "placeholder": "邮箱 / 手机号 / ou_xxx，多个用 , 分隔",
                                            "variant": "outlined",
                                            "hint": "允许执行命令与调用管理员工具的用户。可混填邮箱、手机号或 Lark Open ID（ou_xxx），任填其一即可，多个用 , 分隔。插件会自动识别类型并把邮箱/手机号解析为 Open ID，同步给 AI 智能助手（智能助手只认 Open ID）。使用邮箱/手机号需开通 contact:user.id:readonly 权限。",
                                            "persistentHint": True,
                                            "clearable": True,
                                            "density": "comfortable",
                                        },
                                    }
                                ],
                            },
                        ],
                    },
                    {
                        "component": "VRow",
                        "content": [
                            {
                                "component": "VCol",
                                "props": {"cols": 12},
                                "content": [
                                    {
                                        "component": "VSelect",
                                        "props": {
                                            "model": "switchs",
                                            "label": "通知场景类型",
                                            "multiple": True,
                                            "chips": True,
                                            "clearable": True,
                                            "hint": "需要接收通知的场景类型，不选则全部发送",
                                            "persistentHint": True,
                                            "items": [
                                                "资源下载",
                                                "整理入库",
                                                "订阅",
                                                "站点",
                                                "媒体服务器",
                                                "手动处理",
                                                "插件",
                                                "智能体",
                                                "其它",
                                            ],
                                        },
                                    }
                                ],
                            },
                        ],
                    },
                ],
            }
        ], {
            "enabled": False,
            "app_id": "",
            "app_secret": "",
            "app_secret_visible": False,
            "FEISHU_OPEN_ID": "",
            "chat_id": "",
            "verification_token": "",
            "encrypt_key": "",
            "FEISHU_ADMINS": "",
            "switchs": [],
        }

    # ------------------------------------------------------------------ #
    #  详情页
    # ------------------------------------------------------------------ #
    def get_page(self) -> List[dict]:
        config_ready = bool(self._app_id and self._app_secret)
        status_text = (
            "已启用"
            if (self._enabled and self._client)
            else ("必填项未配置，已自动禁用" if self._config_missing else "未启用")
        )
        status_color = "success" if (self._enabled and self._client) else "warning"
        status_icon = (
            "mdi-check-circle"
            if (self._enabled and self._client)
            else "mdi-alert-circle"
        )

        missing_card: List[dict] = []
        if self._config_missing:
            missing_card = [
                {
                    "component": "VAlert",
                    "props": {
                        "type": "warning",
                        "variant": "tonal",
                        "class": "mb-4",
                        "icon": "mdi-alert-circle",
                    },
                    "content": [
                        {
                            "component": "div",
                            "props": {"class": "font-weight-bold mb-1"},
                            "text": "插件未启用：必填项缺失",
                        },
                        {
                            "component": "div",
                            "text": (
                                "以下项尚未配置，插件已被自动禁用（不会发送任何消息）。"
                                "请在「插件配置」中补齐后重新启用："
                            ),
                        },
                        {
                            "component": "ul",
                            "props": {"class": "mt-1 mb-0 pl-4"},
                            "content": [
                                {"component": "li", "text": name}
                                for name in self._config_missing
                            ],
                        },
                    ],
                }
            ]

        guide_card = {
            "component": "VCard",
            "props": {"class": "mb-4"},
            "content": [
                {
                    "component": "VCardText",
                    "content": [
                        {
                            "component": "div",
                            "props": {"class": "d-flex align-center mb-3"},
                            "content": [
                                {
                                    "component": "VIcon",
                                    "props": {
                                        "color": "primary",
                                        "size": "small",
                                        "class": "mr-2",
                                    },
                                    "text": "mdi-information-outline",
                                },
                                {
                                    "component": "span",
                                    "props": {
                                        "class": "text-subtitle-1 font-weight-bold"
                                    },
                                    "text": "使用指南",
                                },
                            ],
                        },
                        {
                            "component": "div",
                            "props": {"class": "mb-2"},
                            "content": [
                                {"component": "span", "text": "1. 在 "},
                                {"component": "strong", "text": "Lark 开放平台"},
                                {"component": "span", "text": " 创建自建应用，获取 "},
                                {"component": "strong", "text": "App ID"},
                                {"component": "span", "text": " 和 "},
                                {"component": "strong", "text": "App Secret"},
                                {"component": "span", "text": "。"},
                            ],
                        },
                        {
                            "component": "div",
                            "props": {"class": "mb-2"},
                            "content": [
                                {"component": "span", "text": "2. 在应用详情页 "},
                                {
                                    "component": "strong",
                                    "text": "添加应用能力 - 机器人",
                                },
                                {
                                    "component": "span",
                                    "text": "（必须先添加才能收发消息）。",
                                },
                            ],
                        },
                        {
                            "component": "div",
                            "props": {"class": "mb-2"},
                            "content": [
                                {"component": "span", "text": "3. 进入 "},
                                {"component": "strong", "text": "权限管理"},
                                {
                                    "component": "span",
                                    "text": "，开通：im:message、im:chat、contact:user.base:readonly、contact:user.id:readonly",
                                },
                            ],
                        },
                        {
                            "component": "div",
                            "props": {"class": "mb-2"},
                            "content": [
                                {
                                    "component": "span",
                                    "text": "4. 配置事件订阅，将请求地址设置为：",
                                },
                            ],
                        },
                        {
                            "component": "div",
                            "props": {
                                "class": "ml-4 mb-3 pa-2 rounded border text-body-2",
                                "style": (
                                    "border: 1px solid rgba(0,0,0,0.12); background: rgba(0,0,0,0.03); "
                                    "font-family: 'JetBrains Mono', Consolas, monospace; word-break: break-all;"
                                ),
                            },
                            "text": "http(s)://<你的MoviePilot地址>/api/v1/plugin/LarkMessager/webhook",
                        },
                        {
                            "component": "div",
                            "props": {"class": "mb-2"},
                            "content": [
                                {"component": "span", "text": "5. 添加 "},
                                {
                                    "component": "strong",
                                    "text": "im.message.receive_v1",
                                },
                                {"component": "span", "text": " 事件，"},
                                {
                                    "component": "strong",
                                    "props": {"class": "text-error"},
                                    "text": "⚠️ 然后逐个开通事件相关权限（默认未开通，不开通收不到消息！）",
                                },
                            ],
                        },
                        {
                            "component": "div",
                            "props": {"class": "mb-2"},
                            "content": [
                                {
                                    "component": "span",
                                    "text": "6. （推荐）开启加密策略，复制 ",
                                },
                                {"component": "strong", "text": "Encrypt Key"},
                                {"component": "span", "text": " 和 "},
                                {"component": "strong", "text": "Verification Token"},
                                {"component": "span", "text": " 填入插件配置。"},
                            ],
                        },
                        {
                            "component": "div",
                            "props": {"class": "mb-2"},
                            "content": [
                                {
                                    "component": "span",
                                    "text": "7. 发布应用版本，保存插件配置后点击 ",
                                },
                                {"component": "strong", "text": "发送测试消息"},
                                {
                                    "component": "span",
                                    "text": "，到 Lark 中查收测试卡片。",
                                },
                            ],
                        },
                        {
                            "component": "div",
                            "props": {"class": "mb-2"},
                            "content": [
                                {"component": "span", "text": "8. 在测试卡片中点击 "},
                                {"component": "strong", "text": "「点击确认」"},
                                {"component": "span", "text": " 按钮，系统将回复 "},
                                {"component": "strong", "text": "「测试确认成功」"},
                                {"component": "span", "text": " 消息。"},
                            ],
                        },
                    ],
                },
            ],
        }

        components = [
            *missing_card,
            {
                "component": "VCard",
                "props": {"class": "mb-4"},
                "content": [
                    {
                        "component": "VCardItem",
                        "content": [
                            {"component": "VCardTitle", "text": "LarkMessager"},
                            {
                                "component": "VCardSubtitle",
                                "text": "基于 Lark 开放平台应用的消息推送与交互",
                            },
                        ],
                    },
                    {
                        "component": "VCardText",
                        "content": [
                            {
                                "component": "VChip",
                                "props": {
                                    "color": status_color,
                                    "variant": "tonal",
                                    "prependIcon": status_icon,
                                    "size": "small",
                                },
                                "text": status_text,
                            },
                        ],
                    },
                ],
            },
            guide_card,
            {
                "component": "VCard",
                "props": {"class": "mb-4"},
                "content": [
                    {"component": "VCardTitle", "text": "操作"},
                    {
                        "component": "VCardText",
                        "content": [
                            {
                                "component": "VRow",
                                "content": [
                                    {
                                        "component": "VCol",
                                        "props": {"cols": 12, "md": 6},
                                        "content": [
                                            {
                                                "component": "VBtn",
                                                "props": {
                                                    "color": "primary",
                                                    "variant": "tonal",
                                                    "block": True,
                                                    "prependIcon": "mdi-send",
                                                    "size": "large",
                                                },
                                                "text": "发送测试消息",
                                                "events": {
                                                    "click": {
                                                        "api": "plugin/LarkMessager/test",
                                                        "method": "GET",
                                                        "params": {},
                                                    }
                                                },
                                            }
                                        ],
                                    },
                                    {
                                        "component": "VCol",
                                        "props": {"cols": 12, "md": 6},
                                        "content": [
                                            {
                                                "component": "VBtn",
                                                "props": {
                                                    "color": "secondary",
                                                    "variant": "tonal",
                                                    "block": True,
                                                    "prependIcon": "mdi-refresh",
                                                    "size": "large",
                                                },
                                                "text": "刷新状态",
                                                "events": {
                                                    "click": {
                                                        "api": "plugin/LarkMessager/status",
                                                        "method": "GET",
                                                        "params": {},
                                                    }
                                                },
                                            }
                                        ],
                                    },
                                    {
                                        "component": "VCol",
                                        "props": {"cols": 12, "md": 6},
                                        "content": [
                                            {
                                                "component": "VBtn",
                                                "props": {
                                                    "color": "info",
                                                    "variant": "tonal",
                                                    "block": True,
                                                    "prependIcon": "mdi-forum",
                                                    "size": "large",
                                                },
                                                "text": "获取已加入的群聊",
                                                "events": {
                                                    "click": {
                                                        "api": "plugin/LarkMessager/fetch_chats",
                                                        "method": "GET",
                                                        "params": {},
                                                    }
                                                },
                                            }
                                        ],
                                    },
                                ],
                            },
                        ],
                    },
                ],
            },
        ]

        # 测试结果反馈
        last_result = self.get_data("last_test_result")
        if last_result and not last_result.get("displayed", True):
            test_ok = last_result.get("ok", False)
            test_msg = last_result.get("msg", "")
            test_time = last_result.get("time", "")
            alert_text = (
                test_msg if not test_time else f"{test_msg}\n更新时间：{test_time}"
            )
            components.append(
                {
                    "component": "VCard",
                    "props": {"class": "mb-4"},
                    "content": [
                        {"component": "VCardTitle", "text": "测试结果"},
                        {
                            "component": "VCardText",
                            "content": [
                                {
                                    "component": "VAlert",
                                    "props": {
                                        "type": "success" if test_ok else "error",
                                        "variant": "tonal",
                                        "density": "compact",
                                        "icon": (
                                            "mdi-check-circle"
                                            if test_ok
                                            else "mdi-close-circle"
                                        ),
                                    },
                                    "text": alert_text,
                                }
                            ],
                        },
                    ],
                }
            )
            last_result["displayed"] = True
            self.save_data("last_test_result", last_result)

        # 群聊查询结果
        last_chats = self.get_data("last_chats")
        if last_chats and not last_chats.get("displayed", True):
            chats_ok = last_chats.get("ok", False)
            chats_msg = last_chats.get("msg", "")
            chats_time = last_chats.get("time", "")
            chats_alert_text = (
                chats_msg if not chats_time else f"{chats_msg}\n更新时间：{chats_time}"
            )
            components.append(
                {
                    "component": "VCard",
                    "props": {"class": "mb-4"},
                    "content": [
                        {
                            "component": "VCardTitle",
                            "props": {"prependIcon": "mdi-forum"},
                            "text": "群聊 Chat ID",
                        },
                        {
                            "component": "VCardText",
                            "content": [
                                {
                                    "component": "VAlert",
                                    "props": {
                                        "type": "success" if chats_ok else "warning",
                                        "variant": "tonal",
                                        "density": "compact",
                                        "icon": (
                                            "mdi-forum"
                                            if chats_ok
                                            else "mdi-alert-circle"
                                        ),
                                    },
                                    "text": chats_alert_text,
                                }
                            ],
                        },
                    ],
                }
            )
            last_chats["displayed"] = True
            self.save_data("last_chats", last_chats)

        return components

    # ------------------------------------------------------------------ #
    #  API 端点
    # ------------------------------------------------------------------ #
    def get_api(self) -> List[Dict[str, Any]]:
        return [
            {
                "path": "/webhook",
                "endpoint": self._webhook_endpoint,
                "methods": ["POST", "GET"],
                "allow_anonymous": True,
                "summary": "Lark事件回调 Webhook",
                "description": "接收Lark开放平台推送的事件，包括消息、按钮回调等。",
            },
            {
                "path": "/test",
                "endpoint": self._test_endpoint,
                "methods": ["GET"],
                "auth": "bear",
                "summary": "测试Lark连接",
                "description": "验证 App ID / App Secret 是否能正常获取 Token。",
            },
            {
                "path": "/status",
                "endpoint": self._status_endpoint,
                "methods": ["GET"],
                "auth": "bear",
                "summary": "查询插件运行状态",
                "description": "返回当前插件启用状态、App ID、连接状态等。",
            },
            {
                "path": "/fetch_chats",
                "endpoint": self._fetch_chats_endpoint,
                "methods": ["GET"],
                "auth": "bear",
                "summary": "调 Lark API 列出本应用已加入的群聊",
                "description": "返回群聊列表（Chat ID + 名字）。需要已配置 App ID / App Secret。",
            },
        ]

    # ================================================================== #
    #  Webhook 端点实现
    # ================================================================== #
    async def _webhook_endpoint(self, request: Request) -> Response:
        raw_body = await request.body()
        lark_headers = {
            k: v for k, v in request.headers.items() if k.lower().startswith("x-lark-")
        }
        body_preview = raw_body[:200].decode("utf-8", errors="replace")
        logger.info(
            "LarkMessager webhook 收到请求: method=%s, path=%s, lark_headers=%s, body_preview=%s",
            request.method,
            request.url.path,
            lark_headers,
            body_preview,
        )

        # 惰性初始化 crypto
        if self._encrypt_key and not self._crypto:
            self._crypto = LarkCrypto(self._encrypt_key, self._app_secret)
            logger.info("LarkMessager: webhook 惰性初始化 crypto（多 worker 兜底）")

        # 解析请求体
        is_encrypted = False
        try:
            body = json.loads(raw_body.decode("utf-8"))
        except Exception:
            raw_str = raw_body.decode("utf-8")
            if self._crypto and raw_str:
                try:
                    plaintext = self._crypto.decrypt(raw_str)
                    body = json.loads(plaintext)
                    is_encrypted = True
                except Exception as e:
                    logger.error("Webhook 解密失败：%s", e)
                    return JSONResponse({"error": "decrypt failed"}, status_code=400)
            else:
                return JSONResponse({"error": "invalid body"}, status_code=400)

        # 消息解密
        encrypt_data = body.get("encrypt") if isinstance(body, dict) else None
        if encrypt_data:
            if not self._crypto:
                logger.error("收到加密请求但插件未配置 Encrypt Key")
                return JSONResponse(
                    {
                        "error": "encrypt_key not configured",
                        "hint": "请在插件配置中填写 Encrypt Key",
                    },
                    status_code=400,
                )
            try:
                plaintext = self._crypto.decrypt(encrypt_data)
                body = json.loads(plaintext)
                is_encrypted = True
                logger.info("LarkMessager: 加密请求解密成功（encrypt_key 匹配）")
            except Exception as e:
                logger.error("Webhook encrypt 字段解密失败：%s", e)
                return JSONResponse({"error": "decrypt failed"}, status_code=400)

        # URL 验证（challenge）
        if isinstance(body, dict) and (
            "challenge" in body or body.get("type") == "url_verification"
        ):
            challenge = body.get("challenge", "")
            logger.info("Lark URL 验证请求，返回 challenge")
            return JSONResponse({"challenge": challenge})

        # 签名校验
        if self._encrypt_key and self._crypto:
            raw_event_type = ""
            if isinstance(body, dict):
                raw_event_type = body.get("event_type") or (
                    (body.get("header") or {}).get("event_type", "")
                )
            if is_encrypted:
                logger.info(
                    "加密请求解密成功，跳过 X-Lark-Signature 校验（event_type=%s）",
                    raw_event_type or "(unknown)",
                )
            elif raw_event_type == "card.action.trigger":
                logger.info("卡片回调跳过 X-Lark-Signature 校验")
            else:
                signature = request.headers.get("X-Lark-Signature", "")
                timestamp = request.headers.get("X-Lark-Request-Timestamp", "")
                nonce = request.headers.get("X-Lark-Request-Nonce", "")
                if not signature:
                    logger.warning("缺少 X-Lark-Signature 头")
                    return JSONResponse({"error": "missing signature"}, status_code=403)
                if not self._crypto.verify_signature(
                    signature, raw_body, timestamp, nonce
                ):
                    logger.warning("X-Lark-Signature 校验失败")
                    return JSONResponse(
                        {"error": "signature verification failed"}, status_code=403
                    )

        # Token 校验
        if self._verification_token:
            body_token = ""
            if isinstance(body, dict):
                body_token = body.get("token", "") or (
                    (body.get("header") or {}).get("token", "")
                )
            query_token = request.query_params.get("token", "")
            req_token = body_token or query_token
            if req_token != self._verification_token:
                logger.warning("Webhook token 校验失败")
                return JSONResponse(
                    {"error": "token verification failed"}, status_code=403
                )

        # 构造事件对象
        event = (
            LarkWebhookEvent(**body) if isinstance(body, dict) else LarkWebhookEvent()
        )
        event_type = event.event_type

        # 事件分发
        if event_type == "im.message.receive_v1":
            await self._handle_message_receive(event, body)
        elif event_type == "card.action.trigger":
            logger.info(
                "卡片回调原始事件体（解密后）: %s",
                json.dumps(body, ensure_ascii=False)[:2000],
            )
            await self._handle_card_action(event, body)
        elif event_type in (
            "im.message.message_read_v1",
            "im.message.reaction_created_v1",
            "im.message.reaction_deleted_v1",
            "im.message.recalled_v1",
            "im.chat.access_event.bot_p2p_chat_entered_v1",
        ):
            logger.debug("LarkMessager 静默处理事件：%s", event_type)
        else:
            logger.debug("LarkMessager 收到未注册事件类型：%s", event_type)

        return JSONResponse({"success": True})

    # ================================================================== #
    #  _handle_message_receive — 解析消息内容 → 构建 Feishu payload → 转发
    #  对标 Feishu._on_message + _parse_message_content
    # ================================================================== #
    async def _handle_message_receive(
        self, event: LarkWebhookEvent, raw_body: dict = None
    ):
        """处理用户发消息给机器人的事件，解析图片/音频/文件/富文本并转发。"""
        evt = event.event or {}
        message = evt.get("message", {}) or {}
        sender = evt.get("sender", {}) or {}

        sender_id_obj = sender.get("sender_id", {}) or {}
        if isinstance(sender_id_obj, dict):
            sender_open_id = sender_id_obj.get("open_id", "")
            sender_user_id = sender_id_obj.get("user_id", "")
        else:
            sender_open_id = str(sender_id_obj)
            sender_user_id = ""

        message_id = message.get("message_id", "")
        chat_id = message.get("chat_id", "")
        chat_type = message.get("chat_type", "")
        # 注意：接收事件(im.message.receive_v1)的字段是 message_type，
        # 而 msg_type 是「发送消息 API」的字段名，二者不可混用。
        message_type = message.get("message_type") or message.get("msg_type") or ""

        # 解析消息内容
        content_raw = message.get("content", {})
        if isinstance(content_raw, str):
            try:
                content = json.loads(content_raw)
            except Exception:
                content = {"raw": content_raw}
        else:
            content = content_raw or {}

        text = ""
        images = None
        audio_refs = None
        files = None

        if message_type == "text":
            text = content.get("text", "").strip() if isinstance(content, dict) else ""
        elif message_type == "image":
            image_key = (
                str(content.get("image_key") or "").strip()
                if isinstance(content, dict)
                else ""
            )
            if image_key:
                if message_id:
                    images = [{"ref": f"feishu://image/{message_id}/{image_key}"}]
                else:
                    images = [{"ref": f"feishu://image/{image_key}"}]
        elif message_type in ("audio", "media", "file"):
            file_key = (
                str(content.get("file_key") or "").strip()
                if isinstance(content, dict)
                else ""
            )
            file_name = (
                str(content.get("file_name") or "").strip()
                if isinstance(content, dict)
                else ""
            )
            if file_key:
                if message_type == "audio":
                    resource_path = (
                        f"{message_id}/{file_key}" if message_id else file_key
                    )
                    audio_refs = [
                        f"feishu://file/{resource_path}/{file_name or 'audio.opus'}"
                    ]
                else:
                    resource_path = (
                        f"{message_id}/{file_key}" if message_id else file_key
                    )
                    files = [
                        {
                            "ref": f"feishu://file/{resource_path}/{file_name or 'attachment'}",
                            "name": file_name or None,
                        }
                    ]
        elif message_type == "post":
            text, images = self._parse_post_content(content, message_id)

        # 记录用户/会话映射
        if self._client:
            self._client.remember_user_id_type(
                open_id=sender_open_id, user_id=sender_user_id
            )
            self._client.remember_target(
                userid=sender_open_id or sender_user_id, chat_id=chat_id
            )

        # 构建与内置飞书 _on_message 一致的 payload 格式
        payload = {
            "type": "message",
            "source": self._msg_source,
            "message_id": message_id,
            "chat_id": chat_id,
            "chat_type": chat_type,
            "message_type": message_type,
            "text": text,
            "images": images,
            "audio_refs": audio_refs,
            "files": files,
            "sender": {
                "open_id": sender_open_id,
                "user_id": sender_user_id,
                "name": sender_open_id or sender_user_id,
            },
        }
        self._forward_to_message_chain(payload)
        logger.info(
            "已转发 Lark 消息到 MessageChain：sender=%s, type=%s, text=%s",
            sender_open_id,
            message_type,
            text[:50],
        )

    # ================================================================== #
    #  _parse_post_content — 解析富文本消息（对标 Feishu._parse_post_message_content）
    # ================================================================== #
    @staticmethod
    def _parse_post_content(
        content: dict, message_id: str = ""
    ) -> Tuple[str, Optional[List[dict]]]:
        """从飞书富文本消息中提取文本和图片引用。"""
        # 找到 post body
        post_body = None
        if isinstance(content.get("content"), list):
            post_body = content
        post = content.get("post") if isinstance(content, dict) else None
        if isinstance(post, dict) and not post_body:
            for locale in ("zh_cn", "en_us", "ja_jp"):
                locale_body = post.get(locale)
                if isinstance(locale_body, dict):
                    post_body = locale_body
                    break
            if not post_body:
                for locale_body in post.values():
                    if isinstance(locale_body, dict):
                        post_body = locale_body
                        break

        if not post_body:
            return "", None

        lines = []
        title = str(post_body.get("title") or "").strip()
        if title:
            lines.append(title)

        images = []
        post_content = post_body.get("content")
        if isinstance(post_content, list):
            for row in post_content:
                if not isinstance(row, list):
                    continue
                row_parts = []
                for element in row:
                    if not isinstance(element, dict):
                        continue
                    tag = str(element.get("tag") or "").strip()
                    image_key = str(element.get("image_key") or "").strip()
                    if tag == "img" and image_key:
                        if message_id:
                            images.append(
                                {"ref": f"feishu://image/{message_id}/{image_key}"}
                            )
                        else:
                            images.append({"ref": f"feishu://image/{image_key}"})
                    # 解析元素文本
                    if tag in ("text", "plain_text"):
                        row_parts.append(
                            str(element.get("text") or element.get("content") or "")
                        )
                    elif tag == "a":
                        link_text = str(element.get("text") or "").strip()
                        href = str(
                            element.get("href") or element.get("url") or ""
                        ).strip()
                        if link_text and href and link_text != href:
                            row_parts.append(f"{link_text} {href}")
                        else:
                            row_parts.append(link_text or href)
                    elif tag == "at":
                        user_name = str(
                            element.get("user_name") or element.get("name") or ""
                        ).strip()
                        user_id = str(element.get("user_id") or "").strip()
                        target = user_name or user_id
                        if target:
                            row_parts.append(f" @{target}")
                    elif tag in ("code_block", "pre"):
                        code = str(
                            element.get("text") or element.get("content") or ""
                        ).strip()
                        language = str(element.get("language") or "").strip()
                        if code:
                            if language:
                                row_parts.append(f"```{language}\n{code}\n```")
                            else:
                                row_parts.append(f"```\n{code}\n```")
                    else:
                        row_parts.append(
                            str(element.get("text") or element.get("content") or "")
                        )
                row_text = "".join(row_parts).strip()
                if row_text:
                    lines.append(row_text)

        text = "\n".join(lines).strip()
        return text, images or None

    # ================================================================== #
    #  _handle_card_action — 构建 Feishu payload → 转发到 MessageChain
    #  对标 Feishu._on_card_action
    # ================================================================== #
    async def _handle_card_action(self, event: LarkWebhookEvent, raw_body: dict = None):
        """处理卡片按钮回调，构建 Feishu 兼容 payload 并转发。"""
        raw_body = raw_body or {}

        action = (event.action if event.action else None) or (event.event or {}).get(
            "action", {}
        )
        operator = (event.operator if event.operator else None) or (
            event.event or {}
        ).get("operator", {})

        # message_id 提取
        message_id = ""
        message_obj = event.message or {}
        if isinstance(message_obj, dict):
            message_id = message_obj.get("message_id", "")
        if not message_id:
            evt = event.event or {}
            if isinstance(evt.get("context"), dict):
                message_id = evt["context"].get("open_message_id", "")
            if not message_id and isinstance(evt.get("message"), dict):
                message_id = evt["message"].get("message_id", "")
        if not message_id:
            raw_evt = raw_body.get("event", {}) or {}
            if isinstance(raw_evt.get("context"), dict):
                message_id = raw_evt["context"].get("open_message_id", "")
            if not message_id and isinstance(raw_evt.get("message"), dict):
                message_id = raw_evt["message"].get("message_id", "")

        # chat_id 提取
        chat_id = ""
        if isinstance(raw_body.get("event"), dict):
            chat_id = (raw_body["event"].get("context") or {}).get(
                "open_chat_id", ""
            ) or raw_body["event"].get("chat_id", "")

        # 使用 extract_card_callback_data 提取 callback_data（对标 Feishu._extract_card_callback_data）
        action_value = ""
        action_tag = ""
        if isinstance(action, dict):
            action_tag = action.get("tag", "")
            value_raw = action.get("value", "")
            action_value = LarkClient.extract_card_callback_data(
                value_raw, action.get("name")
            )

        callback_data = action_value or action_tag

        # operator open_id
        operator_open_id = ""
        operator_user_id = ""
        if isinstance(operator, dict):
            operator_id_obj = operator.get("operator_id", {}) or {}
            if isinstance(operator_id_obj, dict):
                operator_open_id = operator_id_obj.get("open_id", "")
                operator_user_id = operator_id_obj.get("user_id", "")
            if not operator_open_id:
                operator_open_id = operator.get("open_id", "")

        logger.info(
            "收到卡片按钮回调：callback_data=%s, operator=%s",
            callback_data,
            operator_open_id,
        )

        # 记录用户/会话映射
        if self._client:
            self._client.remember_user_id_type(
                open_id=operator_open_id, user_id=operator_user_id
            )
            self._client.remember_target(
                userid=operator_open_id or operator_user_id, chat_id=chat_id
            )

        # 测试按钮：直接回复（插件内部逻辑）
        if callback_data == "test_ok" and self._client:
            try:
                self._client.reply_message(
                    message_id,
                    "✅ 测试确认成功！LarkMessager 插件卡片交互正常工作。",
                    msg_type="text",
                )
                logger.info("已回复测试卡片确认消息")
            except Exception as e:
                logger.error("回复测试卡片确认消息失败：%s", e)
            return

        # 构建与内置飞书 _on_card_action 一致的 payload
        payload = {
            "type": "cardAction",
            "source": self._msg_source,
            "message_id": message_id,
            "chat_id": chat_id,
            "callback_data": callback_data,
            "sender": {
                "open_id": operator_open_id,
                "user_id": operator_user_id,
                "name": operator_open_id or operator_user_id,
            },
        }
        self._forward_to_message_chain(payload)

    # ------------------------------------------------------------------ #
    #  推送目标解析（配置级别，用于测试端点）
    # ------------------------------------------------------------------ #
    def _resolve_targets(self, userid: str = "") -> tuple[list[tuple[str, str]], str]:
        targets: list[tuple[str, str]] = []
        seen: set[str] = set()
        warn_parts: list[str] = []

        def _add(rid: str, rtype: str):
            if rid and rid not in seen:
                seen.add(rid)
                targets.append((rid, rtype))

        if self._chat_id:
            _add(self._chat_id, "chat_id")

        if self._user_id:
            uid = self._user_id.strip()
            if uid.startswith("ou_"):
                _add(uid, "open_id")
            else:
                if self._client:
                    try:
                        parsed = False
                        if "@" in uid:
                            result = self._client.batch_get_id(emails=[uid])
                            parsed = True
                        elif uid.isdigit():
                            result = self._client.batch_get_id(mobiles=[uid])
                            parsed = True
                        else:
                            warn_parts.append(
                                f"用户解析失败：Lark 不支持通过工号/用户名（{uid}）查询用户。"
                                "请填写邮箱地址或手机号，或在 Lark 开放平台查看用户 Open ID 并填写 ou_xxx"
                            )
                        if parsed:
                            open_id = result.get(uid, "")
                            if open_id:
                                _add(open_id, "open_id")
                            else:
                                warn_parts.append(
                                    f"用户解析失败：找不到用户（{uid}），请检查邮箱/手机号是否正确"
                                )
                    except Exception as e:
                        error_msg = str(e)
                        if (
                            "99991672" in error_msg
                            or "contact:user.id:readonly" in error_msg
                        ):
                            warn_parts.append(
                                "用户解析失败：缺少权限 contact:user.id:readonly。"
                                "请在 Lark 开放平台 > 权限管理 开通此权限，并重新发布应用版本"
                            )
                        else:
                            warn_parts.append(f"用户解析失败：{error_msg}")
                else:
                    warn_parts.append("用户解析失败：插件未启用或 Client 未初始化")

        if userid:
            _add(userid, "chat_id" if userid.startswith("oc_") else "open_id")

        return targets, "；".join(warn_parts) if warn_parts else ""

    # ------------------------------------------------------------------ #
    #  /test 端点
    # ------------------------------------------------------------------ #
    def _test_endpoint(self, request: Request) -> JSONResponse:
        def _store(ok: bool, msg: str) -> dict:
            result = {
                "ok": ok,
                "msg": msg,
                "time": time.strftime("%Y-%m-%d %H:%M:%S"),
                "displayed": False,
            }
            self.save_data("last_test_result", result)
            return result

        if not self._client:
            return JSONResponse(
                _store(False, "插件未启用，或 App ID / App Secret 未配置")
            )

        targets, warn_msg = self._resolve_targets()
        if not targets:
            error_msg = (
                warn_msg if warn_msg else "未配置默认通知用户或群聊，请至少填一项"
            )
            return JSONResponse(_store(False, error_msg))

        try:
            from datetime import datetime

            now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
            # 构建精美测试卡片（schema 2.0，带 header 模板色 + 分隔线 + 字段布局）
            card = {
                "schema": "2.0",
                "config": {
                    "wide_screen_mode": True,
                    "enable_forward": True,
                    "update_multi": True,
                    "summary": {"content": "LarkMessager 连接测试"},
                },
                "header": {
                    "template": "blue",
                    "title": {
                        "tag": "plain_text",
                        "content": "LarkMessager 连接测试",
                    },
                },
                "body": {
                    "direction": "vertical",
                    "padding": "12px 12px 12px 12px",
                    "elements": [
                        {
                            "tag": "hr",
                        },
                        {
                            "tag": "column_set",
                            "flex_mode": "none:spread",
                            "background_style": "grey",
                            "columns": [
                                {
                                    "tag": "column",
                                    "width": "weighted",
                                    "weight": 1,
                                    "elements": [
                                        {
                                            "tag": "markdown",
                                            "content": "**状态**",
                                        },
                                        {
                                            "tag": "markdown",
                                            "content": "**连接正常**",
                                        },
                                    ],
                                },
                                {
                                    "tag": "column",
                                    "width": "weighted",
                                    "weight": 1,
                                    "elements": [
                                        {
                                            "tag": "markdown",
                                            "content": "**版本**",
                                        },
                                        {
                                            "tag": "markdown",
                                            "content": f"v{self.plugin_version}",
                                        },
                                    ],
                                },
                                {
                                    "tag": "column",
                                    "width": "weighted",
                                    "weight": 1,
                                    "elements": [
                                        {
                                            "tag": "markdown",
                                            "content": "**时间**",
                                        },
                                        {
                                            "tag": "markdown",
                                            "content": now,
                                        },
                                    ],
                                },
                            ],
                        },
                        {
                            "tag": "hr",
                        },
                        {
                            "tag": "markdown",
                            "content": (
                                "Lark 消息插件连接正常！这是一条测试卡片消息。"
                                "\n\n如果看到此消息并能正常交互，说明 **Webhook → 插件 → Lark API** "
                                "全链路已打通"
                            ),
                        },
                        {
                            "tag": "column_set",
                            "flex_mode": "none",
                            "columns": [
                                {
                                    "tag": "column",
                                    "width": "weighted",
                                    "weight": 1,
                                    "elements": [
                                        {
                                            "tag": "button",
                                            "text": {
                                                "tag": "plain_text",
                                                "content": "点击确认",
                                            },
                                            "type": "primary",
                                            "behaviors": [
                                                {
                                                    "type": "callback",
                                                    "value": {
                                                        "callback_data": "test_ok"
                                                    },
                                                }
                                            ],
                                        },
                                    ],
                                },
                                {
                                    "tag": "column",
                                    "width": "weighted",
                                    "weight": 1,
                                    "elements": [
                                        {
                                            "tag": "button",
                                            "text": {
                                                "tag": "plain_text",
                                                "content": "查看文档",
                                            },
                                            "type": "default",
                                            "behaviors": [
                                                {
                                                    "type": "open_url",
                                                    "default_url": "https://open.larksuite.com/document/home/",
                                                }
                                            ],
                                        },
                                    ],
                                },
                            ],
                        },
                    ],
                },
            }
            sent_ids = []
            for rid, rid_type in targets:
                mid = self._client.send_card(rid, card, rid_type)
                sent_ids.append(mid)
            logger.info(
                "LarkMessager 测试消息已发送 %d 条，message_ids=%s",
                len(sent_ids),
                sent_ids,
            )
            ok_msg = f"测试消息已发送 {len(sent_ids)} 条，请到 Lark 查收（message_ids={sent_ids}）"
            if warn_msg:
                ok_msg += f"；注意：{warn_msg}"
            return JSONResponse(_store(True, ok_msg))
        except Exception as e:
            logger.error("LarkMessager 发送测试消息失败：%s", e)
            return JSONResponse(_store(False, f"发送失败：{e}"))

    # ------------------------------------------------------------------ #
    #  /status 端点
    # ------------------------------------------------------------------ #
    def _status_endpoint(self, request: Request) -> JSONResponse:
        self.del_data("last_test_result")
        self.del_data("last_chats")
        return JSONResponse(
            {
                "enabled": self._enabled,
                "app_id": self._app_id[:8] + "..." if self._app_id else "",
                "has_client": self._client is not None,
                "has_crypto": self._crypto is not None,
                "admin_count": len(self._admin_users),
                "chat_id": self._chat_id[:8] + "..." if self._chat_id else "",
            }
        )

    # ------------------------------------------------------------------ #
    #  /fetch_chats 端点
    # ------------------------------------------------------------------ #
    def _fetch_chats_endpoint(self, request: Request) -> JSONResponse:
        if not self._app_id or not self._app_secret:
            result = {
                "ok": False,
                "msg": "请先在插件配置中填写 App ID 和 App Secret 并保存。",
                "chats": [],
            }
        else:
            try:
                client = LarkClient(self._app_id, self._app_secret)
                url = f"{API_BASE}/im/v1/chats"
                params = {"user_id_type": "open_id", "page_size": 100}
                resp = requests.get(
                    url, headers=client._headers(), params=params, timeout=10
                )
                data = resp.json()
                if data.get("code") != 0:
                    result = {
                        "ok": False,
                        "msg": f"Lark API 错误：{data.get('msg')}（code={data.get('code')}）",
                        "chats": [],
                    }
                else:
                    items = (data.get("data") or {}).get("items") or []
                    chats = [
                        {
                            "chat_id": it.get("chat_id", ""),
                            "name": it.get("name", "(未命名)"),
                            "description": it.get("description", ""),
                        }
                        for it in items
                    ]
                    if not chats:
                        result = {
                            "ok": False,
                            "msg": "Lark 返回空列表。请确认：本应用已被添加为群聊机器人。",
                            "chats": [],
                        }
                    else:
                        lines = [
                            f"- {c['name']} ({c['chat_id']})"
                            + (
                                f"\n  {c['description']}"
                                if c.get("description")
                                else ""
                            )
                            for c in chats
                        ]
                        result = {
                            "ok": True,
                            "msg": f"共 {len(chats)} 个群聊：\n" + "\n".join(lines),
                            "chats": chats,
                        }
            except Exception as e:
                logger.error("LarkMessager fetch_chats 失败：%s", e)
                result = {"ok": False, "msg": f"请求失败：{e}", "chats": []}

        import datetime

        result["time"] = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        result["displayed"] = False
        self.save_data("last_chats", result)
        return JSONResponse(result)

    # ------------------------------------------------------------------ #
    #  管理员标识解析与通知配置同步（供 AI 智能助手识别）
    # ------------------------------------------------------------------ #
    def _start_admin_sync(self):
        """
        在后台线程中解析管理员标识并同步到通知配置。
        用线程而非同步解析，避免 init_plugin 在启动时因网络请求阻塞。
        """
        if not self._enabled:
            return
        if not self._client:
            logger.debug("LarkMessager 未启用或 client 未就绪，跳过管理员同步")
            return
        worker = threading.Thread(target=self._admin_sync_worker, daemon=True)
        worker.start()

    def _admin_sync_worker(self):
        """
        后台工作线程：解析所有管理员标识为 Open ID，并写回
        SYSTEMCONFIG.Notifications，供 AI 智能助手的渠道管理员判定读取。
        解析失败时重试一次，避免瞬断导致管理员遗漏。
        """
        client = self._client
        if not client:
            return
        try:
            admin_openids, default_open_id = self._resolve_admin_identifiers()
            # 解析失败时重试一次（瞬断保护）
            if (self._admin_emails or self._admin_mobiles or self._admin_openids_direct) \
                    and not admin_openids:
                time.sleep(5)
                if not self._client:
                    return
                admin_openids, default_open_id = self._resolve_admin_identifiers()
            if default_open_id and default_open_id != self._user_id_raw:
                self._user_id = default_open_id
            self._sync_admin_to_notification_config(admin_openids, default_open_id)
        except Exception as e:
            logger.error("LarkMessager 管理员同步失败：%s", e)

    def _resolve_single(self, identifier: str) -> str:
        """
        用 Lark 通讯录接口把单个邮箱/手机号解析为 Open ID。
        已是 ou_xxx 的直接返回；解析失败返回空串。
        """
        if not self._client or not identifier:
            return ""
        identifier = identifier.strip()
        if not identifier:
            return ""
        if identifier.startswith("ou_"):
            return identifier
        try:
            if "@" in identifier:
                result = self._client.batch_get_id(emails=[identifier])
            else:
                # 手机号（允许 +86 等前缀）；非邮箱、非 Open ID 的都按手机号尝试
                result = self._client.batch_get_id(mobiles=[identifier])
            return (result or {}).get(identifier, "") or ""
        except Exception as e:
            logger.warning(
                "LarkMessager 解析管理员标识失败：%s, error=%s", identifier, e
            )
            return ""

    def _resolve_admin_identifiers(self) -> Tuple[List[str], str]:
        """
        将管理员输入框（FEISHU_ADMINS，混填邮箱/手机号/Open ID）统一解析为
        Open ID 列表，并一并解析默认通知用户（FEISHU_OPEN_ID，可能也是
        邮箱/手机号）。返回 (管理员 Open ID 列表去重, 解析后的默认用户 Open ID 或 "")。
        """
        admin_ids: List[str] = []
        # 1. 直接填的 Open ID
        for uid in self._admin_openids_direct:
            if uid.startswith("ou_"):
                admin_ids.append(uid)
            else:
                rid = self._resolve_single(uid)
                if rid:
                    admin_ids.append(rid)
        # 2. 邮箱
        for email in self._admin_emails:
            rid = self._resolve_single(email)
            if rid:
                admin_ids.append(rid)
        # 3. 手机号
        for mobile in self._admin_mobiles:
            rid = self._resolve_single(mobile)
            if rid:
                admin_ids.append(rid)
        # 4. 默认用户
        default_open_id = ""
        if self._user_id_raw:
            if self._user_id_raw.startswith("ou_"):
                default_open_id = self._user_id_raw
            else:
                default_open_id = self._resolve_single(self._user_id_raw) or self._user_id_raw
        # 去重保序
        return list(dict.fromkeys(admin_ids)), default_open_id

    def _sync_admin_to_notification_config(self, admin_openids: List[str], default_open_id: str):
        """
        将解析后的管理员 Open ID 写回 SYSTEMCONFIG.Notifications 中名为配置源
        （self._msg_source，即 "Lark"）的通知配置。

        关键点：AI 智能助手的渠道管理员判定
        （app/agent/tools/base.py 的 _has_channel_admin_permission）只读取
        SYSTEMCONFIG.Notifications 里 NotificationConf.config["FEISHU_ADMINS"]，
        并按发送者 Open ID 精确比对；它并不会解析邮箱，也不会读取插件
        自身的 plugin.LarkMessager 配置。因此必须把 Open ID 写到
        Notifications 才会生效。

        为避免干扰通知子系统，该条配置使用非标准 type（larkmessager）
        且 enabled=False——智能助手读取时不区分 type/enabled，照常命中；
        而通知服务枚举（按 type+enabled 过滤）会忽略它。
        """
        try:
            configs = self.systemconfig.get(SystemConfigKey.Notifications)
            if not isinstance(configs, list):
                configs = []
            target = None
            for c in configs:
                if isinstance(c, dict) and c.get("name") == self._msg_source:
                    target = c
                    break
            admin_str = ",".join(sorted(set(admin_openids))) if admin_openids else ""
            # 没有任何管理员、也没有默认用户时，不创建空配置；
            # 若已存在则清空 FEISHU_ADMINS，使智能助手停止识别。
            if not admin_str and not default_open_id:
                if target and isinstance(target.get("config"), dict) and (
                    target["config"].get("FEISHU_ADMINS")
                    or target["config"].get("FEISHU_OPEN_ID")
                ):
                    target["config"]["FEISHU_ADMINS"] = ""
                    if "FEISHU_OPEN_ID" in target["config"]:
                        target["config"]["FEISHU_OPEN_ID"] = ""
                    self.systemconfig.set(SystemConfigKey.Notifications, configs)
                    logger.info("LarkMessager 已清空管理员 Open ID 同步配置")
                return
            # 变更守卫：与现有值一致则不写回，避免触发 CONFIG_WATCH 重载造成死循环
            if target:
                cfg = target.get("config")
                if not isinstance(cfg, dict):
                    cfg = {}
                    target["config"] = cfg
                if cfg.get("FEISHU_ADMINS") == admin_str and (
                    not default_open_id or cfg.get("FEISHU_OPEN_ID") == default_open_id
                ):
                    return
                cfg["FEISHU_ADMINS"] = admin_str
                if default_open_id:
                    cfg["FEISHU_OPEN_ID"] = default_open_id
            else:
                target = {
                    "name": self._msg_source,
                    "type": "larkmessager",
                    "config": {"FEISHU_ADMINS": admin_str},
                    "switchs": [],
                    "enabled": False,
                }
                if default_open_id:
                    target["config"]["FEISHU_OPEN_ID"] = default_open_id
                configs.append(target)
            self.systemconfig.set(SystemConfigKey.Notifications, configs)
            logger.info(
                "LarkMessager 已同步 %d 个管理员 Open ID 到通知配置：%s",
                len(admin_openids), admin_str,
            )
        except Exception as e:
            logger.error("LarkMessager 写回通知配置失败：%s", e)

    # ------------------------------------------------------------------ #
    #  管理员权限校验（邮箱/手机号解析版）
    # ------------------------------------------------------------------ #
    def _is_admin(self, open_id: str) -> bool:
        if not self._admin_users:
            return True
        if open_id in self._admin_users:
            return True
        if not self._client:
            return False
        for identifier in self._admin_users:
            if identifier.startswith("ou_"):
                continue
            if identifier in self._admin_users_resolved:
                if self._admin_users_resolved[identifier] == open_id:
                    return True
                continue
            try:
                if "@" in identifier:
                    result = self._client.batch_get_id(emails=[identifier])
                else:
                    result = self._client.batch_get_id(mobiles=[identifier])
                resolved_open_id = result.get(identifier, "")
                self._admin_users_resolved[identifier] = resolved_open_id
                if resolved_open_id == open_id:
                    return True
            except Exception as e:
                logger.warning("解析管理员标识失败：%s, error=%s", identifier, e)
                continue
        return False
