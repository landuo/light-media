"""
LarkMessager 消息加解密
Lark 事件回调加密采用 AES-256-CBC，IV 取密文前 16 字节
参考官方文档：https://open.feishu.cn/document/server-docs/event-subscription-guide/event-subscription-configure-/encrypt-key-encryption-configuration-case
"""

import base64
import hashlib
import hmac
from Crypto.Cipher import AES

from app.log import logger


class LarkCrypto:
    """
    Lark事件订阅加解密
    """

    def __init__(self, encrypt_key: str = "", app_secret: str = ""):
        """
        :param encrypt_key: Lark应用后台配置的 Encrypt Key
        :param app_secret: Lark应用 App Secret（保留参数，当前算法未用到）
        """
        self._encrypt_key = encrypt_key
        self._app_secret = app_secret
        # AES-256-CBC：key = SHA-256(encrypt_key) 的完整 32 字节 digest
        # IV 不从 encrypt_key 推导，而是从密文前 16 字节取出（见 decrypt）
        self._key = hashlib.sha256(encrypt_key.encode("utf-8")).digest() if encrypt_key else None

    def verify_signature(self, signature_header: str, raw_body: bytes,
                         timestamp: str = "", nonce: str = "") -> bool:
        """
        校验 X-Lark-Signature 头
        Lark 官方算法：sha256(timestamp + nonce + encrypt_key + body) 的小写 hex 字符串

        :param signature_header: 请求头 X-Lark-Signature 的值（纯 hex，无前缀）
        :param raw_body: 原始请求体 bytes
        :param timestamp: 请求头 X-Lark-Request-Timestamp 的值
        :param nonce: 请求头 X-Lark-Request-Nonce 的值
        :return: 是否校验通过
        """
        if not signature_header or not self._encrypt_key:
            return False
        if not timestamp or not nonce:
            logger.warning("缺少 X-Lark-Request-Timestamp 或 X-Lark-Request-Nonce 头")
            return False
        try:
            body_str = raw_body.decode("utf-8")
            sign_string = f"{timestamp}{nonce}{self._encrypt_key}{body_str}"
            computed = hashlib.sha256(sign_string.encode("utf-8")).hexdigest().lower()
            return hmac.compare_digest(computed, signature_header.lower())
        except Exception as e:
            logger.error("校验签名失败：%s", e)
            return False

    def decrypt(self, encrypted_data: str) -> str:
        """
        解密Lark回调数据
        算法（参考官方 Python 示例）：
        - key = SHA-256(encrypt_key) 的 32 字节
        - IV = base64decode(encrypted_data) 的前 16 字节
        - 密文 = base64decode(encrypted_data) 的第 16 字节之后
        - AES-256-CBC 解密后去除 PKCS7 填充

        :param encrypted_data: Base64 编码的加密字符串（含前 16 字节 IV）
        :return: 解密后的 JSON 明文
        """
        if not self._key:
            raise ValueError("encrypt_key 未配置，无法解密，请在插件配置中填写 Encrypt Key")
        encrypted_bytes = base64.b64decode(encrypted_data)
        if len(encrypted_bytes) < AES.block_size:
            raise ValueError(f"密文长度不足 {AES.block_size} 字节，无法提取 IV")
        iv = encrypted_bytes[:AES.block_size]  # 前 16 字节是 IV
        ciphertext = encrypted_bytes[AES.block_size:]  # 后面是真正的密文
        cipher = AES.new(self._key, AES.MODE_CBC, iv)
        decrypted = cipher.decrypt(ciphertext)
        # 去除 PKCS7 填充
        pad_len = decrypted[-1]
        plaintext = decrypted[:-pad_len]
        return plaintext.decode("utf-8")

    def encrypt(self, plaintext: str) -> str:
        """
        加密响应数据（用于回调解密模式）
        :param plaintext: 明文 JSON 字符串
        :return: Base64 编码的加密字符串（前 16 字节是随机 IV）
        """
        if not self._key:
            raise ValueError("encrypt_key 未配置，无法加密，请在插件配置中填写 Encrypt Key")
        import os
        iv = os.urandom(AES.block_size)  # 随机 16 字节 IV
        cipher = AES.new(self._key, AES.MODE_CBC, iv)
        data = plaintext.encode("utf-8")
        # PKCS7 填充
        pad_len = AES.block_size - len(data) % AES.block_size
        data += bytes([pad_len]) * pad_len
        encrypted = cipher.encrypt(data)
        return base64.b64encode(iv + encrypted).decode("utf-8")
