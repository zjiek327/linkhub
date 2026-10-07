"""凭证加解密：AES-GCM，密钥由 LINKHUB_SECRET_KEY 派生。"""
import base64
import hashlib
import os

from cryptography.hazmat.primitives.ciphers.aead import AESGCM

from ..config import get_settings


def _key() -> bytes:
    return hashlib.sha256(get_settings().secret_key.encode()).digest()


def encrypt(plaintext: str) -> str:
    nonce = os.urandom(12)
    ct = AESGCM(_key()).encrypt(nonce, plaintext.encode(), None)
    return base64.b64encode(nonce + ct).decode()


def decrypt(token: str) -> str:
    raw = base64.b64decode(token)
    return AESGCM(_key()).decrypt(raw[:12], raw[12:], None).decode()
