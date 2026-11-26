/**
 * 远端玩家层：把服务端 player_move 广播渲染为同屏其他玩家（② Match handler 的客户端承接）。
 * 位置由服务端下发，客户端只表现；断线/离开自动移除。
 */
import { Node, Sprite, UITransform, _decorator } from 'cc';
import { PlayerMove } from '../core/Protocol';
import { loadFrame } from './CocosResourceLoader';

const { ccclass } = _decorator;

@ccclass('RemotePlayerLayer')
export class RemotePlayerLayer {
  private nodes = new Map<string, Node>();

  constructor(private root: Node) {}

  /** 接 NetworkManager.on('player_move')：更新/新增远端玩家 */
  apply(msg: PlayerMove): void {
    let n = this.nodes.get(msg.playerId);
    if (!n) {
      n = new Node('Remote_' + msg.playerId);
      const t = n.addComponent(UITransform);
      t.setContentSize(96, 96);
      n.addComponent(Sprite);
      loadFrame('characters/char_hero_mage_idle_f00')
        .then(f => { const sp = n!.getComponent(Sprite); if (sp) sp.spriteFrame = f; })
        .catch(() => {});
      this.root.addChild(n);
      this.nodes.set(msg.playerId, n);
    }
    n.setPosition(msg.x - 1920, 1080 - msg.y, 0);
    if (msg.action === 'battle') n.active = true;
  }

  remove(playerId: string): void {
    const n = this.nodes.get(playerId);
    if (n) { n.destroy(); this.nodes.delete(playerId); }
  }

  clear(): void {
    this.nodes.forEach(n => n.destroy());
    this.nodes.clear();
  }

  get count(): number { return this.nodes.size; }
}
