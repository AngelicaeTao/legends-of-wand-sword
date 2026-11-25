/**
 * Cocos 主入口组件：挂在 Main 场景根节点的唯一脚本。
 * 职责：加载配置/资源 → 连接网络 → 创角/读档 → 组装 HUD/世界/战斗视图 → 分派服务端推送。
 * 业务逻辑全部复用纯逻辑层（core/systems），本组件只做引擎装配与生命周期。
 */
import { Component, Node, SpriteFrame, _decorator } from 'cc';
import { ConfigManager } from '../core/ConfigManager';
import { NetworkManager } from '../core/NetworkManager';
import { GameState } from '../core/GameState';
import { PlayerMove } from '../core/Protocol';
import {
  loadConnectionInfo, loadConfigsLocal, loadFrame, releaseResources,
} from './CocosResourceLoader';
import { HUDView } from './views/HUDView';
import { WorldView } from './views/WorldView';
import { BattleView } from './views/BattleView';
import { RemotePlayerLayer } from './RemotePlayerLayer';

const { ccclass } = _decorator;

@ccclass('GameRoot')
export class GameRoot extends Component {
  private net: NetworkManager | null = null;
  private state: GameState | null = null;
  private hud: HUDView | null = null;
  private world: WorldView | null = null;
  private worldNode: Node | null = null;
  private battle: BattleView | null = null;
  private remote: RemotePlayerLayer | null = null;
  private animSpec: any[] = [];

  async start(): Promise<void> {
    const root = this.node;
    try {
      // 1) 本地资源：连接配置 + 游戏配置 + 动画规格
      const info = await loadConnectionInfo();
      await loadConfigsLocal();
      const specAsset: any = await new Promise((res, rej) => {
        // animation_spec.json 在 resources 根（JsonAsset）
        import('cc').then(({ resources, JsonAsset }) => {
          resources.load('animation_spec', JsonAsset, (e, a: any) => e || !a ? rej(e) : res(a.json));
        });
      });
      this.animSpec = specAsset.animations || [];

      // 2) 状态 + 网络
      this.state = new GameState();
      this.net = new NetworkManager({
        host: info.host, port: info.port, useSSL: info.useSSL, serverKey: info.serverKey,
      });

      // 3) 服务端推送分派（只读表现）
      this.net.on('state_snapshot', d => this.state!.applySnapshot(d));
      this.net.on('battle_report', d => this.showBattle(d));
      this.net.on('player_move', (d: PlayerMove) => this.remote?.apply(d));

      await this.connectWithTimeout(this.net);

      // 4) 设备登录（request 走 RPC，standalone 也可换成 HTTP 创角）
      const deviceId = this.deviceId();
      await this.net.request('login', { deviceId }).catch(() => {});

      // 5) 组装视图
      const hudNode = new Node('HUD');
      root.addChild(hudNode);
      this.hud = new HUDView(hudNode, this.state);

      const remoteNode = new Node('RemotePlayers');
      root.addChild(remoteNode);
      this.remote = new RemotePlayerLayer(remoteNode);

      this.showWorld();
    } catch (e) {
      console.error('[GameRoot] 启动失败', e);
    }
  }

  private connectWithTimeout(net: NetworkManager): Promise<void> {
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('连接超时')), 10000);
      net.onStateChange = s => {
        if (s === 'open') { clearTimeout(timer); resolve(); }
      };
      net.connect();
    });
  }

  private deviceId(): string {
    const k = 'zjcs_device_id';
    let id = '';
    try { id = localStorage.getItem(k) || ''; } catch (e) {}
    if (!id) {
      id = 'dev_' + Math.random().toString(36).slice(2) + Date.now().toString(36);
      try { localStorage.setItem(k, id); } catch (e) {}
    }
    return id;
  }

  private showWorld(): void {
    this.battle?.destroy(); this.battle = null;
    if (this.worldNode) { this.worldNode.active = true; return; }
    const node = new Node('WorldView');
    this.worldNode = node;
    this.node.addChild(node);
    this.world = new WorldView(node, this.net!, this.state!);
    this.world.bindAnimSpec(this.animSpec);
    const bg = (this.state?.snapshot?.worldId) || 'W1';
    const theme = ConfigManager.instance.worldById(bg)?.theme || 'grassland';
    loadFrame('backgrounds/bg_explore_' + theme)
      .then(f => this.world?.setBackground(f))
      .catch(() => {});
  }

  private showBattle(report: any): void {
    if (this.worldNode) this.worldNode.active = false;
    if (!this.battle) {
      const node = new Node('BattleView');
      this.node.addChild(node);
      this.battle = new BattleView(node, () => {
        this.battle?.destroy(); this.battle = null;
        if (this.worldNode) this.worldNode.active = true;
      });
      this.battle.bindAnimSpec(this.animSpec);
    }
    this.battle.play(report);
  }

  onDestroy(): void {
    this.net?.close();
    releaseResources();
  }
}
