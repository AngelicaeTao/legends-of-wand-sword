/**
 * creator.js — 角色创建与换装界面（交付包 §4.1 / §4.2 / §2.2 的运行时实现）
 *
 * create 模式（首次进入）：① 职业（战士 / 法师 + 初始技能）→ ② 外观（6 槽 × 4 选项 + 不戴帽）
 *                          → ③ 命名 → 提交服务端 create_character
 * look 模式（家园换装）：只做 ② 外观，提交服务端 update_look（只改 appearance，不动职业/装备/技能）
 *
 * 预览：职业步显示整体立绘（StandeeActor），外观步显示 6 部件自由组合（Look.draw）
 */
(function (global) {
  'use strict';
  // v1.9.65 职业→立绘 bundle：统一走 app.js 下发的 window.CLASS_BUNDLES（战士/骑士素材对调过）
  const cbOf = (id) => (global.CLASS_BUNDLES && global.CLASS_BUNDLES[id]) || ('char_' + id);

  const $id = (i) => document.getElementById(i);
  // HTML 转义：姓名/职业/技能等动态文本拼进 innerHTML/属性前统一处理
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const SLOT_ORDER = ['face', 'hair', 'top', 'pants', 'shoes', 'hat'];
  const NOUN = { face: '脸型', hair: '发型', top: '上衣', pants: '裤子', shoes: '鞋子', hat: '帽子' };

  let o = null;          // {mode, api, uid, cfg, onDone, onCancel, sfx, startLook}
  let mode = 'create';
  let step = 1;
  let cls = 'warrior';
  let look = {};
  let name = '';
  let slot = 'face';
  let actor = null;      // StandeeActor（职业预览）
  let rafId = 0;
  let busy = false;

  function pool(classId) {
    const s = o.cfg && o.cfg.skills && o.cfg.skills.classes;
    return (s && s[classId]) || { name: classId, initial: [], advanced: [], tier: 0 };
  }
  function skillById(id) {
    const all = (o.cfg && o.cfg.skills && o.cfg.skills.skills) || [];
    return all.find(x => x.id === id) || { id, name: id };
  }
  function defaultLook() {
    const m = o.cfg && o.cfg.creator;
    const out = {};
    if (m && m.slots) {
      for (const s of SLOT_ORDER) {
        const ops = (m.slots[s] && m.slots[s].options) || [];
        if (ops.length) out[s] = ops[0].id;
      }
    }
    return out;
  }

  /* ---------------- 预览绘制 ---------------- */
  function tick() {
    rafId = 0;
    const cv = $id('crCanvas');
    if (!cv || $id('createView').classList.contains('hidden')) return;
    const g = cv.getContext('2d');
    g.clearRect(0, 0, cv.width, cv.height);
    // 地面投影
    const cx = cv.width / 2, footY = cv.height - 18;
    const grd = g.createRadialGradient(cx, footY + 6, 2, cx, footY + 6, 62);
    grd.addColorStop(0, 'rgba(20,26,20,.34)');
    grd.addColorStop(1, 'rgba(20,26,20,0)');
    g.save(); g.translate(cx, footY + 6); g.scale(1, 0.34); g.translate(-cx, -(footY + 6));
    g.fillStyle = grd; g.beginPath(); g.arc(cx, footY + 6, 62, 0, Math.PI * 2); g.fill(); g.restore();
    let drawn = false;
    if (step === 1 && mode === 'create') {
      // 职业立绘：2 倍图 128×192 → 4 倍整数放大（256×384），像素不重采样
      if (actor) { actor.render(g, cx, footY, 4, {}); drawn = true; }
    } else {
      drawn = global.Look.drawFit(g, cx, footY, cv.height - 40, look);
    }
    if (!drawn) {
      g.fillStyle = 'rgba(240,213,144,.5)';
      g.font = '13px "Microsoft YaHei",sans-serif';
      g.textAlign = 'center';
      g.fillText('外观加载中…', cx, footY - 120);
    }
    rafId = requestAnimationFrame(tick);
  }
  function kick() { if (!rafId) rafId = requestAnimationFrame(tick); }

  /* ---------------- 渲染 ---------------- */
  function renderHead() {
    const t = $id('crTitle'), st = $id('crSteps');
    if (mode === 'look') {
      t.textContent = '家园换装';
      st.innerHTML = '<span class="on">外观</span>';
    } else {
      t.textContent = '角色创建';
      st.innerHTML = [1, 2, 3].map(n => `<span class="${n === step ? 'on' : (n < step ? 'done' : '')}">${n} ${['职业', '外观', '命名'][n - 1]}</span>`).join('');
    }
  }

  function renderPanel() {
    const body = $id('crPanel');
    if (step === 1 && mode === 'create') {
      const cards = ['warrior', 'mage'].map(id => {
        const p = pool(id);
        const c = ((o.cfg.classes && o.cfg.classes.classes) || []).find(x => x.id === id) || {};
        const sk = (p.initial || []).map(sid => {
          const s = skillById(sid);
          return `<span class="cr-chip"><b>${esc(s.name)}</b><i>CD ${s.cd || 2}s</i></span>`;
        }).join('');
        const b = c.bonus || {};
        const stat = (k, label) => {
          const v = b[k] == null ? 1 : b[k];
          const cls = v >= 1.08 ? 'up' : (v <= 0.95 ? 'down' : '');
          return `<span class="cs ${cls}">${label}<b>${Math.round(v * 100)}%</b></span>`;
        };
        const stats = `<span class="cr-stats">${stat('hp', '生命')}${stat('atk', '攻击')}${stat('def', '防御')}${stat('crit', '暴击')}</span>`;
        return `<button class="cr-class ${cls === id ? 'on' : ''}" data-cls="${id}">
          <span class="cc-pic"><img src="/res/characters/parts/${cbOf(id)}/${cbOf(id)}_standee.png" alt=""></span>
          <span class="cc-txt">
            <b>${esc(c.name || p.name)}</b>
            <em>${esc(c.desc || '')}</em>
            <span class="cr-chips">${sk}</span>
            ${stats}
          </span>
        </button>`;
      }).join('');
      body.innerHTML = `<div class="cr-sec">选择开局职业（进阶职业需 Lv.44 且开启第 2 世界后转职）</div>
        <div class="cr-classes">${cards}</div>
        <div class="cr-tip">开局自带 2 个初始技能，技能库只显示本职业技能，跨职业技能不可装配。</div>`;
      body.querySelectorAll('.cr-class').forEach(el => el.onclick = () => {
        cls = el.dataset.cls;
        o.sfx && o.sfx.tab();
        actor = new global.StandeeActor(cbOf(cls));
        actor.init().then(() => actor.play('idle'));
        renderPanel(); renderHead(); updateName();
      });
      return;
    }

    if (step === 2) {
      const m = o.cfg.creator;
      const tabs = SLOT_ORDER.map(s => {
        const cur = look[s] === 'none' ? '不戴帽' : ((global.Look.optOf(s, look[s]) || {}).name || '');
        return `<button class="cr-tab ${s === slot ? 'on' : ''}" data-slot="${s}">${NOUN[s]}<i>${esc(cur)}</i></button>`;
      }).join('');
      const opts = [];
      const list = (m.slots[slot] && m.slots[slot].options) || [];
      for (const op of list) {
        opts.push(`<button class="cr-opt ${look[slot] === op.id ? 'on' : ''}" data-opt="${op.id}">
          <span class="oi"><img src="${global.Look.BASE + op.file}" alt=""></span><em>${esc(op.name)}</em></button>`);
      }
      if (slot === 'hat') {
        opts.push(`<button class="cr-opt ${look.hat === 'none' ? 'on' : ''}" data-opt="none">
          <span class="oi none">✕</span><em>不戴帽</em></button>`);
      }
      body.innerHTML = `<div class="cr-tabs">${tabs}</div><div class="cr-opts">${opts.join('')}</div>
        <div class="cr-tip">叠放顺序 face → hair → hat → top → pants → shoes（发型罩头顶、不挡五官）。帽子与长发可叠，选「不戴帽」可避开。</div>`;
      body.querySelectorAll('.cr-tab').forEach(el => el.onclick = () => {
        slot = el.dataset.slot; o.sfx && o.sfx.tab(); renderPanel();
      });
      body.querySelectorAll('.cr-opt').forEach(el => el.onclick = () => {
        look[slot] = el.dataset.opt; o.sfx && o.sfx.click(); renderPanel();
      });
      return;
    }

    // step 3：命名
    const p = pool(cls);
    const c = ((o.cfg.classes && o.cfg.classes.classes) || []).find(x => x.id === cls) || {};
    const sk = (p.initial || []).map(sid => skillById(sid).name).join(' · ');
    const lookTxt = SLOT_ORDER.map(s => (look[s] === 'none' ? '不戴帽' : ((global.Look.optOf(s, look[s]) || {}).name || ''))).filter(Boolean).join(' · ');
    body.innerHTML = `
      <div class="cr-sec">给角色取个名字</div>
      <div class="cr-name"><input id="crNameInput" maxlength="12" placeholder="输入角色名（最多 12 字）" value="${esc(name)}"></div>
      <div class="cr-sum">
        <div class="sr"><span>职业</span><b>${esc(c.name || p.name)}</b></div>
        <div class="sr"><span>初始技能</span><b>${esc(sk)}</b></div>
        <div class="sr"><span>家园外观</span><b class="wide">${esc(lookTxt)}</b></div>
      </div>
      <div class="cr-tip">创建后进入大世界；家园里可随时「换装」，外观只影响家园与创建预览，探索/战斗使用职业装备装立绘。</div>`;
    const inp = $id('crNameInput');
    inp.oninput = () => { name = inp.value; updateName(); };
    inp.onkeydown = (e) => { if (e.key === 'Enter') submit(); };
  }

  function updateName() {
    const nm = $id('crPvName'), cl = $id('crPvCls');
    if (nm) nm.textContent = (name || '').trim() || '未命名';
    if (cl) cl.textContent = pool(cls).name || cls;
  }

  function renderFoot() {
    const back = $id('crBack'), next = $id('crNext');
    if (mode === 'look') {
      back.textContent = '放弃';
      next.textContent = '保存外观';
      back.classList.remove('hidden');
      return;
    }
    back.textContent = '上一步';
    back.classList.toggle('hidden', step === 1);
    next.textContent = step === 3 ? '完成创建' : '下一步';
  }

  function renderAll() { renderHead(); renderPanel(); renderFoot(); updateName(); kick(); }

  /* ---------------- 交互 ---------------- */
  function next() {
    if (busy) return;
    if (mode === 'look') return submit();
    if (step === 1) { step = 2; o.sfx && o.sfx.click(); renderAll(); return; }
    if (step === 2) { step = 3; o.sfx && o.sfx.click(); renderAll(); return; }
    submit();
  }
  function back() {
    if (busy) return;
    if (mode === 'look') { o.sfx && o.sfx.panelClose(); close(true); return; }
    if (step === 1) return;
    step--; o.sfx && o.sfx.click(); renderAll();
  }

  async function submit() {
    if (busy) return;
    const nm = (name || '').trim();
    if (mode === 'create' && !nm) { o.toast('还差一步', '请先输入角色名'); return; }
    busy = true;
    const btn = $id('crNext'); btn.textContent = '提交中…';
    try {
      let r;
      if (mode === 'look') {
        r = await o.api('/api/player/action', { userId: o.uid, action: 'update_look', homeLook: look });
        if (r.state) o.onDone && o.onDone(r.state);
        o.sfx && o.sfx.equip();
      } else {
        r = await o.api('/api/player/create_character', { userId: o.uid, name: nm, classId: cls, homeLook: look, skills: (pool(cls).initial || []) });
        o.sfx && o.sfx.login();
        o.onDone && o.onDone(r.state);
      }
      close(false);
    } catch (e) {
      o.sfx && o.sfx.fail();
      o.toast(mode === 'look' ? '换装失败' : '创建失败', e.message);
      btn.textContent = mode === 'look' ? '保存外观' : '完成创建';
    } finally { busy = false; }
  }

  function close(cancel) {
    if (rafId) { cancelAnimationFrame(rafId); rafId = 0; }
    $id('createView').classList.add('hidden');
    if (cancel && o && o.onCancel) o.onCancel();
  }

  function open(opts) {
    o = opts;
    mode = opts.mode || 'create';
    step = mode === 'look' ? 2 : 1;
    cls = (opts.classId && pool(opts.classId).tier === 0) ? opts.classId : 'warrior';
    look = Object.assign(defaultLook(), opts.look || {});
    name = opts.name || '';
    slot = 'face';
    $id('createView').classList.remove('hidden');
    actor = new global.StandeeActor(cbOf(cls));
    actor.init().then(() => actor.play('idle'));
    global.Look.preloadAll().then(kick);
    renderAll();
    $id('crBack').onclick = back;
    $id('crNext').onclick = next;
  }

  global.Creator = { open, close };
})(window);
