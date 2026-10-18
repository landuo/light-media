def reset(
    db: AsyncSession = Depends(get_db), _: User = Depends(get_current_active_superuser)
) -> Any:
    """
    清空所有站点数据并重新同步CookieCloud站点信息
    """
    Site.reset(db)
    SystemConfigOper().set(SystemConfigKey.IndexerSites, [])
    SystemConfigOper().set(SystemConfigKey.RssSites, [])
    # 启动定时服务
    Scheduler().start("cookiecloud", manual=True)
    # 插件站点删除
    eventmanager.send_event(EventType.SiteDeleted, {"site_id": "*"})
    return schemas.Response(success=True, message="站点已重置！")
