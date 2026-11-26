/**
 * PartActor.js — 部件化角色/物件渲染器（Canvas 版，对应交付包 PartActor.ts）
 * 读 parts.json 组装部件；读 actions.json 关键帧线性插值驱动动作。
 * 坐标约定：角色原点=脚底中心，x右为正，y上为正（逻辑像素）；PNG 为 ×4 导出。
 * pivot=部件旋转中心（相对裁剪图左上角，图像坐标 y 向下）；anchor=pivot 在角色坐标的挂点（y 向上）。
 */
(function (global) {
  'use strict';

  const cache = new Map(); // bundle -> {parts, actions, imgs}
  const DEFAULT = { dx: 0, dy: 0, rot: 0, sx: 1, sy: 1, alpha: 1, flash: 0 };

  function lerp(a, b, r) { return a + (b - a) * r; }
  function norm(v) {
    if (!v || typeof v !== 'object') return { ...DEFAULT };
    return {
      dx: v.dx ?? 0, dy: v.dy ?? 0, rot: v.rot ?? 0,
      sx: v.sx ?? 1, sy: v.sy ?? 1, alpha: v.alpha ?? 1, flash: v.flash ?? 0,
    };
  }

  function loadBundle(bundle) {
    if (cache.has(bundle)) return cache.get(bundle);
    const p = fetch(`/res/v3/parts/${bundle}/parts.json`).then(r => r.json());
    const a = fetch(`/res/v3/parts/${bundle}/actions.json`).then(r => r.json());
    const entry = { ready: Promise.all([p, a]).then(([parts, actions]) => {
      entry.parts = parts; entry.actions = actions;
      // 性能：z 序排序只算一次（原实现每帧每实例 sort）
      entry.sortedParts = Object.keys(parts.parts || {}).sort((x, y) => ((parts.parts[x].z || 0) - (parts.parts[y].z || 0)));
      // 性能：每个状态预计算该状态用到的部件名列表（原实现每帧 Set+spread 求并集）
      for (const sname in actions.states) {
        const st = actions.states[sname];
        const seen = {};
        const list = [];
        for (const k of (st.keys || [])) for (const n in (k.parts || {}))
          if (n !== '_flash' && n !== '_alpha' && !seen[n]) { seen[n] = 1; list.push(n); }
        st._parts = list;
      }
      const imgs = {};
      const jobs = Object.entries(parts.parts).map(([name, info]) =>
        new Promise(res => {
          const im = new Image();
          im.onload = () => { imgs[name] = im; res(); };
          im.onerror = () => { imgs[name] = null; res(); };
          im.src = `/res/v3/parts/${bundle}/${info.file}`;
        })
      );
      return Promise.all(jobs).then(() => { entry.imgs = imgs; });
    }) };
    cache.set(bundle, entry);
    return entry;
  }

  class PartActor {
    constructor(bundle) {
      this.bundle = bundle;
      this.entry = loadBundle(bundle);
      this.state = 'idle';
      this.loop = true;
      this.t0 = performance.now();
      this.onDone = null;
      this.cur = { root: { ...DEFAULT } };
    }

    async init() { await this.entry.ready; return this; }

    play(state, onDone) {
      const st = this.entry.actions && this.entry.actions.states[state];
      if (!st) return;
      this.state = state;
      this.loop = !!st.loop;
      this.t0 = performance.now();
      this.onDone = onDone || null;
    }
    playOnce(state, onDone) { this.play(state, onDone); }

    /** 当前动作在 now 时刻的插值变换集合（零分配：写入并复用 this.cur） */
    _sample(now) {
      const acts = this.entry.actions;
      if (!acts) return null;
      const st = acts.states[this.state];
      if (!st || !st.keys || !st.keys.length) return null;
      const ms0 = now - this.t0;
      const keys = st.keys;
      const dur = keys[keys.length - 1].t;
      const ms = this.loop ? ms0 % Math.max(1, dur) : ms0;
      let i = 0;
      while (i < keys.length - 1 && keys[i + 1].t <= ms) i++;
      const a = keys[i], b = keys[Math.min(i + 1, keys.length - 1)];
      const r = Math.max(0, Math.min(1, (ms - a.t) / Math.max(1, b.t - a.t)));
      const cur = this.cur;
      const ap = a.parts || {}, bp = b.parts || {};
      // 预计算的 st._parts（该状态涉及的部件名），避免每帧 Set/spread 求并集
      const names = st._parts || (st._parts = Object.keys(ap).concat(Object.keys(bp)).filter((n, idx, arr) => n !== '_flash' && n !== '_alpha' && arr.indexOf(n) === idx));
      for (let ni = 0; ni < names.length; ni++) {
        const name = names[ni];
        const av = ap[name], bv = bp[name];
        let o = cur[name];
        if (!o) o = cur[name] = { dx: 0, dy: 0, rot: 0, sx: 1, sy: 1, alpha: 1, flash: 0 };
        o.dx = lerp(av ? (av.dx || 0) : 0, bv ? (bv.dx || 0) : 0, r);
        o.dy = lerp(av ? (av.dy || 0) : 0, bv ? (bv.dy || 0) : 0, r);
        o.rot = lerp(av ? (av.rot || 0) : 0, bv ? (bv.rot || 0) : 0, r);
        o.sx = lerp(av ? (av.sx == null ? 1 : av.sx) : 1, bv ? (bv.sx == null ? 1 : bv.sx) : 1, r);
        o.sy = lerp(av ? (av.sy == null ? 1 : av.sy) : 1, bv ? (bv.sy == null ? 1 : bv.sy) : 1, r);
        o.alpha = lerp(av ? (av.alpha == null ? 1 : av.alpha) : 1, bv ? (bv.alpha == null ? 1 : bv.alpha) : 1, r);
        o.flash = lerp(av ? (av.flash || 0) : 0, bv ? (bv.flash || 0) : 0, r);
      }
      cur._alpha = lerp(typeof ap._alpha === 'number' ? ap._alpha : 1,
                        typeof bp._alpha === 'number' ? bp._alpha : 1, r);
      cur._flash = lerp(typeof ap._flash === 'number' ? ap._flash : 0,
                        typeof bp._flash === 'number' ? bp._flash : 0, r);
      // 上个动作遗留、当前状态未定义的部件：立即回默认姿态（与原实现语义一致，
      // 否则 idle 会冻结 walk 的手臂/腿摆动姿态）
      for (const n in cur) {
        if (n === '_alpha' || n === '_flash') continue;
        if (ap[n] === undefined && bp[n] === undefined) {
          const o = cur[n];
          o.dx = 0; o.dy = 0; o.rot = 0; o.sx = 1; o.sy = 1; o.alpha = 1; o.flash = 0;
        }
      }
      if (!this.loop && ms >= dur && this.onDone) {
        const cb = this.onDone; this.onDone = null; cb();
      }
      return cur;
    }

    /**
     * 绘制角色/物件。
     * @param ctx Canvas 2D 上下文
     * @param x,y 屏幕上角色脚底中心的像素坐标
     * @param scale 逻辑像素→屏幕像素倍率（默认 4）
     * @param opts {dir:-1|1 水平翻转, tint:bool 闪白}
     */
    render(ctx, x, y, scale = 4, opts = {}) {
      if (!this.entry.parts || !this.entry.imgs) return;
      const tf = this._sample(performance.now());
      if (!tf) return;
      const parts = this.entry.parts.parts || {};
      const root = tf.root || DEFAULT;
      ctx.save();
      ctx.translate(x + root.dx * scale, y - root.dy * scale);
      if (root.rot) ctx.rotate(root.rot * Math.PI / 180);
      ctx.scale((opts.dir || 1) * root.sx, root.sy);
      const a0 = Math.max(0, Math.min(1, (tf._alpha ?? 1) * root.alpha));
      // 近透明时不绘制：极小 alpha + 极扁 sy 的组合会被采样成一条杂色横带
      if (a0 < 0.02 || Math.abs(root.sy) < 0.04) { ctx.restore(); return; }
      ctx.globalAlpha = a0;
      const flashAll = (tf._flash || 0) > 0;
      const names = this.entry.sortedParts || Object.keys(parts); // 预排序，免每帧 sort
      for (const name of names) {
        const info = parts[name], im = this.entry.imgs[name];
        if (!im) continue;
        const t = tf[name] || DEFAULT;
        // 旋转中心 = pivot（图像坐标，y向下）：px=pivot.x*scale, py=pivot.y*scale（图像内）
        // 部件绘制原点：anchor 为 pivot 在角色坐标（y向上）的位置 → 屏幕 (anchor.x*scale, -anchor.y*scale)
        const ax = info.anchor[0] * scale + t.dx * scale;
        const ay = -info.anchor[1] * scale - t.dy * scale;
        const px = info.pivot[0] * scale, py = info.pivot[1] * scale;
        const iw = info.size[0] * scale, ih = info.size[1] * scale;
        ctx.save();
        ctx.translate(ax, ay);
        if (t.rot) {
          ctx.translate(px, py);
          ctx.rotate(t.rot * Math.PI / 180);
          ctx.translate(-px, -py);
        }
        ctx.scale(t.sx, t.sy);
        ctx.drawImage(im, -px, -py, iw, ih);
        // 受击闪白：在部件图区域叠一层半透明白（限制在矩形内，不污染背景）
        if (flashAll || (t.flash || 0) > 0) {
          ctx.globalAlpha = Math.max(0, Math.min(1, (flashAll ? tf._flash : t.flash) * 0.5));
          ctx.fillStyle = '#fff';
          ctx.fillRect(-px, -py, iw, ih);
        }
        ctx.restore();
      }
      ctx.restore();
    }
  }

  global.PartActor = PartActor;
  global.PartActor.preload = function (bundles) {
    return Promise.all((bundles || []).map(b => loadBundle(b).ready));
  };
})(window);
