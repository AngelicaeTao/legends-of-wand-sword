/**
 * 战报表现器：把服务端下发的 battle_report 逐回合演出。
 * 关键口径（需求文档/一致性报告反复强调）：
 *  - 战斗为自动演出，玩家零输入；
 *  - 底部技能栏是只读回显（4 已装 + 普攻），不是可点击按钮；
 *  - 客户端只表现，不结算、不接受战斗中的操作。
 */
import { BattleReport, BattleRound } from '../core/Protocol';
import { ConfigManager } from '../core/ConfigManager';

export class BattlePresenter {
  private queue: BattleRound[] = [];
  private playing = false;
  private idx = 0;

  /** 注入 Cocos 节点引用（由场景绑定） */
  constructor(private view: {
    setHeroHp(hp: number): void;
    setMonHp(hp: number): void;
    showFx(fx: string, value: number, target: 'hero' | 'monster'): void;
    appendLog(text: string): void;
    onFinish(result: string): void;
  }) {}

  play(report: BattleReport, intervalMs = 400): void {
    this.queue = report.rounds;
    this.idx = 0;
    this.playing = true;
    const cfg = ConfigManager.instance;
    const tick = () => {
      if (!this.playing || this.idx >= this.queue.length) {
        this.playing = false;
        this.view.onFinish(report.result);
        return;
      }
      const r = this.queue[this.idx++];
      this.renderRound(r, cfg);
      setTimeout(tick, intervalMs);
    };
    tick();
  }

  private renderRound(r: BattleRound, cfg: ConfigManager): void {
    this.view.setHeroHp(r.heroHp);
    this.view.setMonHp(r.monHp);
    if (r.damage > 0) {
      this.view.showFx(r.fx, r.damage, r.actor === 'hero' ? 'monster' : 'hero');
    } else if (r.heal > 0) {
      this.view.showFx(r.fx, r.heal, 'hero');
    }
    const who = r.actor === 'hero' ? '我方' : '敌方';
    const act = r.damage > 0 ? `造成 ${r.damage} 伤害`
      : r.heal > 0 ? `回复 ${r.heal} 生命`
      : `施放 ${r.skillName}`;
    this.view.appendLog(`#${r.n} ${who}·${r.skillName} → ${act}`);
  }

  stop(): void { this.playing = false; }
}
