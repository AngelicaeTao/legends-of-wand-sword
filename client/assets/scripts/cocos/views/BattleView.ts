/**
 * 战斗视图：代码驱动构建战斗界面（不依赖场景里手摆节点）。
 * 接 BattlePresenter 的 view 接口，把服务端战报逐回合演出。
 * 口径：战斗零输入、底部技能栏只读回显。
 */
import { Node, Sprite, Label, Color, UITransform, _decorator } from 'cc';
import { BattlePresenter } from '../../systems/BattlePresenter';
import { BattleReport } from '../../core/Protocol';
import { PixelAnimator } from '../anim/PixelAnimator';

const { ccclass } = _decorator;

function mkNode(name: string, w: number, h: number, x: number, y: number): Node {
  const n = new Node(name);
  const t = n.addComponent(UITransform);
  t.setContentSize(w, h);
  n.setPosition(x, y, 0);
  return n;
}

function mkLabel(name: string, text: string, size: number, color: Color, w: number, h: number, x: number, y: number): Label {
  const n = mkNode(name, w, h, x, y);
  const l = n.addComponent(Label);
  l.string = text;
  l.fontSize = size;
  l.lineHeight = size + 4;
  l.color = color;
  l.overflow = Label.Overflow.SHRINK;
  return l;
}

@ccclass('BattleView')
export class BattleView {
  private presenter: BattlePresenter;
  private heroAnim: PixelAnimator | null = null;
  private monAnim: PixelAnimator | null = null;
  private heroHpLabel: Label;
  private monHpLabel: Label;
  private logLabel: Label;
  private fxLabel: Label;

  constructor(private root: Node, onFinish?: (result: string) => void) {
    const W = 1536, H = 1024;

    // 英雄（左）与怪物（右）
    const hero = mkNode('Hero', 128, 128, -420, -60);
    this.heroAnim = hero.addComponent(PixelAnimator);
    hero.addComponent(Sprite);
    this.root.addChild(hero);

    const mon = mkNode('Monster', 128, 128, 380, -60);
    this.monAnim = mon.addComponent(PixelAnimator);
    mon.addComponent(Sprite);
    this.root.addChild(mon);

    this.heroHpLabel = mkLabel('HeroHp', '我方 HP --', 40, Color.WHITE, 400, 60, -420, -170);
    this.monHpLabel = mkLabel('MonHp', '敌方 HP --', 40, Color.WHITE, 400, 60, 380, -170);
    this.root.addChild(this.heroHpLabel.node);
    this.root.addChild(this.monHpLabel.node);

    this.fxLabel = mkLabel('FX', '', 64, new Color(255, 90, 60), 400, 80, 0, 60);
    this.root.addChild(this.fxLabel.node);

    this.logLabel = mkLabel('Log', '', 28, new Color(230, 230, 230), W - 120, 240, 0, -320);
    this.logLabel.horizontalAlign = Label.HorizontalAlign.LEFT;
    this.logLabel.verticalAlign = Label.VerticalAlign.TOP;
    this.root.addChild(this.logLabel.node);

    this.presenter = new BattlePresenter({
      setHeroHp: (hp) => { this.heroHpLabel.string = '我方 HP ' + hp; },
      setMonHp: (hp) => { this.monHpLabel.string = '敌方 HP ' + hp; },
      showFx: (fx, value, target) => {
        this.fxLabel.string = (target === 'hero' ? '-' : '+') + value;
        this.fxLabel.node.setPosition(target === 'hero' ? -420 : 380, 60, 0);
        this.fxLabel.color = fx.indexOf('heal') >= 0 ? new Color(90, 230, 120) : new Color(255, 90, 60);
      },
      appendLog: (text) => { this.logLabel.string = this.logLabel.string + text + '\n'; },
      onFinish: (result) => onFinish?.(result),
    });
  }

  /** 由 GameRoot 注入动画规格（来自 animation_spec.json） */
  bindAnimSpec(animations: any[]): void {
    this.heroAnim?.setSpec(animations);
    this.monAnim?.setSpec(animations);
    this.heroAnim?.play('mage_idle').catch(() => {});
  }

  /** 播放战报。动画 id 由回合 fx/action 映射到动画组，缺失则退回 idle。 */
  play(report: BattleReport): void {
    this.logLabel.string = '';
    this.presenter.play(report, 400);
    report.rounds.forEach((r, i) => {
      if (r.actor === 'hero' && r.damage > 0) {
        setTimeout(() => this.heroAnim?.playOnce('mage_attack').catch(() => {}), i * 400);
      } else if (r.actor === 'monster' && r.damage > 0) {
        setTimeout(() => this.monAnim?.playOnce('slime_attack').catch(() => {}), i * 400);
      }
    });
  }

  destroy(): void { this.presenter.stop(); this.root.destroy(); }
}
