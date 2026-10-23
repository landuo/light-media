    def clone_plugin(self, plugin_id: str, suffix: str, name: str, description: str,
                     version: str = None, icon: str = None) -> Tuple[bool, str]:
        """
        创建插件分身
        :param plugin_id: 原插件ID
        :param suffix: 分身后缀
        :param name: 分身名称
        :param description: 分身描述
        :param version: 自定义版本号
        :param icon: 自定义图标URL
        :return: (是否成功, 错误信息)
        """
        try:
            # 验证参数
            if not plugin_id or not suffix:
                return False, "插件ID和分身后缀不能为空"

            # 检查原插件是否存在
            if plugin_id not in self._plugins:
                return False, f"原插件 {plugin_id} 不存在"

            # 生成分身插件ID
            clone_id = f"{plugin_id}{suffix.lower()}"

            # 检查分身插件是否已存在
            if self.is_plugin_exists(clone_id):
                return False, f"分身插件 {clone_id} 已存在"

            # 获取原插件目录
            original_plugin_dir = Path(settings.ROOT_PATH) / "app" / "plugins" / plugin_id.lower()
            if not original_plugin_dir.exists():
                return False, f"原插件目录 {original_plugin_dir} 不存在"

            # 创建分身插件目录
            clone_plugin_dir = Path(settings.ROOT_PATH) / "app" / "plugins" / clone_id.lower()

            # 复制插件目录
            import shutil
            shutil.copytree(original_plugin_dir, clone_plugin_dir)
            logger.info(f"已复制插件目录：{original_plugin_dir} -> {clone_plugin_dir}")

            # 修改插件文件内容
            success, msg = self._modify_plugin_files(
                plugin_dir=clone_plugin_dir,
                original_id=plugin_id,
                suffix=suffix.lower(),
                name=name,
                description=description,
                version=version,
                icon=icon
            )

            if not success:
                # 如果修改失败，清理已创建的目录
                if clone_plugin_dir.exists():
                    shutil.rmtree(clone_plugin_dir)
                return False, msg

            # 将分身插件添加到已安装列表
            systemconfig = SystemConfigOper()
            installed_plugins = systemconfig.get(SystemConfigKey.UserInstalledPlugins) or []
            if clone_id not in installed_plugins:
                installed_plugins.append(clone_id)
                systemconfig.set(SystemConfigKey.UserInstalledPlugins, installed_plugins)

            # 为分身插件创建初始配置（从原插件复制配置）
            logger.info(f"正在为分身插件 {clone_id} 创建初始配置...")
            original_config = self.get_plugin_config(plugin_id)
            if original_config:
                # 复制原插件配置作为分身插件的初始配置
                clone_config = original_config.copy()
                # 可以在这里修改一些默认值，比如禁用分身插件
                # 默认禁用分身插件，让用户手动配置
                clone_config['enable'] = False
                clone_config['enabled'] = False
                self.save_plugin_config(clone_id, clone_config, force=True)
                logger.info(f"已为分身插件 {clone_id} 设置初始配置")
            else:
                logger.info(f"原插件 {plugin_id} 没有配置，分身插件 {clone_id} 将使用默认配置")

            # 注册分身插件的API和服务
            logger.info(f"正在注册分身插件 {clone_id} ...")
            PluginManager().reload_plugin(clone_id)
            # 确保分身插件正确初始化配置
            if clone_id in self._running_plugins:
                clone_instance = self._running_plugins[clone_id]
                clone_config = self.get_plugin_config(clone_id)
                if clone_config:
                    logger.info(f"正在为分身插件 {clone_id} 重新初始化配置...")
                    clone_instance.init_plugin(clone_config)
                    logger.info(f"分身插件 {clone_id} 配置重新初始化完成")

            logger.info(f"插件分身 {clone_id} 创建成功")
            return True, clone_id

        except Exception as e:
            logger.error(f"创建插件分身失败：{str(e)}")
            return False, f"创建插件分身失败：{str(e)}"

