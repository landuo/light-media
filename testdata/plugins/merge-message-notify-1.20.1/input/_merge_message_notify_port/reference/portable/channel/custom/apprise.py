from typing import Tuple, List, Dict, Any
import apprise
from app.plugins.mergemessagenotify.channel.custom import CustomChannel
from app.schemas.types import NotificationType
from app.log import logger

class AppriseChannel(CustomChannel):
    comp_key: str = f'{CustomChannel.comp_key}.apprise'
    comp_name: str = 'Apprise'
    comp_order: int = CustomChannel.comp_order * 100 + 52
    config_default: Dict[str, Any] = {}

    def get_form(self) -> Tuple[List[dict], Dict[str, Any]]:
        config_suggest = {}
        config_suggest.update(self.config_default)
        row1 = {'component': 'VRow', 'content': [{'component': 'VCol', 'props': {'cols': 12, 'xxl': 12, 'xl': 12, 'lg': 12, 'md': 12, 'sm': 12, 'xs': 12}, 'content': [{'component': 'VTextarea', 'props': {'model': 'service_urls', 'label': '服务地址', 'placeholder': '1、填写服务地址，每行一个；\n2、语法查看官方文档，例如：\n   apprise://hostname/Token\n   bark://hostname/device_key', 'hint': '必填。填写服务地址，每行一个；语法查看官方文档。'}}]}]}
        row2 = self.build_notify_type_select_row_element()
        row3 = self.build_test_once_switch_row_element()
        elements = [row1, row2, row3]
        self.save_default_config()
        return (elements, config_suggest)

    def __check_config(self) -> bool:
        service_url_lines = self.__extract_service_url_lines()
        if not service_url_lines:
            logger.warn(f'配置检查不通过: channel = {self.comp_name}, 服务地址无效')
            return False
        return True

    def __extract_service_url_lines(self) -> List[str]:
        service_urls: str = self.get_config_item(config_key='service_urls') or ''
        return [line.strip() for line in service_urls.splitlines() if line and line.strip()]

    def __build_apprise(self) -> apprise.Apprise:
        app = apprise.Apprise()
        service_url_lines = self.__extract_service_url_lines()
        for service_url_line in service_url_lines:
            if not service_url_line:
                continue
            app.add(service_url_line)
        return app

    def send_message(self, title: str, text: str, type: NotificationType=None, ext_info: dict={}) -> bool:
        type_str = type.value if type else None
        enable_notify_types: List[str] = self.get_config_item('enable_notify_types')
        if type and enable_notify_types and (type.name not in enable_notify_types):
            logger.warn(f'发送消息中止: channel = {self.comp_name}, type = {type_str}, 消息类型不受支持')
            return False
        if not self.__check_config():
            return False
        image = ext_info.get('image')
        app = self.__build_apprise()
        res = app.notify(title=title, body=text or title, attach=image)
        if res:
            logger.info(f'发送消息成功: channel = {self.comp_name}, type = {type_str}')
            return True
        else:
            logger.warn(f'发送消息失败: channel = {self.comp_name}, type = {type_str}')
            return False
