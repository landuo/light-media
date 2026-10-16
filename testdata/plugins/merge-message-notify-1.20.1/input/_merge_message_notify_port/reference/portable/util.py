from mako.template import Template
from app.log import logger

class TemplateUtil:

    @staticmethod
    def render_text(text: str, variables: dict) -> str:
        if not text or not variables:
            return text
        try:
            template = Template(text=text)
            return template.render(**variables)
        except Exception as e:
            logger.error(f'渲染文本异常: text = {text}, variables = {str(variables)}, {str(e)}', exc_info=True)
            return text
