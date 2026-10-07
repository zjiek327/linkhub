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

    # ---- 集群模式（默认关闭 = 单机） ----
    cluster_enabled: bool = False
    cluster_token: str = ""                    # 集群令牌，所有节点一致
    node_name: str = ""                        # 默认取主机名
    advertise_addr: str = "http://127.0.0.1:8000"  # 其他节点回连本节点用的地址
    cluster_mdns: bool = True                  # mDNS 自动发现（C2）
    heartbeat_interval: float = 5.0            # 秒
    heartbeat_timeout: float = 15.0            # 秒，超时判离线
    directory_sync_interval: float = 60.0      # 秒，目录反熵对账周期
    resource_watch_interval: float = 2.0       # 秒，串口热插拔轮询

    cors_origins: list[str] = ["http://localhost:5173", "http://127.0.0.1:5173"]


@lru_cache
def get_settings() -> Settings:
    return Settings()
