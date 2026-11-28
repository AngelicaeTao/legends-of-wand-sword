/* ============================================================
   杖剑传说 · 主线任务追踪（v1.9）
   ------------------------------------------------------------
   设计目标：让玩家一进大世界就知道"下一步该干什么"。
   - 五步新手主线：击杀 → 开箱 → 装备 → 强化 → 走出草原
   - 右上角常驻追踪卡：当前任务 + 下一步（灰），进度条 + 奖励
   - 完成后卡面转金，点「领取」调服务端 quest_claim（奖励金额服务端白名单）
   - 进度本地持久化（localStorage / 按账号隔离），刷新不丢
   ============================================================ */
(function(){
'use strict';

const QDEFS=[
  {id:'q_kill3',   name:'初试身手', desc:'击败 3 只怪物',            goal:3, kind:'kill',    ico:'sword',  reward:'800 金币 · 30 钻石'},
  {id:'q_chest1',  name:'意外之财', desc:'开启 1 个宝箱',            goal:1, kind:'chest',   ico:'chest',  reward:'600 金币 · 20 钻石'},
  {id:'q_equip4',  name:'全副武装', desc:'穿戴 4 件装备',            goal:4, kind:'equip',   ico:'armor',  reward:'900 金币 · 40 钻石'},
  {id:'q_enhance1',name:'精益求精', desc:'强化装备 1 次',            goal:1, kind:'enhance', ico:'hammer', reward:'700 金币 · 25 钻石'},
  {id:'q_travel',  name:'走出草原', desc:'抵达迷雾森林（W2）',       goal:1, kind:'travel',  ico:'map',    reward:'1,500 金币 · 80 钻石'}
];

let uid='', st={progress:{},claimed:{}}, hooks={};
const $=id=>document.getElementById(id);
// HTML 转义：任务名/描述等动态文本拼进 innerHTML 前统一处理
const esc=s=>String(s==null?'':s).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));

function key(){ return 'zjcs_quest_'+uid; }
function load(){
  try{ st=JSON.parse(localStorage.getItem(key())||'null')||{}; }catch(e){ st={}; }
  st.progress=st.progress||{}; st.claimed=st.claimed||{};
}
function save(){ try{ localStorage.setItem(key(),JSON.stringify(st)); }catch(e){} }
function claimedCount(){ return QDEFS.filter(q=>st.claimed[q.id]).length; }
function activeIdx(){ for(let i=0;i<QDEFS.length;i++) if(!st.claimed[QDEFS[i].id]) return i; return -1; }
function done(q){ return (st.progress[q.kind]||0) >= q.goal; }

function render(){
  // v1.9.45 收起/展开：内容写进 #qtBody，外层 #questTracker 只保留表头与折叠按钮，
  // 这样切页签/重渲染都不会把折叠状态或按钮冲掉。
  const outer=$('questTracker');
  const box=$('qtBody')||outer;
  if(!outer||!box) return;
  const ai=activeIdx();
  if(ai<0){
    box.innerHTML=`<div class="qt-head"><span>主线 · 第一章</span><em>${QDEFS.length}/${QDEFS.length}</em></div>
      <div class="qt-all">本章主线已全部完成 · 去更远的世界探索吧</div>`;
    outer.classList.add('qt-all-done');
    return;
  }
  outer.classList.remove('qt-all-done');
  const list=QDEFS.slice(ai,ai+2);
  let html=`<div class="qt-head"><span>主线 · 第一章</span><em>${claimedCount()}/${QDEFS.length}</em></div>`;
  list.forEach((q,i)=>{
    const p=Math.min(st.progress[q.kind]||0,q.goal), ok=done(q)&&i===0;   // 只有当前任务可领，后续任务仅展示进度
    html+=`<div class="qt-item${i>0?' next':''}${ok?' done':''}" data-q="${q.id}">
      <span class="qt-ico">${(window.ICON&&ICON[q.ico])?ICON[q.ico](22,ok?'#7fe0a0':'#f0d590'):''}</span>
      <div class="qt-main">
        <div class="qt-name">${esc(q.name)}</div>
        <div class="qt-desc">${esc(q.desc)}</div>
        <div class="qt-bar${p>=q.goal?' full':''}"><i style="width:${Math.round(p/q.goal*100)}%"></i></div>
      </div>
      <span class="qt-right">${ok?'<b class="qt-claim">领取</b>':`<em>${p}/${q.goal}</em>`}</span>
    </div>`;
  });
  box.innerHTML=html;
  box.querySelectorAll('.qt-claim').forEach(b=>{
    b.onclick=ev=>{ ev.stopPropagation(); claim(b.closest('.qt-item').dataset.q); };
  });
  // 悬停显示奖励
  box.querySelectorAll('.qt-item').forEach(el=>{
    const q=QDEFS.find(x=>x.id===el.dataset.q);
    el.title=q?('奖励：'+q.reward):'';
  });
}

async function claim(qid){
  const q=QDEFS.find(x=>x.id===qid);
  if(!q||!done(q)||st.claimed[qid]) return;
  if(QDEFS[activeIdx()]&&QDEFS[activeIdx()].id!==qid) return;   // 顺序解锁：只能领当前任务
  try{
    const r=await hooks.claim(qid);
    st.claimed[qid]=true; save();
    if(r&&r.state&&hooks.onState) hooks.onState(r.state);
    hooks.toast&&hooks.toast('任务完成 · '+q.name,'奖励已入账：'+q.reward);
    render();
  }catch(e){
    // v1.9.19 自愈：服务端已领取但本地未同步（换设备/清缓存）→ 自动校准，不再报错
    if(e.message&&e.message.indexOf('已领取')>=0){
      st.claimed[qid]=true;
      if((st.progress[q.kind]||0)<q.goal) st.progress[q.kind]=q.goal;
      save(); render();
      hooks.toast&&hooks.toast('任务已同步','「'+q.name+'」奖励此前已领取，进度已自动校准');
      return;
    }
    hooks.toast&&hooks.toast('领取失败',e.message);
  }
}

window.Quest={
  init(userId,h){
    uid=userId||'guest'; hooks=h||{};
    load();
    if(hooks.getState){
      const s=hooks.getState();
      if(s&&s.equip) st.progress.equip=s.equip.filter(e=>e.equipped).length;
      // v1.9.19 服务端权威同步：合并服务端已领取状态，自动检测并校正任务进度
      // 解决：换浏览器/清 localStorage 后本地领取记录丢失，面板误显示「领取」
      const sq=(s&&s.ext&&s.ext.quests)||null;
      if(sq){
        let fixed=0;
        QDEFS.forEach(q=>{
          if(sq[q.id]===true){
            if(!st.claimed[q.id]){ st.claimed[q.id]=true; fixed++; }
            if((st.progress[q.kind]||0)<q.goal) st.progress[q.kind]=q.goal;
          }
        });
        if(fixed>0) save();
      }
    }
    save(); render();
  },
  progress(kind,n){
    if(!kind)return;
    st.progress[kind]=(st.progress[kind]||0)+(n==null?1:n);
    save(); render();
  },
  set(kind,v){
    if(!kind)return;
    const old=st.progress[kind]||0;
    if(v<=old)return;
    st.progress[kind]=v; save(); render();
  },
  active(){ const i=activeIdx(); return i<0?null:QDEFS[i]; },
  list(){ return QDEFS; },
  render
};
})();
