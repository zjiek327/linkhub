"""用户认证与 RBAC：JWT 签发/校验、角色权限、设备粒度授权。

角色：
- admin     全部权限 + 用户管理
- operator  设备操作（开终端/写命令/批量执行）+ 台账读写
- viewer    只读（看列表/看终端旁观，不能申请输入、不能写）

首次启动自动创建 admin/admin（强制提示修改密码）。
"""
import hashlib
import secrets
import time
from datetime import datetime, timezone

import jwt
from fastapi import Depends, HTTPException, Request
from pydantic import BaseModel
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from .config import get_settings
from .database import get_db
from .models import AuditLog, User

ROLES = ("admin", "operator", "viewer")
JWT_ALG = "HS256"


def hash_password(password: str, salt: str = "") -> str:
    salt = salt or secrets.token_hex(8)
    digest = hashlib.pbkdf2_hmac("sha256", password.encode(), salt.encode(), 100_000)
    return f"{salt}${digest.hex()}"


def verify_password(password: str, stored: str) -> bool:
    try:
        salt, _ = stored.split("$", 1)
    except ValueError:
        return False
    return secrets.compare_digest(hash_password(password, salt), stored)


def make_token(user: User) -> str:
    settings = get_settings()
    payload = {"sub": str(user.id), "role": user.role, "name": user.name,
               "exp": time.time() + 7 * 24 * 3600}
    return jwt.encode(payload, settings.secret_key, algorithm=JWT_ALG)


def decode_token(token: str) -> dict:
    try:
        return jwt.decode(token, get_settings().secret_key, algorithms=[JWT_ALG])
    except jwt.PyJWTError:
        raise HTTPException(401, "登录已过期，请重新登录")


async def current_user(request: Request, db: AsyncSession = Depends(get_db)) -> User:
    """从 Authorization: Bearer 或 cookie 解析当前用户。"""
    token = ""
    auth = request.headers.get("Authorization", "")
    if auth.startswith("Bearer "):
        token = auth[7:]
    if not token:
        token = request.cookies.get("linkhub_token", "")
    if not token:
        raise HTTPException(401, "未登录")
    payload = decode_token(token)
    user = await db.get(User, int(payload["sub"]))
    if user is None or not user.enabled:
        raise HTTPException(401, "用户不存在或已禁用")
    return user


def require_role(*roles: str):
    async def checker(user: User = Depends(current_user)) -> User:
        if user.role not in roles:
            raise HTTPException(403, f"需要角色: {'/'.join(roles)}")
        return user
    return checker


# 便捷依赖
require_admin = require_role("admin")
require_operator = require_role("admin", "operator")
require_viewer = require_role("admin", "operator", "viewer")   # 登录即可


async def ensure_default_admin(db: AsyncSession) -> None:
    """首次启动创建默认 admin/admin。"""
    row = (await db.execute(select(User).where(User.name == "admin"))).scalar_one_or_none()
    if row is None:
        db.add(User(name="admin", password_hash=hash_password("admin"), role="admin"))
        await db.commit()


async def audit(db: AsyncSession, user: str, action: str, target: str = "", detail: str = "") -> None:
    db.add(AuditLog(user=user, action=action, target=target, detail=detail,
                    ts=datetime.now(timezone.utc)))
    await db.commit()


class LoginIn(BaseModel):
    name: str
    password: str


class UserIn(BaseModel):
    name: str
    password: str = ""
    role: str = "viewer"
    enabled: bool = True


class UserOut(BaseModel):
    id: int
    name: str
    role: str
    enabled: bool
