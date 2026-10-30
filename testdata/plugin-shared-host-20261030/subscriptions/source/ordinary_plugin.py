"""自建普通插件：仅用于独立观察真实订阅 DAO，不包含被测实现。"""

from app.db.subscribe_oper import SubscribeOper


class OwnedSubscriptionsPlugin:
    """通过正常构造的原版 DAO 暴露四个普通业务调用。"""

    plugin_name = "Owned subscription observations"

    def init_plugin(self, config=None):
        """初始化自建插件，不启动外部系统。"""
        self.oper = SubscribeOper()

    def get(self, **kwargs):
        """调用原版 get。"""
        return self.oper.get(**kwargs)

    def list(self, **kwargs):
        """调用原版 list。"""
        return self.oper.list(**kwargs)

    def list_by_tmdbid(self, **kwargs):
        """调用原版 list_by_tmdbid。"""
        return self.oper.list_by_tmdbid(**kwargs)

    def update(self, **kwargs):
        """调用原版 update，不替换返回对象。"""
        return self.oper.update(**kwargs)
