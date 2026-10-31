    @staticmethod
    def clear(temp_path: Path, days: int):
        """
        清理指定目录中指定天数前的文件，递归删除子文件及空文件夹
        """
        if not temp_path.exists():
            return
        # 遍历目录及子目录中的所有文件和文件夹
        for file in temp_path.rglob('*'):
            # 如果是文件并且符合时间条件，则删除
            if file.is_file() and (
                    datetime.datetime.now() - datetime.datetime.fromtimestamp(file.stat().st_mtime)).days > days:
                file.unlink()
        # 删除空的文件夹
        for folder in sorted(temp_path.rglob('*'), reverse=True):
            # 确保是空文件夹
            if folder.is_dir() and not any(folder.iterdir()):
                folder.rmdir()
