/**
 * 像素序列帧播放器：读取 animation_spec.json 的动画定义，按 file_pattern 展开帧路径，
 * 从 resources 加载 SpriteFrame 后按 fps 循环播放。所有帧路径来自资源清单，不硬编码。
 *
 * 用法：
 *   const anim = node.addComponent(PixelAnimator);
 *   await anim.play('mage_idle');       // 循环待机
 *   await anim.playOnce('mage_attack'); // 单次攻击
 */
import { Component, Sprite, SpriteFrame, _decorator } from 'cc';
import { loadFrame } from '../CocosResourceLoader';

const { ccclass, property } = _decorator;

/** animation_spec.json 中一条动画定义 */
export interface AnimDef {
  id: string;
  category: string;
  subject: string;
  state: string;
  frames: number;
  fps: number;
  loop: boolean;
  file_pattern: string;
}

/** 把 f{00-03} 这类占位展开为 00,01,02,03 的实际帧路径（相对 resources） */
export function expandFrames(pattern: string): string[] {
  const m = pattern.match(/^(.*)\{([0-9]+)-([0-9]+)\}(.*)$/);
  if (!m) return [pattern.replace(/\.png$/, '')];
  const out: string[] = [];
  const width = m[2].length;
  for (let i = parseInt(m[2], 10); i <= parseInt(m[3], 10); i++) {
    const num = String(i).padStart(width, '0');
    out.push((m[1] + num + m[4]).replace(/\.png$/, ''));
  }
  return out;
}

@ccclass('PixelAnimator')
export class PixelAnimator extends Component {
  @property(Sprite) sprite: Sprite | null = null;

  private spec: Record<string, AnimDef> = {};
  private cache = new Map<string, SpriteFrame[]>();
  private timer: any = null;
  private idx = 0;
  private currentId = '';

  /** 注入 animation_spec.json 的 animations 数组 */
  setSpec(animations: AnimDef[]): void {
    this.spec = {};
    animations.forEach(a => { this.spec[a.id] = a; });
  }

  /** 加载（带缓存）某动画的所有帧 */
  private async framesOf(id: string): Promise<{ frames: SpriteFrame[]; def: AnimDef }> {
    const def = this.spec[id];
    if (!def) throw new Error('动画不存在:' + id);
    let frames = this.cache.get(id);
    if (!frames) {
      const paths = expandFrames(def.file_pattern);
      frames = await Promise.all(paths.map(p => loadFrame(p)));
      this.cache.set(id, frames);
    }
    return { frames, def };
  }

  /** 循环播放 */
  async play(id: string): Promise<void> {
    this.stop();
    const { frames, def } = await this.framesOf(id);
    this.currentId = id;
    this.idx = 0;
    this.apply(frames);
    const interval = Math.max(30, Math.floor(1000 / (def.fps || 6)));
    this.timer = setInterval(() => {
      this.idx = (this.idx + 1) % frames.length;
      this.sprite!.spriteFrame = frames[this.idx];
    }, interval);
  }

  /** 单次播放，结束回调（用于攻击/受击/死亡） */
  async playOnce(id: string, onDone?: () => void): Promise<void> {
    this.stop();
    const { frames, def } = await this.framesOf(id);
    this.currentId = id;
    this.idx = 0;
    this.apply(frames);
    const interval = Math.max(30, Math.floor(1000 / (def.fps || 8)));
    this.timer = setInterval(() => {
      this.idx++;
      if (this.idx >= frames.length) {
        this.stop();
        onDone?.();
        return;
      }
      this.sprite!.spriteFrame = frames[this.idx];
    }, interval);
  }

  private apply(frames: SpriteFrame[]): void {
    if (!this.sprite) this.sprite = this.getComponent(Sprite);
    if (!this.sprite) throw new Error('PixelAnimator 需要 Sprite 组件');
    this.sprite.spriteFrame = frames[0];
  }

  stop(): void {
    if (this.timer) { clearInterval(this.timer); this.timer = null; }
  }

  get playing(): string { return this.currentId; }

  onDestroy(): void { this.stop(); }
}
