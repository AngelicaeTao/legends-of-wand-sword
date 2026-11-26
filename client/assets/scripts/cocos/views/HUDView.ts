/**
 * HUD：顶部金币/钻石/体力/等级 + 底部只读技能栏。
 * 纯展示 GameState 快照，不做任何决策。
 */
import { Node, Sprite, Label, Color, UITransform, _decorator } from 'cc';
import { GameState } from '../../core/GameState';
import { ConfigManager } from '../../core/ConfigManager';

const { ccclass } = _decorator;

function mkNode(name: string, w: number, h: number, x: number, y: number): Node {
  const n = new Node(name);
  const t = n.addComponent(UITransform);
  t.setContentSize(w, h);
  n.setPosition(x, y, 0);
  return n;
}

@ccclass('HUDView')
export class HUDView {
  private info: Label;
  private skillBar: Label;

  constructor(private root: Node, private state: GameState) {
    this.info = this.label('Info', '加载中…', 34, Color.WHITE, 1500, 60, 0, 460);
    this.root.addChild(this.info.node);
    this.skillBar = this.label('SkillBar', '技能：只读回显', 28, new Color(200, 220, 255), 1500, 50, 0, -470);
    this.root.addChild(this.skillBar.node);
    state.onChange = () => this.refresh();
    this.refresh();
  }

  private label(name: string, text: string, size: number, color: Color, w: number, h: number, x: number, y: number): Label {
    const n = mkNode(name, w, h, x, y);
    const l = n.addComponent(Label);
    l.string = text; l.fontSize = size; l.lineHeight = size + 6; l.color = color;
    return l;
  }

  refresh(): void {
    const s = this.state.snapshot;
    if (!s) { this.info.string = '未登录'; return; }
    this.info.string = `Lv.${s.level}  ${s.classId}  战力 ${s.battlePower}  |  金币 ${s.gold}  钻石 ${s.gem}  体力 ${s.stamina}`;
    const names = this.state.bd.map(id => ConfigManager.instance.skillById(id)?.name || id);
    this.skillBar.string = '技能(只读)：' + (names.length ? names.join(' · ') : '待配置') + ' + 普攻';
  }

  destroy(): void { this.root.destroy(); }
}
