"""全局配置：环境变量 LINKHUB_* 覆盖。"""
import json
from functools import lru_cache
from pathlib import Path

from pydantic import field_validator
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
    discovery_beacon: bool = True              # UDP 广播发现（单网段更可靠）
    discovery_port: int = 37890
    # 定向对端地址（广播被网络过滤时用）。支持 JSON 数组或逗号分隔字符串
    cluster_peers: str = ""
    heartbeat_interval: float = 5.0            # 秒
    heartbeat_timeout: float = 15.0            # 秒，超时判离线
    directory_sync_interval: float = 60.0      # 秒，目录反熵对账周期
    resource_watch_interval: float = 2.0       # 秒，串口热插拔轮询

    cors_origins: list[str] = ["http://localhost:5173", "http://127.0.0.1:5173"]

    def peers_list(self) -> list[str]:
        """cluster_peers → 地址列表。兼容 JSON 数组 / 逗号或空格分隔。"""
        v = self.cluster_peers.strip()
        if not v:
            return []
        if v.startswith("["):
            return [x.strip() for x in json.loads(v) if x.strip()]
        return [x.strip() for x in v.replace(",", " ").split() if x.strip()]


@lru_cache
def get_settings() -> Settings:
    return Settings()
