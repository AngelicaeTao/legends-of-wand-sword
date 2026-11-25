/**
 * 启动引导：读取连接配置 → 加载游戏配置 → 连接服务端 → 登录 → 拉取快照。
 * 这是 Cocos 主场景挂的唯一入口脚本；具体 UI 绑定交由各 Prefab 控制器。
 *
 * 连接配置来源：connection_info.json（由联机脚本生成，含 host/port/useSSL/serverKey），
 * 地址每次启动会变，绝不硬编码。
 */
import { ConfigManager } from './ConfigManager';
import { NetworkManager } from './NetworkManager';
import { GameState } from './GameState';

export interface ConnectionInfo {
  host: string;
  port: number;
  useSSL: boolean;
  serverKey: string;
  httpBase: string;
}

export class Bootstrap {
  static async start(info: ConnectionInfo): Promise<{
    net: NetworkManager; cfg: ConfigManager; state: GameState;
  }> {
    // 1) 配置（先走 HTTP，Web 构建；原生构建改为 resources.load）
    const cfg = ConfigManager.instance;
    await cfg.loadFromServer(info.httpBase);

    // 2) 状态
    const state = new GameState();

    // 3) 网络
    const net = new NetworkManager({
      host: info.host, port: info.port, useSSL: info.useSSL, serverKey: info.serverKey,
    });
    net.on('state_snapshot', (data) => state.applySnapshot(data));
    net.on('battle_report', (data) => state.applyReport(data));

    net.connect();
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('连接超时')), 10000);
      net.onStateChange = (s) => {
        if (s === 'open') { clearTimeout(timer); resolve(); }
      };
    });

    // 4) 设备登录（取 deviceId：浏览器存 localStorage，原生存设备唯一ID）
    let deviceId = this.getDeviceId();
    await net.request('login', { deviceId });

    return { net, cfg, state };
  }

  private static getDeviceId(): string {
    const key = 'zjcs_device_id';
    let id = '';
    try { id = localStorage.getItem(key) || ''; } catch (e) {}
    if (!id) {
      id = 'dev_' + Math.random().toString(36).slice(2) + Date.now().toString(36);
      try { localStorage.setItem(key, id); } catch (e) {}
    }
    return id;
  }
}
