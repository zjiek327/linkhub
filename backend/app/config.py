"""全局配置：环境变量 LINKHUB_* 覆盖。"""
from functools import lru_cache
from pathlib import Path

from pydantic_settings import BaseSettings, SettingsConfigDict

BASE_DIR = Path(__file__).resolve().parent.parent  # backend/


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="LINKHUB_", env_file=".env", extra="ignore")

    app_name: str = "灵枢 LinkHub"
    database_url: str = f"sqlite+aiosqlite:///{BASE_DIR / 'linkhub.db'}"
    builtin_templates_dir: Path = BASE_DIR / "templates_builtin"
    # 凭证加密主密钥，生产环境必须通过 LINKHUB_SECRET_KEY 注入
    secret_key: str = "linkhub-dev-secret-change-me"
    # 会话日志落盘目录
    session_log_dir: Path = BASE_DIR / "session_logs"
    # 串口自动重连
    reconnect_enabled: bool = True
    reconnect_max_delay: float = 30.0  # 秒，指数退避上限

    cors_origins: list[str] = ["http://localhost:5173", "http://127.0.0.1:5173"]


@lru_cache
def get_settings() -> Settings:
    return Settings()
