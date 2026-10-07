"""认证 + 用户管理 + 凭证管理 + 审计查询 API。"""
from fastapi import APIRouter, Depends, HTTPException, Response
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from ..auth import (LoginIn, UserIn, UserOut, audit, current_user, ensure_default_admin,
                    hash_password, make_token, require_admin, require_operator,
                    verify_password)
from ..database import get_db
from ..models import AuditLog, Credential, User
from ..schemas import CredentialIn, CredentialOut
from ..services.crypto import encrypt

router = APIRouter(prefix="/api", tags=["auth"])


@router.post("/auth/login")
async def login(body: LoginIn, response: Response, db: AsyncSession = Depends(get_db)):
    row = (await db.execute(select(User).where(User.name == body.name))).scalar_one_or_none()
    if row is None or not verify_password(body.password, row.password_hash):
        raise HTTPException(401, "用户名或密码错误")
    if not row.enabled:
        raise HTTPException(403, "用户已禁用")
    token = make_token(row)
    response.set_cookie("linkhub_token", token, httponly=True, samesite="lax",
                        max_age=7 * 24 * 3600)
    await audit(db, row.name, "login")
    return {"token": token, "user": UserOut(id=row.id, name=row.name,
                                             role=row.role, enabled=row.enabled)}


@router.post("/auth/logout")
async def logout(response: Response):
    response.delete_cookie("linkhub_token")
    return {"ok": True}


@router.get("/auth/me", response_model=UserOut)
async def me(user: User = Depends(current_user)):
    return UserOut(id=user.id, name=user.name, role=user.role, enabled=user.enabled)


# ---------- 用户管理（admin） ----------
@router.get("/users", response_model=list[UserOut], dependencies=[Depends(require_admin)])
async def list_users(db: AsyncSession = Depends(get_db)):
    rows = (await db.execute(select(User).order_by(User.id))).scalars().all()
    return [UserOut(id=r.id, name=r.name, role=r.role, enabled=r.enabled) for r in rows]


@router.post("/users", response_model=UserOut, status_code=201,
             dependencies=[Depends(require_admin)])
async def create_user(body: UserIn, db: AsyncSession = Depends(get_db)):
    if body.role not in ("admin", "operator", "viewer"):
        raise HTTPException(400, "角色必须是 admin/operator/viewer")
    if not body.password:
        raise HTTPException(400, "必须设置初始密码")
    exists = (await db.execute(select(User).where(User.name == body.name))).scalar_one_or_none()
    if exists:
        raise HTTPException(409, "用户已存在")
    row = User(name=body.name, password_hash=hash_password(body.password),
               role=body.role, enabled=body.enabled)
    db.add(row)
    await db.commit()
    await audit(db, "admin", "user_create", body.name)
    return UserOut(id=row.id, name=row.name, role=row.role, enabled=row.enabled)


@router.put("/users/{user_id}", response_model=UserOut, dependencies=[Depends(require_admin)])
async def update_user(user_id: int, body: UserIn, db: AsyncSession = Depends(get_db),
                      cur: User = Depends(current_user)):
    row = await db.get(User, user_id)
    if row is None:
        raise HTTPException(404, "用户不存在")
    if body.role not in ("admin", "operator", "viewer"):
        raise HTTPException(400, "角色必须是 admin/operator/viewer")
    row.role, row.enabled = body.role, body.enabled
    if body.password:
        row.password_hash = hash_password(body.password)
    await db.commit()
    await audit(db, cur.name, "user_update", row.name)
    return UserOut(id=row.id, name=row.name, role=row.role, enabled=row.enabled)


@router.delete("/users/{user_id}", status_code=204, dependencies=[Depends(require_admin)])
async def delete_user(user_id: int, db: AsyncSession = Depends(get_db),
                      cur: User = Depends(current_user)):
    if user_id == cur.id:
        raise HTTPException(400, "不能删除自己")
    row = await db.get(User, user_id)
    if row is None:
        raise HTTPException(404, "用户不存在")
    await audit(db, cur.name, "user_delete", row.name)
    await db.delete(row)
    await db.commit()


# ---------- 凭证管理（admin/operator 可建，列表只返回名字） ----------
@router.get("/credentials", response_model=list[CredentialOut])
async def list_credentials(db: AsyncSession = Depends(get_db),
                           _: User = Depends(require_operator)):
    rows = (await db.execute(select(Credential).order_by(Credential.id))).scalars().all()
    return [CredentialOut(id=r.id, name=r.name, type=r.type) for r in rows]


@router.post("/credentials", response_model=CredentialOut, status_code=201)
async def create_credential(body: CredentialIn, db: AsyncSession = Depends(get_db),
                            cur: User = Depends(require_operator)):
    exists = (await db.execute(select(Credential).where(Credential.name == body.name))).scalar_one_or_none()
    if exists:
        raise HTTPException(409, "凭证名已存在")
    row = Credential(name=body.name, type=body.type, secret_enc=encrypt(body.secret))
    db.add(row)
    await db.commit()
    await db.refresh(row)
    await audit(db, cur.name, "credential_create", body.name)
    return CredentialOut(id=row.id, name=row.name, type=row.type)


@router.delete("/credentials/{cred_id}", status_code=204)
async def delete_credential(cred_id: int, db: AsyncSession = Depends(get_db),
                            cur: User = Depends(require_operator)):
    row = await db.get(Credential, cred_id)
    if row is None:
        raise HTTPException(404, "凭证不存在")
    await audit(db, cur.name, "credential_delete", row.name)
    await db.delete(row)
    await db.commit()


# ---------- 审计（admin） ----------
@router.get("/audit", dependencies=[Depends(require_admin)])
async def list_audit(limit: int = 100, db: AsyncSession = Depends(get_db)):
    rows = (await db.execute(select(AuditLog).order_by(AuditLog.id.desc()).limit(limit))).scalars().all()
    return [{"id": r.id, "user": r.user, "action": r.action, "target": r.target,
             "detail": r.detail, "ts": r.ts.isoformat() if r.ts else None} for r in rows]
