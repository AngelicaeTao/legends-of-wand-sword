/**
 * StandeeActor.js — 整体立绘动画驱动（Canvas 版，对应交付包 cocos_example/StandeeActor.ts）
 *
 * 交付包 §0.2：主角与 6 种职业服装改为「1 张整体立绘 + root 级变换」，
 * 不再切部件（AI 精致图带武器/披风，切开会碎）。怪物/宝箱仍用 PartActor。
 *
 * 资源：/res/characters/parts/<bundle>/<bundle>_standee.png（128×192，= 逻辑 64×96 的 2 倍导出）
 *       parts.json（standee_sprite_v1）+ actions.json（standee_actions_v1）
 * 坐标：原点 = 脚底中心，x 右为正，y 上为正（逻辑像素）。
 *
 * render(ctx,x,y,scale) 的 scale 语义与 PartActor 一致：逻辑像素→屏幕像素倍率。
 * 旧主角部件画布 32×48、世界内以 scale=2 绘制（屏幕 64×96）；
 * 新立绘逻辑 64×96，因此世界内传 scale=1 即得到同样的屏幕尺寸（比例锚点不变）。
 */
(function (global) {
  'use strict';

  const BASE = '/res/characters/parts/';
  const cache = new Map(); // bundle -> {ready, parts, actions, img, tint, frames}

  function loadBundle(bundle) {
    if (cache.has(bundle)) return cache.get(bundle);
    const entry = { bundle, ready: null, img: null, tint: null, frames: {} };
    const p = fetch(BASE + bundle + '/parts.json').then(r => (r.ok ? r.json() : null)).catch(() => null);
    const a = fetch(BASE + bundle + '/actions.json').then(r => (r.ok ? r.json() : null)).catch(() => null);
    entry.ready = Promise.all([p, a]).then(([parts, actions]) => {
      entry.parts = parts || {};
      entry.actions = actions || { states: {} };
      const file = (parts && parts.sprite && parts.sprite.file) || (bundle + '_standee.png');
      // v1.9.49: 逐帧序列（若 actions.json 某状态带 frames 字段则优先使用）
      const states = entry.actions.states || {};
      const jobs = [new Promise(res => {
        const im = new Image();
        im.onload = () => { entry.img = im; res(); };
        im.onerror = () => { entry.img = null; res(); };
        im.src = BASE + bundle + '/' + file;
      })];
      for (const st of Object.values(states)) {
        if (!st.frames) continue;
        for (const fn of st.frames) {
          jobs.push(new Promise(res => {
            const im = new Image();
            im.onload = () => { entry.frames[fn] = im; res(); };
            im.onerror = () => { res(); };
            im.src = BASE + bundle + '/frames/' + fn;
          }));
        }
      }
      return Promise.all(jobs);
    });
    cache.set(bundle, entry);
    return entry;
  }

  // 闪白用的白色剪影（每 bundle 只生成一次，按需）
  function tintOf(entry) {
    if (entry.tint || !entry.img) return entry.tint;
    const c = document.createElement('canvas');
    c.width = entry.img.naturalWidth; c.height = entry.img.naturalHeight;
    const g = c.getContext('2d');
    g.drawImage(entry.img, 0, 0);
    g.globalCompositeOperation = 'source-in';
    g.fillStyle = '#fff';
    g.fillRect(0, 0, c.width, c.height);
    entry.tint = c;
    return c;
  }

  const lerp = (a, b, r) => a + (b - a) * r;

  class StandeeActor {
    constructor(bundle) {
      this.bundle = bundle;
      this.entry = loadBundle(bundle);
      this.state = 'idle';
      this.loop = true;
      this.t0 = performance.now();
      this.onDone = null;
      this.cur = { dx: 0, dy: 0, rot: 0, sx: 1, sy: 1, alpha: 1, flash: 0 };
    }

    async init() { await this.entry.ready; return this; }

    play(state, onDone) {
      let st = this.entry.actions && this.entry.actions.states[state];
      // v1.9.49: 动作名别名（游戏用 die，新帧包用 death）
      if (!st && state === 'die') st = this.entry.actions && this.entry.actions.states['death'];
      if (!st) return;
      this.state = state;
      this.loop = !!st.loop;
      this.t0 = performance.now();
      this.onDone = onDone || null;
    }
    playOnce(state, onDone) { this.play(state, onDone); }

    /** 采样 root 变换（零分配，写入并复用 this.cur） */
    _sample(now) {
      const acts = this.entry.actions;
      let st = acts && acts.states[this.state];
      if (!st && this.state === 'die') st = acts && acts.states['death'];
      // v1.9.49: 帧序列状态不做 root 变换（动画已烘焙进帧）
      if (st && st.frames && st.frames.length) return this.cur;
      if (!st || !st.keys || !st.keys.length) return null;
      const keys = st.keys;
      const dur = keys[keys.length - 1].t || 1;
      let ms = now - this.t0;
      if (this.loop) ms = ms % dur;
      let i = 0;
      while (i < keys.length - 1 && keys[i + 1].t <= ms) i++;
      const a = keys[i].root || {}, b = keys[Math.min(i + 1, keys.length - 1)].root || {};
      const r = Math.max(0, Math.min(1, (ms - keys[i].t) / Math.max(1, keys[Math.min(i + 1, keys.length - 1)].t - keys[i].t)));
      const o = this.cur;
      o.dx = lerp(a.dx || 0, b.dx || 0, r);
      o.dy = lerp(a.dy || 0, b.dy || 0, r);
      o.rot = lerp(a.rot || 0, b.rot || 0, r);
      o.sx = lerp(a.sx == null ? 1 : a.sx, b.sx == null ? 1 : b.sx, r);
      o.sy = lerp(a.sy == null ? 1 : a.sy, b.sy == null ? 1 : b.sy, r);
      o.alpha = lerp(a._alpha == null ? 1 : a._alpha, b._alpha == null ? 1 : b._alpha, r);
      o.flash = lerp(a._flash || 0, b._flash || 0, r);
      if (!this.loop && ms >= dur && this.onDone) {
        const cb = this.onDone; this.onDone = null; cb();
      }
      return o;
    }

    /**
     * @param {CanvasRenderingContext2D} ctx
     * @param {number} x,y 屏幕上的脚底中心像素坐标
     * @param {number} scale 逻辑像素→屏幕像素倍率（1 = 屏幕 64×96，与世界内旧主角一致）
     * @param {object} opts {dir:-1|1 水平翻转, alpha:额外透明度}
     */
    render(ctx, x, y, scale = 1, opts = {}) {
      const e = this.entry;
      const now = performance.now();
      let st = e.actions && e.actions.states[this.state];
      if (!st && this.state === 'die') st = e.actions && e.actions.states['death'];
      // v1.9.49: 逐帧序列渲染
      if (st && st.frames && st.frames.length) {
        const imgs = st.frames.map(fn => e.frames[fn]).filter(Boolean);
        if (!imgs.length) return;
        const fps = st.fps || 3;
        const dur = imgs.length * (1000 / fps);
        let ms = now - this.t0;
        if (st.loop) ms = ms % dur;
        else if (ms >= dur) ms = dur - 1; // 单次动作停在末帧
        const idx = Math.min(imgs.length - 1, Math.floor(ms / (1000 / fps)));
        const im = imgs[idx];
        const w = 64 * scale, h = 96 * scale;
        const a = Math.max(0, Math.min(1, opts.alpha == null ? 1 : opts.alpha));
        if (a < 0.02) return;
        ctx.save();
        ctx.globalAlpha = a;
        ctx.imageSmoothingEnabled = w < im.naturalWidth;
        ctx.imageSmoothingQuality = 'high';
        ctx.translate(x, y);
        if ((opts.dir || 1) === -1) ctx.scale(-1, 1);
        ctx.drawImage(im, -w / 2, -h, w, h);
        ctx.restore();
        // 单次动作结束回调
        if (!st.loop && this.onDone && now - this.t0 >= dur) {
          const cb = this.onDone; this.onDone = null; cb();
        }
        return;
      }
      if (!e.img) return;
      const tf = this._sample(now);
      if (!tf) return;
      // 立绘 PNG = 逻辑尺寸 × 2
      const w = 64 * scale, h = 96 * scale;
      const a = Math.max(0, Math.min(1, tf.alpha * (opts.alpha == null ? 1 : opts.alpha)));
      if (a < 0.02) return;
      ctx.save();
      ctx.globalAlpha = a;
      // 立绘 128×192 是世界内 64×96 的 2 倍导出：缩小时做平均（否则最近邻丢像素、
      // AI 图的抖色会变成满身噪点），放大时保持最近邻（像素不糊）。
      ctx.imageSmoothingEnabled = w < e.img.naturalWidth;
      ctx.imageSmoothingQuality = 'high';
      ctx.translate(x + tf.dx * scale, y - tf.dy * scale);
      if (tf.rot) ctx.rotate(tf.rot * Math.PI / 180);
      ctx.scale((opts.dir || 1) * tf.sx, tf.sy);
      ctx.drawImage(e.img, -w / 2, -h, w, h);
      if (tf.flash > 0.01) {
        const t = tintOf(e);
        if (t) {
          ctx.globalAlpha = a * Math.min(1, tf.flash) * 0.75;
          ctx.drawImage(t, -w / 2, -h, w, h);
        }
      }
      ctx.restore();
    }
  }

  global.StandeeActor = StandeeActor;
  global.StandeeActor.preload = function (bundles) {
    return Promise.all((bundles || []).map(b => loadBundle(b).ready));
  };
})(window);
