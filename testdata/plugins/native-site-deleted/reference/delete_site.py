async def delete_site(
    site_id: int,
    db: AsyncSession = Depends(get_async_db),
    _: User = Depends(get_current_active_manage_user_async),
) -> Any:
    """
    删除站点
    """
    await Site.async_delete(db, site_id)
    # 插件站点删除
    await eventmanager.async_send_event(EventType.SiteDeleted, {"site_id": site_id})
    return schemas.Response(success=True)
