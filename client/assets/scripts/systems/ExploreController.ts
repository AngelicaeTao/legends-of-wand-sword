/**
 * 大世界探索控制器：横版 45° XY 走动 + 遇怪交战/绕行选择。
 * 坐标换算、警戒半径、点位状态来自配置；服务端权威。
 */
import { ConfigManager } from '../core/ConfigManager';
import { NetworkManager } from '../core/NetworkManager';

export interface Vec2 { x: number; y: number }

export class ExploreController {
  private pos: Vec2 = { x: 1920, y: 1080 };

  constructor(private net: NetworkManager, private view: {
    setActorPos(p: Vec2): void;
    showEncounter(monsterName: string, onEngage: () => void, onAvoid: () => void): void;
    hideEncounter(): void;
  }) {}

  /** 屏幕坐标 → 地面坐标（45° 俯视投影）。原型阶段先用方格标定。 */
  screenToGround(sx: number, sy: number, camera: Vec2): Vec2 {
    // 45° 俯视：屏幕 y 压缩到地面 y 的一半
    return { x: camera.x + sx, y: camera.y + sy * 2 };
  }

  /** 虚拟摇杆移动：按速度积分，并广播给同线玩家 */
  moveBy(dx: number, dy: number, dtSec: number): void {
    const speed = Number(ConfigManager.instance.get().economy.worldMap.moveSpeed) || 240;
    this.pos.x += dx * speed * dtSec;
    this.pos.y += dy * speed * dtSec;
    this.view.setActorPos(this.pos);
    this.net.send('move', { x: Math.round(this.pos.x), y: Math.round(this.pos.y), ts: Date.now(), action: 'walk' });
    this.checkEncounter();
  }

  /** 检测进入怪物警戒半径，弹交战/绕行（只弹最近一只） */
  private checkEncounter(): void {
    const cfg = ConfigManager.instance;
    const world = cfg.worldById('W1');
    if (!world) return;
    const radius = Number(cfg.get().economy.worldMap.aggroRadius) || 120;
    let nearest: any = null, minD = Infinity;
    for (const sp of world.spawns) {
      if (sp.type !== 'MOB' && sp.type !== 'ELITE' && sp.type !== 'BOSS') continue;
      const d = Math.hypot(sp.x - this.pos.x, sp.y - this.pos.y);
      if (d < radius && d < minD) { minD = d; nearest = sp; }
    }
    if (nearest) {
      const mon = cfg.monsterById(nearest.bossMonster || 'slime');
      this.view.showEncounter(mon?.name || '怪物',
        () => { this.net.send('engage', { monsterId: mon?.id, monsterLv: world.level, elite: nearest.type === 'ELITE' ? 'elite' : '' }); this.view.hideEncounter(); },
        () => { this.view.hideEncounter(); /* 绕行：怪物短暂脱战，服务端处理冷却 */ });
    }
  }
}
