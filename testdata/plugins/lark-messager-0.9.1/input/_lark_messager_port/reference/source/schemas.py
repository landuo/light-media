"""
LarkMessager 数据模型
"""
from pydantic import BaseModel, Field
from typing import Optional, List, Dict, Any


class LarkMessage(BaseModel):
    """Lark消息基础模型"""
    msg_type: str = Field(..., description="消息类型：text / interactive / image / file")
    content: Dict[str, Any] = Field(default_factory=dict, description="消息内容")
    receive_id: str = Field("", description="接收者 ID（chat_id / open_id / user_id）")


class LarkCardElement(BaseModel):
    """Lark卡片元素"""
    tag: str = Field(..., description="元素标签：div / img / action / hr / markdown 等")
    text: Optional[Dict[str, str]] = Field(None, description="文本内容，如 {'tag': 'plain_text', 'content': '...'}")
    content: Optional[str] = Field(None, description="markdown 内容")
    elements: Optional[List[Dict[str, Any]]] = Field(None, description="嵌套元素")
    actions: Optional[List[Dict[str, Any]]] = Field(None, description="动作列表")
    img_key: Optional[str] = Field(None, description="图片 image_key")
    alt: Optional[Dict[str, str]] = Field(None, description="图片替代文本")


class LarkCardHeader(BaseModel):
    """Lark卡片头部"""
    title: Dict[str, str] = Field(..., description="标题，如 {'tag': 'plain_text', 'content': '...'}")
    subtitle: Optional[Dict[str, str]] = Field(None, description="副标题")
    template: Optional[str] = Field(None, description="卡片颜色模板：blue / green / orange / red / purple / indigo")


class LarkInteractiveCard(BaseModel):
    """Lark交互式卡片"""
    config: Dict[str, bool] = Field(default_factory=lambda: {"wide_screen_mode": True}, description="卡片配置")
    header: Optional[LarkCardHeader] = Field(None, description="卡片头部")
    elements: List[Dict[str, Any]] = Field(default_factory=list, description="卡片元素列表")
    card_link: Optional[Dict[str, str]] = Field(None, description="卡片链接")


class LarkWebhookEvent(BaseModel):
    """Lark Webhook 回调事件"""
    schema_: Optional[str] = Field(None, alias="schema")
    header: Optional[Dict[str, Any]] = Field(None, description="事件头（v2.0 schema）")
    event: Optional[Dict[str, Any]] = Field(None, description="事件体（im.message.receive_v1 等消息事件）")
    challenge: Optional[str] = Field(None, description="URL 验证挑战码")
    # 卡片回调（card.action.trigger）的字段在顶层，不在 event 里
    action: Optional[Dict[str, Any]] = Field(None, description="卡片按钮动作（card.action.trigger 时在顶层）")
    operator: Optional[Dict[str, Any]] = Field(None, description="卡片操作人（card.action.trigger 时在顶层）")
    message: Optional[Dict[str, Any]] = Field(None, description="卡片所属消息（card.action.trigger 时在顶层）")

    @property
    def event_type(self) -> str:
        if self.header:
            return self.header.get("event_type", "")
        return ""

    @property
    def event_id(self) -> str:
        if self.header:
            return self.header.get("event_id", "")
        return ""

    @property
    def app_id(self) -> str:
        if self.header:
            return self.header.get("app_id", "")
        return ""


class LarkUserMessage(BaseModel):
    """Lark用户消息"""
    message_id: str = Field("", description="消息 ID")
    chat_id: str = Field("", description="会话 ID")
    chat_type: str = Field("", description="会话类型：p2p / group")
    sender_id: str = Field("", description="发送者 open_id")
    sender_type: str = Field("", description="发送者类型：user / app")
    create_time: int = Field(0, description="消息发送时间戳（秒）")
    msg_type: str = Field("", description="消息类型")
    text: str = Field("", description="消息文本内容")
    content: Dict[str, Any] = Field(default_factory=dict, description="消息原始内容")


class LarkButtonAction(BaseModel):
    """Lark卡片按钮回调"""
    action_id: str = Field("", description="按钮 action_id")
    action_value: str = Field("", description="按钮携带的 value")
    tag: str = Field("", description="按钮 tag")
    option: Optional[str] = Field(None, description="选中项")
    action_time: int = Field(0, description="动作触发时间戳")
    message_id: str = Field("", description="所属消息 ID")
    chat_id: str = Field("", description="所属会话 ID")
    operator_open_id: str = Field("", description="操作者的 open_id")
