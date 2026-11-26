/**
 * 大世界视图：代码驱动构建横版探索界面，接 ExploreController。
 * 含背景、主角、虚拟摇杆、遇怪弹窗；坐标与刷怪点全部来自配置，服务端权威。
 */
import { Node, Sprite, Label, Color, UITransform, Vec3, _decorator } from 'cc';
import { ExploreController, Vec2 } from '../../systems/ExploreController';
import { NetworkManager } from '../../core/NetworkManager';
import { GameState } from '../../core/GameState';
import { PixelAnimator } from '../anim/PixelAnimator';

const { ccclass } = _decorator;

function mkNode(name: string, w: number, h: number, x: number, y: number): Node {
  const n = new Node(name);
  const t = n.addComponent(UITransform);
  t.setContentSize(w, h);
  n.setPosition(x, y, 0);
  return n;
}

@ccclass('WorldView')
export class WorldView {
  private ctrl: ExploreController;
  private hero: Node;
  private heroAnim: PixelAnimator;
  private encounter: Node;
  private encounterLabel: Label;
  private bgSprite: Sprite;
  private dragging = false;
  private lastTouch: Vec2 = { x: 0, y: 0 };

  constructor(private root: Node, net: NetworkManager, private state: GameState) {
    const W = 1536, H = 1024;

    // 背景（从资源加载，由 GameRoot 注入）
    const bg = mkNode('BG', W, H, 0, 0);
    this.bgSprite = bg.addComponent(Sprite);
    this.root.addChild(bg);

    // 主角
    this.hero = mkNode('Hero', 128, 128, 0, 0);
    this.heroAnim = this.hero.addComponent(PixelAnimator);
    this.hero.addComponent(Sprite);
    this.root.addChild(this.hero);

    // 遇怪弹窗（默认隐藏）
    this.encounter = mkNode('Encounter', 700, 320, 0, -100);
    this.encounter.active = false;
    const mask = this.encounter.addComponent(Sprite);
    mask.color = new Color(20, 20, 30, 220);
    this.encounterLabel = this.label('EncText', '遇到怪物', 44, Color.WHITE, 660, 120, 0, 40);
    this.encounter.addChild(this.encounterLabel.node);
    const engage = this.button('EngageBtn', '交战', 200, 70, -150, -90);
    const avoid = this.button('AvoidBtn', '绕行', 200, 70, 150, -90);
    this.encounter.addChild(engage.node);
    this.encounter.addChild(avoid.node);
    this.root.addChild(this.encounter);

    this.ctrl = new ExploreController(net, {
      setActorPos: (p) => { this.hero.setPosition(p.x - 1920, 1080 - p.y, 0); },
      showEncounter: (name, onEngage, onAvoid) => {
        this.encounterLabel.string = '遇到 ' + name + '！';
        this.encounter.active = true;
        engage.node.off('click');
        avoid.node.off('click');
        engage.node.on('click', () => { this.hide(); onEngage(); });
        avoid.node.on('click', () => { this.hide(); onAvoid(); });
      },
      hideEncounter: () => this.hide(),
    });

    this.setupJoystick();
  }

  private label(name: string, text: string, size: number, color: Color, w: number, h: number, x: number, y: number): Label {
    const n = mkNode(name, w, h, x, y);
    const l = n.addComponent(Label);
    l.string = text; l.fontSize = size; l.lineHeight = size + 6; l.color = color;
    return l;
  }

  private button(name: string, text: string, w: number, h: number, x: number, y: number): Label {
    const n = mkNode(name, w, h, x, y);
    const sp = n.addComponent(Sprite);
    sp.color = new Color(70, 110, 180);
    const l = n.addComponent(Label);
    l.string = text; l.fontSize = 32; l.color = Color.WHITE;
    return l;
  }

  private hide(): void { this.encounter.active = false; }

  /** 虚拟摇杆：触摸拖动 → moveBy，广播给同线玩家 */
  private setupJoystick(): void {
    this.root.on(Node.EventType.TOUCH_START, (e: any) => {
      this.dragging = true;
      const p = e.getLocation();
      this.lastTouch = { x: p.x, y: p.y };
    }, this);
    this.root.on(Node.EventType.TOUCH_MOVE, (e: any) => {
      if (!this.dragging) return;
      const p = e.getLocation();
      const dx = p.x - this.lastTouch.x;
      const dy = p.y - this.lastTouch.y;
      this.lastTouch = { x: p.x, y: p.y };
      this.ctrl.moveBy(dx / 100, dy / 100, 1 / 30);
      this.heroAnim?.play('mage_walk').catch(() => {});
    }, this);
    this.root.on(Node.EventType.TOUCH_END, () => {
      this.dragging = false;
      this.heroAnim?.play('mage_idle').catch(() => {});
    }, this);
  }

  bindAnimSpec(animations: any[]): void {
    this.heroAnim.setSpec(animations);
    this.heroAnim.play('mage_idle').catch(() => {});
  }

  /** 由 GameRoot 注入背景帧（bg_*.png 已加进 resources） */
  setBackground(frame: any): void { if (frame) this.bgSprite.spriteFrame = frame; }

  screenToGround(sx: number, sy: number): Vec2 { return this.ctrl.screenToGround(sx, sy, { x: 1920, y: 1080 }); }

  destroy(): void { this.root.destroy(); }
}
