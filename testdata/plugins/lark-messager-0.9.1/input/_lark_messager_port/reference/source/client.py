"""
LarkMessager Lark API 客户端
Lark开放平台 API 文档：https://open.larksuite.com/document/server-docs

对标内置飞书模块 app/modules/feishu/feishu.py 的 Feishu 类，
使用纯 HTTP REST API 实现，不依赖 lark-oapi SDK。
"""
import base64
import json
import re
import tempfile
import uuid
from pathlib import Path
from typing import Optional, Dict, Any, List, Tuple
from urllib.parse import urlparse

import requests

from app.log import logger


# Lark开放平台 API 地址
API_BASE = "https://open.larksuite.com/open-apis"


class LarkClient:
    """Lark应用 API 客户端（对标内置 Feishu 类）"""

    # —— 常量（对标 Feishu 类） —— #
    PROCESSING_REACTION_EMOJI = "GLANCE"
    STREAM_CARD_TITLE_ELEMENT_ID = "mp_stream_title"
    STREAM_CARD_BODY_ELEMENT_ID = "mp_stream_body"
    IMAGE_SUFFIXES = {".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".tiff", ".heic"}
    MARKDOWN_IMAGE_PATTERN = re.compile(r"!\[(?P<alt>[^\]\n]*)]\((?P<target>[^)\n]*)\)")

    def __init__(self, app_id: str, app_secret: str):
        self._app_id = app_id
        self._app_secret = app_secret
        self._token: str = ""
        self._token_expire: int = 0
        # 用户/会话记忆（对标 Feishu._user_chat_mapping / _user_receive_id_type_mapping）
        self._user_chat_mapping: Dict[str, str] = {}
        self._user_receive_id_type_mapping: Dict[str, str] = {}
        self._chat_open_mapping: Dict[str, str] = {}

    # ------------------------------------------------------------------ #
    #  Token 管理
    # ------------------------------------------------------------------ #
    def _get_tenant_access_token(self, force: bool = False) -> str:
        now = int(__import__("time").time())
        if self._token and not force and self._token_expire - now > 60:
            return self._token
        url = f"{API_BASE}/auth/v3/tenant_access_token/internal"
        payload = {"app_id": self._app_id, "app_secret": self._app_secret}
        resp = requests.post(url, json=payload, timeout=10)
        resp.raise_for_status()
        data = resp.json()
        if data.get("code") != 0:
            raise RuntimeError(f"获取 tenant_access_token 失败：{data.get('msg')}")
        self._token = data["tenant_access_token"]
        self._token_expire = now + data.get("expire", 7200)
        return self._token

    def _headers(self, content_type: str = "application/json") -> Dict[str, str]:
        token = self._get_tenant_access_token()
        return {
            "Authorization": f"Bearer {token}",
            "Content-Type": content_type,
        }

    # ------------------------------------------------------------------ #
    #  用户/会话记忆（对标 Feishu._remember_target / _remember_user_id_type）
    # ------------------------------------------------------------------ #
    def remember_target(self, userid: Optional[str], chat_id: Optional[str]) -> None:
        normalized_userid = (userid or "").strip()
        normalized_chat_id = (chat_id or "").strip()
        if not normalized_userid or not normalized_chat_id:
            return
        self._user_chat_mapping[normalized_userid] = normalized_chat_id
        self._chat_open_mapping[normalized_chat_id] = normalized_userid

    def remember_user_id_type(
        self, open_id: Optional[str] = None, user_id: Optional[str] = None
    ) -> None:
        if open_id:
            self._user_receive_id_type_mapping[open_id.strip()] = "open_id"
        if user_id:
            self._user_receive_id_type_mapping[user_id.strip()] = "user_id"

    # ------------------------------------------------------------------ #
    #  目标解析（对标 Feishu._resolve_target）
    # ------------------------------------------------------------------ #
    def resolve_target(
        self,
        userid: Optional[str] = None,
        chat_id: Optional[str] = None,
        receive_id_type: Optional[str] = None,
        default_open_id: Optional[str] = None,
        default_chat_id: Optional[str] = None,
    ) -> Tuple[str, str]:
        """解析 Lark 发送目标，优先走显式用户，其次回退默认配置。"""
        resolved_userid = (userid or "").strip() or None
        resolved_chat_id = (chat_id or "").strip() or None
        normalized_receive_id_type = (receive_id_type or "").strip() or None
        if not resolved_userid and not resolved_chat_id:
            resolved_userid = default_open_id
            resolved_chat_id = default_chat_id
            if resolved_userid and not normalized_receive_id_type:
                normalized_receive_id_type = "open_id"
        if normalized_receive_id_type == "chat_id" and resolved_chat_id:
            return resolved_chat_id, "chat_id"
        if resolved_userid:
            if normalized_receive_id_type in {"open_id", "user_id"}:
                return resolved_userid, normalized_receive_id_type
            remembered_type = self._user_receive_id_type_mapping.get(resolved_userid)
            return resolved_userid, remembered_type or "open_id"
        if resolved_chat_id:
            return resolved_chat_id, "chat_id"
        raise ValueError("未找到可发送的 Lark 目标")

    # ------------------------------------------------------------------ #
    #  消息发送 / 回复（底层 API 封装）
    # ------------------------------------------------------------------ #
    def _send_message(
        self, receive_id: str, receive_id_type: str, msg_type: str, content: dict
    ) -> Optional[dict]:
        """调用 Lark IM API 发送消息，返回统一结果结构。"""
        url = f"{API_BASE}/im/v1/messages"
        params = {"receive_id_type": receive_id_type}
        payload = {
            "receive_id": receive_id,
            "msg_type": msg_type,
            "content": json.dumps(content, ensure_ascii=False),
            "uuid": str(uuid.uuid4()),
        }
        resp = requests.post(url, params=params, headers=self._headers(), json=payload, timeout=15)
        data = resp.json()
        if data.get("code") != 0:
            logger.error("Lark 消息发送失败：code=%s, msg=%s", data.get("code"), data.get("msg"))
            return None
        msg_data = data.get("data", {})
        return {
            "success": True,
            "message_id": msg_data.get("message_id"),
            "chat_id": msg_data.get("chat_id"),
            "msg_type": msg_data.get("msg_type"),
        }

    def _reply_message(
        self,
        message_id: str,
        msg_type: str,
        content: dict,
        reply_in_thread: bool = False,
    ) -> Optional[dict]:
        """按原消息回复，保持 Lark 会话中的引用关系。"""
        if not message_id:
            raise RuntimeError("回复消息失败：message_id 为空")
        url = f"{API_BASE}/im/v1/messages/{message_id}/reply"
        payload = {
            "msg_type": msg_type,
            "content": json.dumps(content, ensure_ascii=False),
            "uuid": str(uuid.uuid4()),
        }
        resp = requests.post(url, headers=self._headers(), json=payload, timeout=15)
        data = resp.json()
        if data.get("code") != 0:
            logger.error("Lark 回复消息失败：code=%s, msg=%s", data.get("code"), data.get("msg"))
            return None
        msg_data = data.get("data", {})
        return {
            "success": True,
            "message_id": msg_data.get("message_id"),
            "chat_id": msg_data.get("chat_id"),
            "msg_type": msg_data.get("msg_type"),
            "root_id": msg_data.get("root_id"),
            "parent_id": msg_data.get("parent_id"),
            "thread_id": msg_data.get("thread_id"),
        }

    # ------------------------------------------------------------------ #
    #  高层发送方法（对标 Feishu.send_text / send_file / send_voice / send_notification）
    # ------------------------------------------------------------------ #
    def send_text(
        self,
        text: str,
        userid: Optional[str] = None,
        chat_id: Optional[str] = None,
        receive_id_type: Optional[str] = None,
        original_message_id: Optional[str] = None,
        default_open_id: Optional[str] = None,
        default_chat_id: Optional[str] = None,
    ) -> Optional[dict]:
        """发送纯文本消息。"""
        try:
            if original_message_id:
                result = self._reply_message(
                    message_id=original_message_id,
                    msg_type="text",
                    content={"text": text},
                )
            else:
                receive_id, resolved_type = self.resolve_target(
                    userid=userid, chat_id=chat_id, receive_id_type=receive_id_type,
                    default_open_id=default_open_id, default_chat_id=default_chat_id,
                )
                result = self._send_message(receive_id, resolved_type, "text", {"text": text})
        except Exception as err:
            logger.error(f"Lark 文本消息发送失败：{err}")
            return {"success": False}

        if not result:
            return {"success": False}
        result["chat_id"] = result.get("chat_id") or chat_id or self._user_chat_mapping.get(
            userid or "") or default_chat_id
        return result

    def send_file(
        self,
        file_path: str,
        userid: Optional[str] = None,
        chat_id: Optional[str] = None,
        title: Optional[str] = None,
        text: Optional[str] = None,
        file_name: Optional[str] = None,
        receive_id_type: Optional[str] = None,
        original_message_id: Optional[str] = None,
        default_open_id: Optional[str] = None,
        default_chat_id: Optional[str] = None,
    ) -> Optional[dict]:
        """发送本地图片或文件（对标 Feishu.send_file）。"""
        local_file = Path(file_path)
        if not local_file.exists() or not local_file.is_file():
            logger.error(f"Lark 附件不存在：{local_file}")
            return {"success": False}

        suffix = local_file.suffix.lower()
        is_image = suffix in self.IMAGE_SUFFIXES
        try:
            if is_image:
                image_key = self.upload_image(str(local_file))
                if not image_key:
                    return {"success": False}
                payload = self.build_card_v2(
                    title=title, text=text, link=None, buttons=None, image_key=image_key,
                )
                if original_message_id:
                    result = self._reply_message(
                        message_id=original_message_id, msg_type="interactive", content=payload,
                    )
                else:
                    receive_id, resolved_type = self.resolve_target(
                        userid=userid, chat_id=chat_id, receive_id_type=receive_id_type,
                        default_open_id=default_open_id, default_chat_id=default_chat_id,
                    )
                    result = self._send_message(receive_id, resolved_type, "interactive", payload)
            else:
                file_key = self.upload_file(str(local_file), file_name=file_name)
                if not file_key:
                    return {"success": False}
                if original_message_id:
                    result = self._reply_message(
                        message_id=original_message_id, msg_type="file", content={"file_key": file_key},
                    )
                else:
                    receive_id, resolved_type = self.resolve_target(
                        userid=userid, chat_id=chat_id, receive_id_type=receive_id_type,
                        default_open_id=default_open_id, default_chat_id=default_chat_id,
                    )
                    result = self._send_message(receive_id, resolved_type, "file", {"file_key": file_key})
            if result and (title or text) and not is_image:
                self.send_text(
                    self._build_message_text(title=title, text=text),
                    userid=userid, chat_id=chat_id, receive_id_type=receive_id_type,
                    original_message_id=original_message_id,
                    default_open_id=default_open_id, default_chat_id=default_chat_id,
                )
        except Exception as err:
            logger.error(f"Lark 附件发送失败：{err}")
            return {"success": False}

        if not result:
            return {"success": False}
        result["chat_id"] = result.get("chat_id") or chat_id or self._user_chat_mapping.get(
            userid or "") or default_chat_id
        return result

    def send_voice(
        self,
        voice_path: str,
        userid: Optional[str] = None,
        chat_id: Optional[str] = None,
        caption: Optional[str] = None,
        receive_id_type: Optional[str] = None,
        original_message_id: Optional[str] = None,
        default_open_id: Optional[str] = None,
        default_chat_id: Optional[str] = None,
    ) -> Optional[dict]:
        """发送 Lark 语音消息（对标 Feishu.send_voice）。"""
        local_file = Path(voice_path)
        if not local_file.exists() or not local_file.is_file():
            logger.error(f"Lark 语音文件不存在：{local_file}")
            return {"success": False}

        try:
            file_key = self.upload_file(str(local_file), file_name=local_file.name)
            if not file_key:
                return {"success": False}
            if original_message_id:
                result = self._reply_message(
                    message_id=original_message_id, msg_type="audio", content={"file_key": file_key},
                )
            else:
                receive_id, resolved_type = self.resolve_target(
                    userid=userid, chat_id=chat_id, receive_id_type=receive_id_type,
                    default_open_id=default_open_id, default_chat_id=default_chat_id,
                )
                result = self._send_message(receive_id, resolved_type, "audio", {"file_key": file_key})
            if result and caption:
                self.send_text(
                    caption, userid=userid, chat_id=chat_id, receive_id_type=receive_id_type,
                    original_message_id=original_message_id,
                    default_open_id=default_open_id, default_chat_id=default_chat_id,
                )
        except Exception as err:
            logger.error(f"Lark 语音消息发送失败：{err}")
            return {"success": False}

        if not result:
            return {"success": False}
        result["chat_id"] = result.get("chat_id") or chat_id or self._user_chat_mapping.get(
            userid or "") or default_chat_id
        return result

    def send_notification(
        self,
        message,  # Notification 对象
        userid: Optional[str] = None,
        chat_id: Optional[str] = None,
        receive_id_type: Optional[str] = None,
        original_message_id: Optional[str] = None,
        default_open_id: Optional[str] = None,
        default_chat_id: Optional[str] = None,
        image_items: Optional[List[Dict[str, Any]]] = None,
    ) -> Optional[dict]:
        """发送通知消息（对标 Feishu.send_notification）。

        image_items: 可选的图文列表项，每项 {"title": str, "url": str}，
            用于媒体/种子选择列表场景，将每个条目的海报图上传并渲染到卡片。
        """
        from app.schemas.types import NotificationType

        is_streaming_agent_text = (
            message.mtype == NotificationType.Agent
            and not message.buttons
            and not message.link
        )
        if is_streaming_agent_text:
            try:
                stream_image_urls = []
                if self._is_external_image_url(message.image):
                    stream_image_urls.append(message.image)
                stream_image_urls.extend(self._extract_markdown_image_urls(message.text))
                result = self._send_streaming_card_message(
                    title=message.title, text=message.text,
                    userid=userid, chat_id=chat_id, receive_id_type=receive_id_type,
                    original_message_id=original_message_id,
                    default_open_id=default_open_id, default_chat_id=default_chat_id,
                )
            except Exception as err:
                logger.warning(f"Lark 流式卡片发送失败：{err}")
                return {"success": False}
            if not result:
                return {"success": False}
            result["chat_id"] = result.get("chat_id") or chat_id or self._user_chat_mapping.get(
                userid or "") or default_chat_id
            sent_image_urls = self._send_agent_streaming_images(
                stream_image_urls, userid=userid,
                chat_id=result.get("chat_id") or chat_id, receive_id_type=receive_id_type,
                default_open_id=default_open_id, default_chat_id=default_chat_id,
            )
            stream_meta = result.get("metadata", {}).get("feishu_streaming")
            if isinstance(stream_meta, dict):
                stream_meta["sent_image_urls"] = sent_image_urls
            return result

        # 图文列表场景（媒体/种子列表）：将每个条目的海报图上传并渲染到卡片
        image_items_with_keys = None
        if image_items:
            built_items = []
            for item in image_items:
                if not isinstance(item, dict):
                    continue
                url = item.get("url")
                key = self._upload_remote_image(url) if url else None
                built = {"title": item.get("title") or ""}
                if key:
                    built["image_key"] = key
                built_items.append(built)
            if built_items:
                image_items_with_keys = built_items

        image_key = None if image_items_with_keys else self._upload_remote_image(message.image)
        header_template = self._header_template_for_mtype(
            message.mtype.value if message.mtype else None
        )
        payload = self.build_card_v2(
            title=message.title, text=message.text, link=message.link,
            buttons=message.buttons, image_key=image_key,
            header_template=header_template,
            image_items=image_items_with_keys,
        )
        try:
            if original_message_id:
                result = self._reply_message(
                    message_id=original_message_id, msg_type="interactive", content=payload,
                )
            else:
                receive_id, resolved_type = self.resolve_target(
                    userid=userid, chat_id=chat_id, receive_id_type=receive_id_type,
                    default_open_id=default_open_id, default_chat_id=default_chat_id,
                )
                result = self._send_message(receive_id, resolved_type, "interactive", payload)
        except Exception as err:
            logger.error(f"Lark 通知发送失败：{err}")
            return {"success": False}

        if not result:
            return {"success": False}
        result["chat_id"] = result.get("chat_id") or chat_id or self._user_chat_mapping.get(
            userid or "") or default_chat_id
        return result

    # ------------------------------------------------------------------ #
    #  消息编辑（对标 Feishu.edit_message）
    # ------------------------------------------------------------------ #
    def edit_message(
        self,
        message_id: str,
        title: Optional[str] = None,
        text: Optional[str] = None,
        buttons: Optional[List[List[dict]]] = None,
        metadata: Optional[dict] = None,
        chat_id: Optional[str] = None,
    ) -> bool:
        """编辑已发送的 Lark 交互卡片消息。"""
        if not message_id:
            return False

        # 流式卡片更新
        stream_meta = (metadata or {}).get("feishu_streaming") if isinstance(metadata, dict) else None
        if isinstance(stream_meta, dict) and not buttons:
            card_id = str(stream_meta.get("card_id") or "").strip()
            element_id = str(stream_meta.get("element_id") or self.STREAM_CARD_BODY_ELEMENT_ID).strip()
            sequence = int(stream_meta.get("sequence") or 0) + 1
            stream_meta["sequence"] = sequence

            if card_id and element_id:
                content = self._escape_card_text(
                    self._strip_streaming_markdown_images(text)
                ).strip()
                if self._update_streaming_card_content(
                    card_id=card_id, element_id=element_id, content=content or " ", sequence=sequence,
                ):
                    stream_image_urls = self._extract_markdown_image_urls(text)
                    stream_meta["sent_image_urls"] = self._send_agent_streaming_images(
                        stream_image_urls, chat_id=chat_id,
                        sent_image_urls=stream_meta.get("sent_image_urls") or [],
                    )
                    return True
                logger.error("Lark 流式更新失败被拦截，返回 False 以防止降级")
                return False

        # 普通卡片编辑
        card = self.build_card_v2(title=title, text=text, link=None, buttons=buttons)
        url = f"{API_BASE}/im/v1/messages/{message_id}"
        try:
            resp = requests.patch(
                url, headers=self._headers(),
                json={"content": json.dumps(card, ensure_ascii=False)},
                timeout=15,
            )
            data = resp.json()
            if data.get("code") == 0:
                return True
            logger.error("Lark 消息更新失败：code=%s, msg=%s", data.get("code"), data.get("msg"))
        except Exception as err:
            logger.error(f"Lark 消息更新失败：{err}")
        return False

    # ------------------------------------------------------------------ #
    #  表情回应（对标 Feishu.add_message_reaction / delete_message_reaction）
    # ------------------------------------------------------------------ #
    def add_message_reaction(self, message_id: str, emoji_type: str) -> Optional[str]:
        """为指定消息添加表情回应，返回 reaction_id。"""
        if not message_id or not emoji_type:
            return None
        url = f"{API_BASE}/im/v1/messages/{message_id}/reactions"
        payload = {"reaction_type": {"emoji_type": emoji_type}}
        try:
            resp = requests.post(url, headers=self._headers(), json=payload, timeout=10)
            data = resp.json()
            if data.get("code") != 0:
                logger.error("Lark 表情添加失败：message_id=%s, emoji=%s, code=%s, msg=%s",
                             message_id, emoji_type, data.get("code"), data.get("msg"))
                return None
            return data.get("data", {}).get("reaction_id")
        except Exception as err:
            logger.error(f"Lark 表情添加失败：{err}")
            return None

    def delete_message_reaction(self, message_id: str, reaction_id: str) -> bool:
        """删除指定消息上的表情回应。"""
        if not message_id or not reaction_id:
            return False
        url = f"{API_BASE}/im/v1/messages/{message_id}/reactions/{reaction_id}"
        try:
            resp = requests.delete(url, headers=self._headers(), timeout=10)
            data = resp.json()
            if data.get("code") == 0:
                return True
            logger.error("Lark 表情删除失败：message_id=%s, reaction_id=%s, code=%s, msg=%s",
                         message_id, reaction_id, data.get("code"), data.get("msg"))
        except Exception as err:
            logger.error(f"Lark 表情删除失败：{err}")
        return False

    # ------------------------------------------------------------------ #
    #  资源下载（对标 Feishu.download_image_bytes / download_file_bytes / download_message_resource_bytes）
    # ------------------------------------------------------------------ #
    def download_image_bytes(self, image_key: str) -> Optional[Tuple[bytes, Optional[str], Optional[str]]]:
        """下载 Lark 图片，返回 (bytes, file_name, content_type)。"""
        if not image_key:
            return None
        url = f"{API_BASE}/im/v1/images/{image_key}"
        try:
            resp = requests.get(url, headers=self._headers(), timeout=15)
            if resp.status_code != 200:
                return None
            content_type = resp.headers.get("Content-Type")
            return resp.content, None, content_type
        except Exception as err:
            logger.error(f"Lark 图片下载失败：{err}")
            return None

    def download_file_bytes(self, file_key: str) -> Optional[Tuple[bytes, Optional[str], Optional[str]]]:
        """下载 Lark 文件，返回 (bytes, file_name, content_type)。"""
        if not file_key:
            return None
        url = f"{API_BASE}/im/v1/files/{file_key}?file_type=stream"
        try:
            resp = requests.get(url, headers=self._headers(), timeout=60)
            if resp.status_code != 200:
                return None
            content_type = resp.headers.get("Content-Type")
            return resp.content, None, content_type
        except Exception as err:
            logger.error(f"Lark 文件下载失败：{err}")
            return None

    def download_message_resource_bytes(
        self, message_id: str, file_key: str, resource_type: str
    ) -> Optional[Tuple[bytes, Optional[str], Optional[str]]]:
        """下载消息中的资源文件（图片/音频/文件），返回 (bytes, file_name, content_type)。"""
        if not message_id or not file_key:
            return None
        url = f"{API_BASE}/im/v1/messages/{message_id}/resources/{file_key}"
        params = {"type": resource_type}
        try:
            resp = requests.get(url, headers=self._headers(), params=params, timeout=60)
            if resp.status_code != 200:
                return None
            content_type = resp.headers.get("Content-Type")
            return resp.content, None, content_type
        except Exception as err:
            logger.error(f"Lark 消息资源下载失败：{err}")
            return None

    # ------------------------------------------------------------------ #
    #  卡片构建（对标 Feishu._build_card，schema 2.0）
    # ------------------------------------------------------------------ #
    @staticmethod
    def _escape_card_text(text: Optional[str]) -> str:
        """转义 Lark 卡片 markdown 中易误触的字符。"""
        if not text:
            return ""
        escaped = str(text)
        for source, target in {"\\": "&#92;", "<": "&#60;", ">": "&#62;"}.items():
            escaped = escaped.replace(source, target)
        return escaped

    @staticmethod
    def _build_message_text(
        title: Optional[str], text: Optional[str], link: Optional[str] = None
    ) -> str:
        """拼接 Lark Markdown 文本内容。"""
        parts = []
        if title:
            parts.append(f"**{LarkClient._escape_card_text(title).strip()}**")
        if text:
            parts.append(LarkClient._escape_card_text(text).strip())
        if link:
            parts.append(f"[查看详情]({link.strip()})")
        return "\n\n".join(part for part in parts if part)

    @classmethod
    def _build_markdown_section(
        cls, text: Optional[str], text_size: str = "normal", margin: Optional[str] = None
    ) -> Optional[dict]:
        content = cls._escape_card_text(text).strip()
        if not content:
            return None
        section = {"tag": "markdown", "text_size": text_size, "content": content}
        if margin:
            section["margin"] = margin
        return section

    @staticmethod
    def _card_actions(
        buttons: Optional[List[List[dict]]], margin: Optional[str] = None
    ) -> List[dict]:
        """将统一按钮结构转换为 Lark schema 2.0 卡片按钮配置。"""
        if not buttons:
            return []
        card_rows = []
        for row in buttons[:8]:
            columns = []
            for button in row[:3]:
                text = (button or {}).get("text")
                if not text:
                    continue
                url = (button or {}).get("url")
                callback_data = (button or {}).get("callback_data")
                behaviors = []
                if callback_data:
                    behaviors.append({
                        "type": "callback",
                        "value": {"callback_data": str(callback_data)},
                    })
                if url:
                    behaviors.append({
                        "type": "open_url",
                        "default_url": str(url),
                        "pc_url": str(url),
                        "android_url": str(url),
                        "ios_url": str(url),
                    })
                if not behaviors:
                    behaviors.append({
                        "type": "callback",
                        "value": {"callback_data": str(text)},
                    })
                element = {
                    "tag": "button",
                    "text": {"tag": "plain_text", "content": text[:20]},
                    "type": "default",
                    "behaviors": behaviors,
                }
                columns.append({
                    "tag": "column", "width": "weighted", "weight": 1, "elements": [element],
                })
            if columns:
                row_dict = {"tag": "column_set", "flex_mode": "none", "columns": columns}
                if margin:
                    row_dict["margin"] = margin
                card_rows.append(row_dict)
        return card_rows

    # 通知类型 -> header 模板色映射
    _MTYPE_COLOR_MAP = {
        "资源下载": "blue",
        "整理入库": "turquoise",
        "订阅": "green",
        "站点": "yellow",
        "媒体服务器": "purple",
        "手动处理": "red",
        "插件": "blue",
        "智能体": "indigo",
        "其它": "blue",
    }

    @classmethod
    def _header_template_for_mtype(cls, mtype: Optional[str]) -> str:
        """根据消息类型返回 header 模板色。"""
        if not mtype:
            return "blue"
        return cls._MTYPE_COLOR_MAP.get(str(mtype), "blue")

    @classmethod
    def build_card_v2(
        cls,
        title: Optional[str],
        text: Optional[str],
        link: Optional[str],
        buttons: Optional[List[List[dict]]],
        image_key: Optional[str] = None,
        header_template: Optional[str] = None,
        image_items: Optional[List[Dict[str, Any]]] = None,
    ) -> Dict[str, Any]:
        """构建 Lark schema 2.0 交互卡片（对标 Feishu._build_card，增加 header 样式）。"""
        elements: List[dict] = []
        has_header = bool(title)
        # 图文列表项：每项 [海报 | 标题] 一行（用于媒体/种子列表场景，避免仅发文字）
        has_image = bool(image_key)
        if image_items:
            for item in image_items:
                item_title = item.get("title") or ""
                item_key = item.get("image_key")
                if item_key:
                    elements.append({
                        "tag": "column_set",
                        "flex_mode": "none",
                        "columns": [
                            {
                                "tag": "column",
                                "width": "weighted",
                                "weight": 1,
                                "elements": [{
                                    "tag": "img",
                                    "img_key": item_key,
                                    "alt": {"tag": "plain_text", "content": item_title or "图片"},
                                    "mode": "fit_horizontal",
                                }],
                            },
                            {
                                "tag": "column",
                                "width": "weighted",
                                "weight": 2,
                                "elements": [cls._build_markdown_section(item_title, text_size="normal")],
                            },
                        ],
                    })
                    has_image = True
                else:
                    section = cls._build_markdown_section(item_title, text_size="normal")
                    if section:
                        elements.append(section)
                elements.append({"tag": "hr"})
            if elements and elements[-1].get("tag") == "hr":
                elements.pop()
        elif image_key:
            elements.append({
                "tag": "img",
                "img_key": image_key,
                "alt": {"tag": "plain_text", "content": title or "图片"},
                "mode": "fit_horizontal",
            })
        # 有 header 标题栏时，body 里不再重复显示标题；无 header 时 body 内显示 heading 标题
        text_margin = "12px 12px 0px 12px" if has_image else None
        body_margin = "4px 12px 12px 12px" if has_image else None
        action_margin = "0px 12px 12px 12px" if has_image else None

        title_section = None
        if not has_header and title:
            title_section = cls._build_markdown_section(title, text_size="heading", margin=text_margin)

        body_raw = cls._build_message_text(title=None, text=text, link=link)
        body_section = cls._build_markdown_section(body_raw, text_size="normal", margin=body_margin) if body_raw else None

        if title_section:
            elements.append(title_section)
        # 正文样式处理
        if body_section:
            # 有图片时：文字用灰色背景信息栏包裹 + 前面加分隔线
            if has_image:
                elements.append({"tag": "hr"})
                elements.append({
                    "tag": "column_set",
                    "flex_mode": "none",
                    "background_style": "grey",
                    "columns": [
                        {
                            "tag": "column",
                            "width": "weighted",
                            "weight": 1,
                            "elements": [body_section],
                        },
                    ],
                })
            else:
                if title_section:
                    elements.append({"tag": "hr"})
                elements.append(body_section)
        # 按钮处理
        actions = cls._card_actions(buttons, margin=action_margin)
        if body_section and actions:
            elements.append({"tag": "hr"})
        elements.extend(actions)

        card: Dict[str, Any] = {
            "schema": "2.0",
            "config": {
                "wide_screen_mode": True,
                "enable_forward": True,
                "update_multi": True,
                "summary": {"content": title or "MoviePilot"},
            },
            "body": {
                "direction": "vertical",
                "padding": "0px 0px 0px 0px" if has_image else "12px 12px 12px 12px",
                "elements": elements,
            },
        }
        # 添加 header（模板色标题栏）
        if has_header:
            card["header"] = {
                "template": header_template or "blue",
                "title": {
                    "tag": "plain_text",
                    "content": title,
                },
            }
        return card

    # ------------------------------------------------------------------ #
    #  兼容旧接口：保留 build_card 给测试端点使用
    # ------------------------------------------------------------------ #
    @staticmethod
    def build_card(
        title: str,
        content: str,
        buttons: Optional[List[Dict[str, Any]]] = None,
        color: str = "blue",
        img_key: Optional[str] = None,
        link: Optional[str] = None,
    ) -> Dict[str, Any]:
        """
        构建测试用卡片（旧版兼容接口，转为 schema 2.0 格式）。
        buttons 格式: [{"text": "...", "action_id": "...", "value": "...", "type": "primary"}]
        """
        # 将旧版按钮格式转换为新版 List[List[dict]] 格式
        v2_buttons = None
        if buttons:
            v2_buttons = []
            for btn in buttons:
                callback_data = btn.get("value") or btn.get("action_id") or ""
                v2_buttons.append([{
                    "text": btn.get("text", ""),
                    "callback_data": callback_data,
                }])
        return LarkClient.build_card_v2(
            title=title, text=content, link=link,
            buttons=v2_buttons, image_key=img_key,
            header_template=color,
        )

    @staticmethod
    def build_button(
        text: str, action_id: str, value: str = "", button_type: str = "default"
    ) -> Dict[str, Any]:
        """构建单个卡片按钮（旧版兼容接口）。"""
        return {
            "tag": "button",
            "text": {"tag": "plain_text", "content": text},
            "action_id": action_id,
            "value": {"value": value},
            "type": button_type,
        }

    def build_interactive_card(
        self, title: str, body_elements: List[Dict[str, Any]],
        actions: Optional[List[Dict[str, Any]]] = None, color: str = "blue",
    ) -> Dict[str, Any]:
        """构建自定义交互式卡片（旧版兼容接口）。"""
        elements = list(body_elements)
        if actions:
            elements.append({"tag": "action", "actions": actions})
        return {
            "schema": "2.0",
            "config": {"wide_screen_mode": True, "update_multi": True, "summary": {"content": title}},
            "body": {"direction": "vertical", "padding": "12px 12px 12px 12px", "elements": elements},
        }

    # ------------------------------------------------------------------ #
    #  流式卡片（对标 Feishu._build_streaming_card_payload / _create_streaming_card 等）
    # ------------------------------------------------------------------ #
    @classmethod
    def _build_streaming_card_payload(
        cls, title: Optional[str], text: Optional[str]
    ) -> Dict[str, Any]:
        """构建支持 CardKit 流式更新的 Lark 卡片 JSON 2.0。"""
        elements: List[dict] = []
        title_content = cls._escape_card_text(title).strip() if title else ""
        body_content = cls._escape_card_text(cls._strip_streaming_markdown_images(text)).strip()
        if title_content:
            elements.append({
                "tag": "markdown",
                "element_id": cls.STREAM_CARD_TITLE_ELEMENT_ID,
                "content": f"**{title_content}**",
            })
        elements.append({
            "tag": "markdown",
            "element_id": cls.STREAM_CARD_BODY_ELEMENT_ID,
            "content": body_content or " ",
        })
        return {
            "schema": "2.0",
            "config": {
                "wide_screen_mode": True,
                "enable_forward": True,
                "update_multi": True,
                "streaming_mode": True,
                "summary": {"content": title or "MoviePilot助手"},
                "streaming_config": {
                    "print_frequency_ms": {"default": 70},
                    "print_step": {"default": 1},
                    "print_strategy": "fast",
                },
            },
            "body": {
                "direction": "vertical",
                "padding": "12px 12px 12px 12px",
                "elements": elements,
            },
        }

    def _create_streaming_card(self, title: Optional[str], text: Optional[str]) -> Optional[str]:
        """通过 CardKit API 创建流式卡片，返回 card_id。"""
        url = f"{API_BASE}/cardkit/v1/cards"
        card_data = self._build_streaming_card_payload(title=title, text=text)
        payload = {
            "type": "card_json",
            "data": json.dumps(card_data, ensure_ascii=False),
        }
        try:
            resp = requests.post(url, headers=self._headers(), json=payload, timeout=15)
            data = resp.json()
            if data.get("code") == 0:
                return data.get("data", {}).get("card_id")
            logger.warning("Lark 流式卡片创建失败：code=%s, msg=%s", data.get("code"), data.get("msg"))
        except Exception as err:
            logger.error(f"Lark 流式卡片创建失败：{err}")
        return None

    def _send_streaming_card_message(
        self,
        title: Optional[str],
        text: Optional[str],
        userid: Optional[str] = None,
        chat_id: Optional[str] = None,
        receive_id_type: Optional[str] = None,
        original_message_id: Optional[str] = None,
        default_open_id: Optional[str] = None,
        default_chat_id: Optional[str] = None,
    ) -> Optional[dict]:
        """创建流式卡片并发送消息，返回带 metadata 的结果。

        若 CardKit 流式不可用（最常见是 Lark 应用缺少 cardkit:card:write 权限作用域，
        或 CardKit API 调用失败），则降级为普通 interactive 卡片发送——
        普通卡片走标准 IM 发消息 API（/im/v1/messages），无需 cardkit 权限，
        保证消息仍能送达（只是不具备逐字流式动画）。
        """
        card_id = self._create_streaming_card(title=title, text=text)
        if card_id:
            card_content = {"type": "card", "data": {"card_id": card_id}}
            if original_message_id:
                result = self._reply_message(
                    message_id=original_message_id, msg_type="interactive", content=card_content,
                )
            else:
                receive_id, resolved_type = self.resolve_target(
                    userid=userid, chat_id=chat_id, receive_id_type=receive_id_type,
                    default_open_id=default_open_id, default_chat_id=default_chat_id,
                )
                result = self._send_message(receive_id, resolved_type, "interactive", card_content)
            if not result:
                return None
            result["metadata"] = {
                "feishu_streaming": {
                    "card_id": card_id,
                    "element_id": self.STREAM_CARD_BODY_ELEMENT_ID,
                    "sequence": 0,
                }
            }
            return result

        # —— 降级：CardKit 流式不可用，改用普通 interactive 卡片（无需 cardkit 权限）——
        logger.warning(
            "Lark CardKit 流式卡片不可用，降级为普通卡片发送"
            "（若需逐字流式效果，请在 Lark 开放平台为应用添加 cardkit:card:write 权限作用域）"
        )
        fallback_payload = self.build_card_v2(
            title=title, text=text, link=None, buttons=None, image_key=None,
            header_template=self._header_template_for_mtype("Agent"),
        )
        if original_message_id:
            return self._reply_message(
                message_id=original_message_id, msg_type="interactive", content=fallback_payload,
            )
        receive_id, resolved_type = self.resolve_target(
            userid=userid, chat_id=chat_id, receive_id_type=receive_id_type,
            default_open_id=default_open_id, default_chat_id=default_chat_id,
        )
        return self._send_message(receive_id, resolved_type, "interactive", fallback_payload)

    @staticmethod
    def _safe_parse_cardkit(resp) -> dict:
        """防御性解析 CardKit 变更类端点（content/settings）的返回。

        Lark 国际版（open.larksuite.com）的 card_element.content / card.settings
        这类「变更」端点在成功时可能返回 ``null`` 后尾随一个 NUL/控制字符
        （如 ``null\\x00``），导致 ``resp.json()`` 抛
        ``JSONDecodeError: Extra data`` 而被误判为失败。

        处理策略：剥离 NUL/控制字符与首尾空白后 ``json.loads``；
        解析失败 / 空响应 / 非 dict（如 ``null``）一律返回空 dict，
        由调用方结合 HTTP 状态码兜底判定（变更内容已随请求体发往 Lark 生效）。
        若解析出带 ``code`` 字段的 dict，则原样返回交由调用方以 ``code`` 为准。
        """
        text = getattr(resp, "text", "") or ""
        raw = text
        cleaned = "".join(ch for ch in text if ch >= " " or ch in "\t\n\r")
        if not cleaned.strip():
            return {}
        try:
            parsed = json.loads(cleaned)
        except Exception:
            logger.warning(
                "Lark CardKit 响应解析失败(raw=%r, status=%s)，转由状态码判定",
                raw[:200], resp.status_code,
            )
            return {}
        if isinstance(parsed, dict):
            return parsed
        # 解析为 null/列表/字符串等非 dict：无 code 信息，交由状态码兜底
        return {}

    @staticmethod
    def _cardkit_ok(resp, data: dict) -> bool:
        """判定 CardKit 变更请求是否成功。

        - 响应体含明确 ``code`` 字段 → 以 ``code == 0`` 为准
          （Lark 即便业务出错也常返回 HTTP 200，故不可只信状态码）；
        - 响应体无 ``code`` 信息（null/空/无法解析）→ 以 HTTP 2xx 兜底。
        """
        if isinstance(data, dict) and "code" in data:
            return data.get("code") == 0
        return 200 <= resp.status_code < 300

    def _update_streaming_card_content(
        self, card_id: str, element_id: str, content: str, sequence: int
    ) -> bool:
        """更新流式卡片内容（CardKit API）。"""
        url = f"{API_BASE}/cardkit/v1/cards/{card_id}/elements/{element_id}/content"
        payload = {
            "uuid": str(uuid.uuid4()),
            "content": content or " ",
            "sequence": sequence,
        }
        try:
            # 注意：CardKit 内容更新端点仅接受 PUT（官方 lark_oapi SDK 固定为 HttpMethod.PUT），
            # 用 POST 会被 Lark 网关判为未匹配路由而返回纯文本 404 page not found。
            resp = requests.put(url, headers=self._headers(), json=payload, timeout=15)
            data = self._safe_parse_cardkit(resp)
            if self._cardkit_ok(resp, data):
                logger.debug("Lark 流式卡片更新成功：card_id=%s, seq=%s", card_id, sequence)
                return True
            logger.warning("Lark 流式卡片内容更新失败：card_id=%s, seq=%s, code=%s, msg=%s",
                           card_id, sequence, data.get("code"), data.get("msg"))
        except Exception as err:
            logger.error(f"Lark 流式卡片内容更新失败：{err}")
        return False

    def close_streaming_card(self, card_id: str, sequence: int) -> bool:
        """关闭流式卡片（CardKit settings API）。"""
        if not card_id:
            return False
        url = f"{API_BASE}/cardkit/v1/cards/{card_id}/settings"
        payload = {
            "settings": json.dumps({"config": {"streaming_mode": False}}, ensure_ascii=False),
            "uuid": str(uuid.uuid4()),
            "sequence": sequence,
        }
        try:
            # 注意：CardKit settings（关闭流式卡片）端点仅接受 PATCH（官方 lark_oapi SDK 固定为
            # HttpMethod.PATCH），用 POST 会被 Lark 网关判为未匹配路由而返回纯文本 404 page not found。
            resp = requests.patch(url, headers=self._headers(), json=payload, timeout=15)
            data = self._safe_parse_cardkit(resp)
            if self._cardkit_ok(resp, data):
                return True
            logger.warning("Lark 关闭流式卡片失败：card_id=%s, seq=%s, code=%s, msg=%s",
                           card_id, sequence, data.get("code"), data.get("msg"))
        except Exception as err:
            logger.error(f"Lark 关闭流式卡片失败：{err}")
        return False

    # ------------------------------------------------------------------ #
    #  流式卡片辅助方法（对标 Feishu._strip_streaming_markdown_images 等）
    # ------------------------------------------------------------------ #
    @classmethod
    def _strip_streaming_markdown_images(cls, text: Optional[str]) -> str:
        if not text:
            return ""
        normalized_text = cls._strip_trailing_incomplete_markdown_image(str(text))
        parts = []
        last_end = 0
        for match in cls.MARKDOWN_IMAGE_PATTERN.finditer(normalized_text):
            parts.append(normalized_text[last_end:match.start()])
            alt_text = (match.group("alt") or "").strip()
            if alt_text:
                parts.append(alt_text)
            last_end = match.end()
        parts.append(normalized_text[last_end:])
        return "".join(parts)

    @classmethod
    def _strip_trailing_incomplete_markdown_image(cls, text: str) -> str:
        if not text:
            return ""
        start = text.rfind("![")
        if start < 0:
            return text
        fragment = text[start:]
        if "\n" in fragment or "\r" in fragment or cls.MARKDOWN_IMAGE_PATTERN.fullmatch(fragment):
            return text
        if ")" not in fragment:
            return text[:start].rstrip()
        return text

    @classmethod
    def _extract_markdown_image_urls(cls, text: Optional[str]) -> List[str]:
        if not text:
            return []
        urls = []
        for match in cls.MARKDOWN_IMAGE_PATTERN.finditer(str(text)):
            image_url = (match.group("target") or "").strip()
            if image_url and cls._is_external_image_url(image_url):
                urls.append(image_url)
        return urls

    @staticmethod
    def _is_external_image_url(image_url: str) -> bool:
        normalized_url = (image_url or "").strip().lower()
        return normalized_url.startswith(("http://", "https://", "feishu://image/"))

    @staticmethod
    def _dedupe_image_urls(image_urls: List[str]) -> List[str]:
        deduped = []
        seen = set()
        for image_url in image_urls:
            normalized_url = (image_url or "").strip()
            if not normalized_url or normalized_url in seen:
                continue
            seen.add(normalized_url)
            deduped.append(normalized_url)
        return deduped

    def _send_agent_streaming_images(
        self,
        image_urls: List[str],
        userid: Optional[str] = None,
        chat_id: Optional[str] = None,
        receive_id_type: Optional[str] = None,
        sent_image_urls: Optional[List[str]] = None,
        default_open_id: Optional[str] = None,
        default_chat_id: Optional[str] = None,
    ) -> List[str]:
        """将 Agent 流式回复中的图片作为独立图片卡片发送。"""
        sent_images = list(sent_image_urls or [])
        pending_image_urls = [
            url for url in self._dedupe_image_urls(image_urls) if url not in sent_images
        ]
        for image_url in pending_image_urls:
            image_key = self._upload_remote_image(image_url)
            if not image_key:
                continue
            payload = self.build_card_v2(
                title=None, text=None, link=None, buttons=None, image_key=image_key,
            )
            try:
                receive_id, resolved_type = self.resolve_target(
                    userid=userid, chat_id=chat_id, receive_id_type=receive_id_type,
                    default_open_id=default_open_id, default_chat_id=default_chat_id,
                )
                self._send_message(receive_id, resolved_type, "interactive", payload)
                sent_images.append(image_url)
            except Exception as err:
                logger.error(f"Lark Agent 图片消息发送失败：{err}")
        return sent_images

    # ------------------------------------------------------------------ #
    #  远程图片上传（对标 Feishu._upload_remote_image）
    # ------------------------------------------------------------------ #
    def _upload_remote_image(self, image_url: Optional[str]) -> Optional[str]:
        """下载远程图片并上传到 Lark，返回 image_key。"""
        image_url = (image_url or "").strip()
        if not image_url:
            return None
        if image_url.startswith("feishu://image/"):
            resource_path = image_url.replace("feishu://image/", "", 1)
            return resource_path.rsplit("/", 1)[-1].strip() or None

        try:
            resp = requests.get(image_url, timeout=30)
            if not resp.content:
                logger.warning(f"Lark 图片下载失败：{image_url}")
                return None
            content_type = resp.headers.get("Content-Type", "")
            suffix = ".jpg"
            if "png" in content_type:
                suffix = ".png"
            elif "gif" in content_type:
                suffix = ".gif"
            elif "webp" in content_type:
                suffix = ".webp"
            elif "jpeg" in content_type or "jpg" in content_type:
                suffix = ".jpg"
            else:
                path_suffix = Path(urlparse(image_url).path).suffix.lower()
                if path_suffix in self.IMAGE_SUFFIXES:
                    suffix = path_suffix
            with tempfile.NamedTemporaryFile(suffix=suffix, delete=False) as fp:
                fp.write(resp.content)
                temp_path = fp.name
            try:
                return self.upload_image(temp_path)
            finally:
                try:
                    Path(temp_path).unlink(missing_ok=True)
                except Exception:
                    pass
        except Exception as err:
            logger.error(f"Lark 远程图片上传失败：{err}")
            return None

    # ------------------------------------------------------------------ #
    #  卡片回调数据提取（对标 Feishu._extract_card_callback_data）
    # ------------------------------------------------------------------ #
    @staticmethod
    def extract_card_callback_data(value: Any, name: Optional[str] = None) -> Optional[str]:
        """从新版/旧版卡片回调中提取统一的 callback_data。"""
        callback_data = None
        if isinstance(value, dict):
            callback_data = value.get("callback_data") or value.get("data") or value.get("value")
        elif isinstance(value, str):
            callback_data = value
        if not callback_data:
            callback_data = name
        return str(callback_data).strip() if callback_data else None

    # ------------------------------------------------------------------ #
    #  媒体文件上传 / 下载（基础 API）
    # ------------------------------------------------------------------ #
    def upload_image(self, image_path: str, image_type: str = "message") -> str:
        """上传图片，返回 image_key。"""
        import mimetypes
        url = f"{API_BASE}/im/v1/images"
        mime_type, _ = mimetypes.guess_type(image_path)
        if not mime_type or not mime_type.startswith("image/"):
            mime_type = "image/png"
        with open(image_path, "rb") as f:
            files = {"image": (Path(image_path).name, f, mime_type)}
            data = {"image_type": image_type}
            headers = {"Authorization": f"Bearer {self._get_tenant_access_token()}"}
            resp = requests.post(url, data=data, headers=headers, files=files, timeout=30)
        data = resp.json()
        if data.get("code") != 0:
            raise RuntimeError(f"上传图片失败：{data.get('msg')}")
        return data["data"]["image_key"]

    def upload_file(self, file_path: str, file_name: str = "", file_type: str = "stream") -> str:
        """上传文件，返回 file_key。"""
        url = f"{API_BASE}/im/v1/files"
        params = {"file_type": file_type}
        file_name = file_name or Path(file_path).name
        with open(file_path, "rb") as f:
            files = {"file": (file_name, f, "application/octet-stream")}
            headers = {"Authorization": f"Bearer {self._get_tenant_access_token()}"}
            resp = requests.post(url, params=params, headers=headers, files=files, timeout=60)
        data = resp.json()
        if data.get("code") != 0:
            raise RuntimeError(f"上传文件失败：{data.get('msg')}")
        return data["data"]["file_key"]

    def download_image(self, image_key: str) -> bytes:
        """下载 Lark 图片，返回图片二进制数据（旧版兼容接口）。"""
        url = f"{API_BASE}/im/v1/images/{image_key}"
        resp = requests.get(url, headers=self._headers(), timeout=15)
        resp.raise_for_status()
        if resp.headers.get("Content-Type", "").startswith("application/json"):
            data = resp.json()
            if data.get("code") != 0:
                raise RuntimeError(f"下载图片失败：{data.get('msg')}")
        return resp.content

    @staticmethod
    def _guess_image_ext(img_bytes: bytes) -> str:
        if img_bytes.startswith(b'\xff\xd8'):
            return "jpeg"
        if img_bytes.startswith(b'\x89PNG\r\n\x1a\n'):
            return "png"
        if img_bytes.startswith(b'GIF87a') or img_bytes.startswith(b'GIF89a'):
            return "gif"
        if img_bytes.startswith(b'BM'):
            return "bmp"
        if img_bytes[:4] == b'RIFF' and img_bytes[8:12] == b'WEBP':
            return "webp"
        return "png"

    def download_image_to_data_url(self, image_key: str) -> str:
        """下载 Lark 图片并转为 data URL（用于 AI 智能体识别图片）。"""
        img_bytes = self.download_image(image_key)
        ext = self._guess_image_ext(img_bytes)
        b64 = base64.b64encode(img_bytes).decode("utf-8")
        return f"data:image/{ext};base64,{b64}"

    def download_file_bytes_raw(self, file_key: str) -> bytes:
        """下载 Lark 文件，返回文件二进制数据（旧版兼容接口）。"""
        url = f"{API_BASE}/im/v1/files/{file_key}?file_type=stream"
        resp = requests.get(url, headers=self._headers(), timeout=60)
        resp.raise_for_status()
        return resp.content

    # ------------------------------------------------------------------ #
    #  用户信息查询
    # ------------------------------------------------------------------ #
    def get_user_info(self, open_id: str) -> Dict[str, Any]:
        """查询用户基本信息。"""
        url = f"{API_BASE}/contact/v3/users/{open_id}"
        params = {"user_id_type": "open_id"}
        resp = requests.get(url, headers=self._headers(), params=params, timeout=10)
        data = resp.json()
        if data.get("code") != 0:
            logger.warning("查询用户信息失败：%s", data.get("msg"))
            return {}
        return data.get("data", {}).get("user", {})

    def batch_get_id(
        self,
        emails: Optional[List[str]] = None,
        mobiles: Optional[List[str]] = None,
        employee_ids: Optional[List[str]] = None,
    ) -> Dict[str, str]:
        """批量通过邮箱/手机号/工号查询用户的 open_id。"""
        url = f"{API_BASE}/contact/v3/users/batch_get_id"
        params: Dict[str, Any] = {"user_id_type": "open_id"}
        payload: Dict[str, Any] = {}
        if emails:
            payload["emails"] = emails
        if mobiles:
            payload["mobiles"] = mobiles
        if employee_ids:
            payload["employee_ids"] = employee_ids
        if not payload:
            return {}
        resp = requests.post(url, params=params, headers=self._headers(), json=payload, timeout=15)
        data = resp.json()
        if data.get("code") != 0:
            raise RuntimeError(f"batch_get_id 失败：{data.get('msg')}（code={data.get('code')}）")
        result: Dict[str, str] = {}
        items = (data.get("data") or {}).get("user_list") or []
        for it in items:
            oid = it.get("user_id", "")
            if not oid:
                continue
            for key in ("email", "mobile", "employee_id", "employee_no"):
                v = it.get(key, "")
                if v:
                    result[v] = oid
        return result

    # ------------------------------------------------------------------ #
    #  旧版发送方法（保持向后兼容）
    # ------------------------------------------------------------------ #
    def send_card(
        self, receive_id: str, card: Dict[str, Any], receive_id_type: str = "open_id"
    ) -> str:
        """发送交互式卡片消息，返回 message_id（旧版兼容接口）。"""
        result = self._send_message(receive_id, receive_id_type, "interactive", card)
        if not result or not result.get("success"):
            raise RuntimeError("发送卡片消息失败")
        return result["message_id"]

    def send_image_msg(
        self, receive_id: str, image_key: str, receive_id_type: str = "open_id"
    ) -> str:
        """发送图片消息（旧版兼容接口）。"""
        result = self._send_message(receive_id, receive_id_type, "image", {"image_key": image_key})
        if not result or not result.get("success"):
            raise RuntimeError("发送图片消息失败")
        return result["message_id"]

    def send_file_msg(
        self, receive_id: str, file_key: str, receive_id_type: str = "open_id"
    ) -> str:
        """发送文件消息（旧版兼容接口）。"""
        result = self._send_message(receive_id, receive_id_type, "file", {"file_key": file_key})
        if not result or not result.get("success"):
            raise RuntimeError("发送文件消息失败")
        return result["message_id"]

    def reply_message(
        self,
        message_id: str,
        content: str,
        msg_type: str = "text",
    ) -> str:
        """
        回复消息（旧版兼容接口，修复了 JSON 双重序列化 bug）。
        :param content: 文本字符串 或 已序列化的 JSON 字符串（dict 也会自动处理）
        :return: 新消息的 message_id
        """
        if not message_id:
            raise RuntimeError("回复消息失败：message_id 为空")
        # 统一构建 content dict
        if msg_type == "text":
            content_dict = {"text": content} if isinstance(content, str) else content
        else:
            # interactive/file/audio 等：content 可能是 dict 或 JSON 字符串
            if isinstance(content, dict):
                content_dict = content
            elif isinstance(content, str):
                try:
                    content_dict = json.loads(content)
                except (json.JSONDecodeError, TypeError):
                    content_dict = {"text": content}
            else:
                content_dict = {"text": str(content)}

        result = self._reply_message(message_id, msg_type, content_dict)
        if not result or not result.get("success"):
            raise RuntimeError("回复消息失败")
        return result["message_id"]

    # 旧版 send_image / send_file 方法名兼容（通过 send_image_msg / send_file_msg 调用）
    # 如果外部代码需要直接通过 receive_id+image_key 发送，使用 send_image_msg / send_file_msg
